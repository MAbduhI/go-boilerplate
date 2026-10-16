package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

var (
	// ErrTxPoolExhausted is returned when the transaction pool cannot accept more
	// concurrent transactions within the acquire timeout.
	ErrTxPoolExhausted = errors.New("postgres: transaction pool exhausted, acquire timeout exceeded")

	// ErrTxPoolClosed is returned when attempting to run a transaction against a closed transaction pool.
	ErrTxPoolClosed = errors.New("postgres: transaction pool is closed")
)

// TxPoolConfig configures concurrency bounding and retry policy for transactions.
type TxPoolConfig struct {
	// Capacity is the maximum number of concurrent transactions allowed.
	// Defaults to 80% of DB MaxOpenConns if <= 0.
	Capacity int

	// AcquireTimeout is the maximum duration to wait for an available transaction slot.
	// Defaults to 5s if <= 0.
	AcquireTimeout time.Duration

	// MaxRetries specifies how many times to retry on transient PostgreSQL errors
	// (such as serialization_failure 40001 or deadlock_detected 40P01).
	// Defaults to 3. Set to 0 to disable retries.
	MaxRetries int

	// RetryBackoff is the base duration to wait between retries, backed by exponential jitter.
	// Defaults to 50ms if <= 0.
	RetryBackoff time.Duration
}

// TxPoolStats provides live observability counters for the transaction pool.
type TxPoolStats struct {
	Active     int   `json:"active"`      // Number of currently executing transactions
	Capacity   int   `json:"capacity"`    // Maximum concurrent transaction capacity
	Waiting    int64 `json:"waiting"`     // Callers currently waiting for a transaction slot
	Committed  int64 `json:"committed"`   // Total successfully committed transactions
	RolledBack int64 `json:"rolled_back"` // Total rolled back transactions
	Retried    int64 `json:"retried"`     // Total retry attempts due to transient errors
	Timeouts   int64 `json:"timeouts"`    // Total requests that timed out waiting for a slot
}

// TxOption configures a specific transaction run.
type TxOption func(*txRunConfig)

type txRunConfig struct {
	sqlOptions *sql.TxOptions
	timeout    time.Duration
	maxRetries *int
}

// WithIsolation sets the transaction isolation level (e.g. sql.LevelSerializable).
func WithIsolation(level sql.IsolationLevel) TxOption {
	return func(c *txRunConfig) {
		if c.sqlOptions == nil {
			c.sqlOptions = &sql.TxOptions{}
		}
		c.sqlOptions.Isolation = level
	}
}

// WithReadOnly marks the transaction as read-only.
func WithReadOnly(readOnly bool) TxOption {
	return func(c *txRunConfig) {
		if c.sqlOptions == nil {
			c.sqlOptions = &sql.TxOptions{}
		}
		c.sqlOptions.ReadOnly = readOnly
	}
}

// WithTxOptions passes custom sql.TxOptions directly.
func WithTxOptions(opts *sql.TxOptions) TxOption {
	return func(c *txRunConfig) {
		c.sqlOptions = opts
	}
}

// WithTxTimeout sets an explicit per-transaction timeout.
func WithTxTimeout(timeout time.Duration) TxOption {
	return func(c *txRunConfig) {
		c.timeout = timeout
	}
}

// WithTxMaxRetries overrides the pool default retry count for this transaction.
func WithTxMaxRetries(retries int) TxOption {
	return func(c *txRunConfig) {
		c.maxRetries = &retries
	}
}

// WithoutRetry explicitly disables automatic retries for this transaction.
func WithoutRetry() TxOption {
	return WithTxMaxRetries(0)
}

// TxPool regulates concurrent database transactions using a bounded semaphore pool,
// preventing connection exhaustion and starving simple read queries while providing
// automatic retry resilience for transient deadlock and serialization failures.
type TxPool struct {
	client *Client
	cfg    TxPoolConfig
	sem    chan struct{}
	closed atomic.Bool

	// Telemetry counters
	waiting    atomic.Int64
	committed  atomic.Int64
	rolledBack atomic.Int64
	retried    atomic.Int64
	timeouts   atomic.Int64
}

// NewTxPool initializes a new transaction pool bound to the given client.
func NewTxPool(client *Client, cfg TxPoolConfig) *TxPool {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 20
	}
	if cfg.AcquireTimeout <= 0 {
		cfg.AcquireTimeout = 5 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 50 * time.Millisecond
	}

	return &TxPool{
		client: client,
		cfg:    cfg,
		sem:    make(chan struct{}, cfg.Capacity),
	}
}

// Execute runs fn inside a transaction managed by the transaction pool.
//
// If the context is already inside a transaction (ambient tx), it runs inside
// a nested savepoint without acquiring a new semaphore permit.
func (p *TxPool) Execute(ctx context.Context, fn func(ctx context.Context) error, opts ...TxOption) error {
	if p.closed.Load() {
		return ErrTxPoolClosed
	}

	// 1. Re-entrancy & Ambient Transaction check:
	// When already inside a transaction, reuse it via savepoints instead of
	// consuming another pool slot. This prevents self-deadlock in composed usecases.
	if tx, ok := TxFromContext(ctx); ok {
		return tx.Transaction(func(nested *gorm.DB) error {
			return fn(contextWithTx(ctx, nested))
		})
	}

	runCfg := txRunConfig{}
	for _, opt := range opts {
		opt(&runCfg)
	}

	// Apply optional per-transaction timeout
	if runCfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runCfg.timeout)
		defer cancel()
	}

	maxRetries := p.cfg.MaxRetries
	if runCfg.maxRetries != nil {
		maxRetries = *runCfg.maxRetries
	}

	// 2. Acquire slot from semaphore bounded by AcquireTimeout and ctx
	p.waiting.Add(1)
	acquireTimer := time.NewTimer(p.cfg.AcquireTimeout)
	defer acquireTimer.Stop()

	select {
	case p.sem <- struct{}{}:
		p.waiting.Add(-1)
	case <-ctx.Done():
		p.waiting.Add(-1)
		p.timeouts.Add(1)
		return fmt.Errorf("%w: context cancelled while acquiring transaction slot: %w", ErrTxPoolExhausted, ctx.Err())
	case <-acquireTimer.C:
		p.waiting.Add(-1)
		p.timeouts.Add(1)
		return ErrTxPoolExhausted
	}

	defer func() {
		<-p.sem
	}()

	// 3. Execution loop with transient error retry
	var attempt int
	for {
		attempt++

		err := p.runTransaction(ctx, runCfg.sqlOptions, fn)
		if err == nil {
			p.committed.Add(1)
			return nil
		}

		p.rolledBack.Add(1)

		// Check if error is transient and retries remain
		if attempt <= maxRetries && isTransientTxError(err) {
			p.retried.Add(1)
			backoff := p.calculateBackoff(attempt)
			if p.client != nil && p.client.log != nil {
				p.client.log.Warn("postgres: retrying transaction after transient error",
					slog.Int("attempt", attempt),
					slog.Int("max_retries", maxRetries),
					slog.Duration("backoff", backoff),
					slog.Any("error", err))
			}

			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return fmt.Errorf("postgres: context cancelled during transaction retry backoff: %w", ctx.Err())
			}
		}

		return err
	}
}

// runTransaction initiates a GORM transaction with optional SQL transaction options.
func (p *TxPool) runTransaction(ctx context.Context, sqlOpts *sql.TxOptions, fn func(ctx context.Context) error) error {
	db := p.client.db.WithContext(ctx)
	if sqlOpts == nil {
		return db.Transaction(func(tx *gorm.DB) error {
			return fn(contextWithTx(ctx, tx))
		})
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return fn(contextWithTx(ctx, tx))
	}, sqlOpts)
}

// calculateBackoff computes exponential backoff with randomized jitter.
func (p *TxPool) calculateBackoff(attempt int) time.Duration {
	base := p.cfg.RetryBackoff
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	d := base * time.Duration(1<<shift)
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	// Add up to 25% random jitter
	jitter := time.Duration(rand.Int63n(int64(d/4 + 1)))
	return d + jitter
}

// isTransientTxError checks for PostgreSQL transient errors (deadlocks, serialization failures).
func isTransientTxError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "40001") || // serialization_failure
		strings.Contains(s, "40p01") || // deadlock_detected
		strings.Contains(s, "deadlock detected") ||
		strings.Contains(s, "could not serialize access") ||
		strings.Contains(s, "concurrent update") ||
		strings.Contains(s, "lock not available")
}

// Stats returns live telemetry metrics for the pool.
func (p *TxPool) Stats() TxPoolStats {
	return TxPoolStats{
		Active:     len(p.sem),
		Capacity:   cap(p.sem),
		Waiting:    p.waiting.Load(),
		Committed:  p.committed.Load(),
		RolledBack: p.rolledBack.Load(),
		Retried:    p.retried.Load(),
		Timeouts:   p.timeouts.Load(),
	}
}

// Capacity reports the pool capacity.
func (p *TxPool) Capacity() int {
	return cap(p.sem)
}

// Active reports the number of transactions currently in flight.
func (p *TxPool) Active() int {
	return len(p.sem)
}

// Close gracefully closes the transaction pool.
func (p *TxPool) Close() error {
	p.closed.Store(true)
	return nil
}
