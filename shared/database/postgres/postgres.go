package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// DB is the contract repositories depend on.
//
// Db returns a concrete *gorm.DB rather than the reference project's
// interface{}, so callers no longer need an unchecked type assertion at every
// query site.
type DB interface {
	// Db returns the handle to run queries on: the ambient transaction when ctx
	// carries one, otherwise the pool. Always call this instead of holding a
	// *gorm.DB, or the query will escape the caller's transaction.
	Db(ctx context.Context) *gorm.DB

	// WithTransaction runs fn inside a transaction managed by the transaction pool,
	// committing on nil error and rolling back otherwise.
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error

	// WithPooledTransaction runs fn inside a transaction managed by the transaction pool
	// with custom options (e.g. isolation levels, read-only, per-tx timeouts, retries).
	WithPooledTransaction(ctx context.Context, fn func(ctx context.Context) error, opts ...TxOption) error

	// TxPool returns the underlying transaction pool for stats and metrics.
	TxPool() *TxPool

	// Ping verifies the connection is alive.
	Ping(ctx context.Context) error

	// Close drains the pool.
	Close() error
}

// Client is the GORM-backed DB implementation.
type Client struct {
	db     *gorm.DB
	cfg    Config
	log    *slog.Logger
	txPool *TxPool
}

// compile-time check that Client satisfies DB.
var _ DB = (*Client)(nil)

// New connects, applies the pool settings, initializes the transaction pool, and verifies the connection.
func New(ctx context.Context, cfg Config) (*Client, error) {
	cfg.normalize()
	log := logger.Module("postgres")

	gormDB, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger:                 newGormLogger(cfg),
		PrepareStmt:            cfg.PrepareStmt,
		SkipDefaultTransaction: cfg.SkipDefaultTransaction,
		// Store and compare timestamps in UTC; letting the server's local zone
		// leak in makes rows written by different hosts inconsistent.
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: open %s/%s: %w", cfg.Addr(), cfg.DBName, err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres: underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	c := &Client{db: gormDB, cfg: cfg, log: log}

	// Initialize transaction pool with configured bounds
	c.txPool = NewTxPool(c, TxPoolConfig{
		Capacity:       cfg.TxPoolSize,
		AcquireTimeout: cfg.TxAcquireTimeout,
		MaxRetries:     cfg.TxMaxRetries,
		RetryBackoff:   cfg.TxRetryBackoff,
	})

	// gorm.Open is lazy, so a bad host or password surfaces only on first use.
	// Ping here to turn a misconfiguration into a boot failure.
	if err := c.Ping(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	log.Info("connected",
		slog.String("addr", cfg.Addr()),
		slog.String("database", cfg.DBName),
		slog.Int("max_open_conns", cfg.MaxOpenConns),
		slog.Int("max_idle_conns", cfg.MaxIdleConns),
		slog.Int("tx_pool_size", cfg.TxPoolSize),
		slog.Duration("conn_max_lifetime", cfg.ConnMaxLifetime),
		slog.Duration("conn_max_idle_time", cfg.ConnMaxIdleTime))

	return c, nil
}

// NewWithGorm wraps an existing *gorm.DB, for tests and for callers that build
// their own dialector.
func NewWithGorm(db *gorm.DB, cfg Config) *Client {
	cfg.normalize()
	c := &Client{db: db, cfg: cfg, log: logger.Module("postgres")}
	c.txPool = NewTxPool(c, TxPoolConfig{
		Capacity:       cfg.TxPoolSize,
		AcquireTimeout: cfg.TxAcquireTimeout,
		MaxRetries:     cfg.TxMaxRetries,
		RetryBackoff:   cfg.TxRetryBackoff,
	})
	return c
}

// Gorm returns the root handle, bypassing any ambient transaction. Reserve it
// for migrations and admin tasks; request paths must use Db.
func (c *Client) Gorm() *gorm.DB { return c.db }

// Config reports the resolved configuration.
func (c *Client) Config() Config { return c.cfg }

// TxPool returns the active transaction pool.
func (c *Client) TxPool() *TxPool { return c.txPool }

// Db returns the ambient transaction when present, otherwise the pool bound to
// ctx so query cancellation follows the request.
//
// The name keeps the spelling used by the project this was ported from, so
// existing repositories read the same after the move; Go style would prefer DB,
// but that collides with the DB interface this type implements.
func (c *Client) Db(ctx context.Context) *gorm.DB { //nolint:staticcheck // ST1003: see doc comment
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return c.db.WithContext(ctx)
}

// WithTransaction runs fn in a transaction governed by the transaction pool.
//
// When ctx is already inside a transaction, fn runs in a nested savepoint
// without acquiring a new pool permit. That keeps a usecase composable — it can be
// called standalone or from within a larger transaction — and a failure rolls
// back only fn's own work rather than the entire outer transaction.
//
// Rollback on panic is handled by GORM's Transaction, which rolls back and
// re-panics so the stack is preserved.
func (c *Client) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if c.txPool != nil {
		return c.txPool.Execute(ctx, fn)
	}
	if tx, ok := TxFromContext(ctx); ok {
		return tx.Transaction(func(nested *gorm.DB) error {
			return fn(contextWithTx(ctx, nested))
		})
	}
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(contextWithTx(ctx, tx))
	})
}

// WithPooledTransaction executes fn inside a transaction managed by TxPool
// with custom options (isolation levels, timeouts, retries).
func (c *Client) WithPooledTransaction(ctx context.Context, fn func(ctx context.Context) error, opts ...TxOption) error {
	if c.txPool != nil {
		return c.txPool.Execute(ctx, fn, opts...)
	}
	return c.WithTransaction(ctx, fn)
}

// WithDirectTransaction runs fn in a transaction directly against the connection pool,
// bypassing the transaction pool's concurrency limiter.
func (c *Client) WithDirectTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if tx, ok := TxFromContext(ctx); ok {
		return tx.Transaction(func(nested *gorm.DB) error {
			return fn(contextWithTx(ctx, nested))
		})
	}
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(contextWithTx(ctx, tx))
	})
}

// Ping verifies the connection, bounded by ConnectTimeout.
func (c *Client) Ping(ctx context.Context) error {
	sqlDB, err := c.db.DB()
	if err != nil {
		return fmt.Errorf("postgres: underlying sql.DB: %w", err)
	}
	if c.cfg.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.ConnectTimeout)
		defer cancel()
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres: ping %s/%s: %w", c.cfg.Addr(), c.cfg.DBName, err)
	}
	return nil
}

// Stats exposes pool counters for a metrics or health endpoint.
func (c *Client) Stats() (open, inUse, idle int, waitCount int64) {
	sqlDB, err := c.db.DB()
	if err != nil {
		return 0, 0, 0, 0
	}
	s := sqlDB.Stats()
	return s.OpenConnections, s.InUse, s.Idle, s.WaitCount
}

// Close drains the pool. It is registered with the shutdown registry.
func (c *Client) Close() error {
	if c.txPool != nil {
		_ = c.txPool.Close()
	}
	sqlDB, err := c.db.DB()
	if err != nil {
		return fmt.Errorf("postgres: underlying sql.DB: %w", err)
	}
	return sqlDB.Close()
}

// NotDeleted is a GORM scope filtering out soft-deleted rows.
//
// Base models keep DeletedAt as a plain *time.Time to stay free of GORM types,
// which means GORM's automatic soft-delete filter does not apply; this scope is
// how a repository opts in.
//
//	db.Scopes(postgres.NotDeleted).Find(&out)
func NotDeleted(db *gorm.DB) *gorm.DB { return db.Where("deleted_at IS NULL") }

// newGormLogger routes GORM's SQL logging through slog at the configured level.
func newGormLogger(cfg Config) gormlogger.Interface {
	level := gormlogger.Warn
	switch cfg.LogLevel {
	case "silent", "none":
		level = gormlogger.Silent
	case "error":
		level = gormlogger.Error
	case "warn", "warning":
		level = gormlogger.Warn
	case "info", "debug", "trace":
		level = gormlogger.Info
	}
	slowThreshold := cfg.SlowThreshold
	if slowThreshold <= 0 {
		slowThreshold = 500 * time.Millisecond
	}
	return gormlogger.New(slogWriter{log: logger.Module("postgres.sql")}, gormlogger.Config{
		SlowThreshold:             slowThreshold,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// slogWriter adapts GORM's Printf-style writer onto slog, so SQL logs share the
// structured stream instead of going to stdout as loose text.
type slogWriter struct{ log *slog.Logger }

func (w slogWriter) Printf(format string, args ...any) {
	w.log.Info(fmt.Sprintf(format, args...))
}
