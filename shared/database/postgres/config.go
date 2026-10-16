// Package postgres wraps GORM with pooling, a correct transaction-in-context
// helper, transaction pool concurrency limits, and a shutdown-friendly Close.
package postgres

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/MAbduhI/go-boilerplate/shared/config"
)

// Config describes the connection, connection pool, and transaction pool limits.
//
// Every duration is a time.Duration rather than a bare "minutes" int, so the
// unit is carried by the value; the reference project's ConnMaxLifetimeMin had
// to be multiplied by time.Minute at the call site.
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
	TimeZone string

	// Pool limits, all mapped onto the underlying *sql.DB.
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	// Transaction Pool (TxPool) limits.
	TxPoolSize       int           // Max concurrent active transactions (defaults to 80% of MaxOpenConns)
	TxAcquireTimeout time.Duration // Max time to wait for a transaction slot before failing (default: 5s)
	TxMaxRetries     int           // Max retry attempts on transient serialization/deadlock errors (default: 3)
	TxRetryBackoff   time.Duration // Base backoff for transient error retries (default: 50ms)

	// ConnectTimeout bounds the initial handshake.
	ConnectTimeout time.Duration

	// LogLevel is one of silent, error, warn, info.
	LogLevel      string
	SlowThreshold time.Duration

	// PrepareStmt caches prepared statements. Leave it off when connecting
	// through a transaction-mode pooler such as PgBouncer, which does not keep
	// statements across checkouts.
	PrepareStmt bool

	// SkipDefaultTransaction drops GORM's implicit per-write transaction.
	SkipDefaultTransaction bool
}

// LoadConfig reads Config from POSTGRES_* environment variables.
func LoadConfig() Config {
	return Config{
		Host:     config.String("POSTGRES_HOST", "localhost"),
		Port:     config.String("POSTGRES_PORT", "5432"),
		User:     config.String("POSTGRES_USER", "postgres"),
		Password: config.String("POSTGRES_PASSWORD", "postgres"),
		DBName:   config.String("POSTGRES_DB", "app"),
		SSLMode:  config.String("POSTGRES_SSLMODE", "disable"),
		TimeZone: config.String("POSTGRES_TIMEZONE", "UTC"),

		MaxOpenConns:    config.Int("POSTGRES_MAX_OPEN_CONNS", 25),
		MaxIdleConns:    config.Int("POSTGRES_MAX_IDLE_CONNS", 10),
		ConnMaxLifetime: config.Duration("POSTGRES_CONN_MAX_LIFETIME", 15*time.Minute),
		ConnMaxIdleTime: config.Duration("POSTGRES_CONN_MAX_IDLE_TIME", 5*time.Minute),
		ConnectTimeout:  config.Duration("POSTGRES_CONNECT_TIMEOUT", 10*time.Second),

		TxPoolSize:       config.Int("POSTGRES_TX_POOL_SIZE", 0),
		TxAcquireTimeout: config.Duration("POSTGRES_TX_ACQUIRE_TIMEOUT", 5*time.Second),
		TxMaxRetries:     config.Int("POSTGRES_TX_MAX_RETRIES", 3),
		TxRetryBackoff:   config.Duration("POSTGRES_TX_RETRY_BACKOFF", 50*time.Millisecond),

		LogLevel:      config.String("POSTGRES_LOG_LEVEL", "warn"),
		SlowThreshold: config.Duration("POSTGRES_SLOW_THRESHOLD", 500*time.Millisecond),

		PrepareStmt:            config.Bool("POSTGRES_PREPARE_STMT", true),
		SkipDefaultTransaction: config.Bool("POSTGRES_SKIP_DEFAULT_TRANSACTION", false),
	}
}

// DSN renders the libpq key/value connection string.
func (c Config) DSN() string {
	pairs := [][2]string{
		{"host", c.Host},
		{"port", c.Port},
		{"user", c.User},
		{"password", c.Password},
		{"dbname", c.DBName},
		{"sslmode", c.SSLMode},
		{"TimeZone", c.TimeZone},
	}
	if c.ConnectTimeout > 0 {
		pairs = append(pairs, [2]string{"connect_timeout", fmt.Sprintf("%d", int(c.ConnectTimeout.Seconds()))})
	}

	var b strings.Builder
	for _, kv := range pairs {
		if kv[1] == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s=%s", kv[0], quoteDSNValue(kv[1]))
	}
	return b.String()
}

// quoteDSNValue escapes a value that contains whitespace or quotes, which a
// generated password routinely does.
func quoteDSNValue(v string) string {
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(v) + "'"
}

// Addr returns host:port, for log lines that must not include credentials.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

// normalize applies safe defaults and reconciles contradictory pool settings.
func (c *Config) normalize() {
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns < 0 {
		c.MaxIdleConns = 0
	}
	// More idle than open is not representable in the pool: database/sql would
	// silently reduce the idle limit, so make the clamp visible here.
	if c.MaxIdleConns > c.MaxOpenConns {
		c.MaxIdleConns = c.MaxOpenConns
	}
	if c.TimeZone == "" {
		c.TimeZone = "UTC"
	}

	// Transaction pool defaults
	if c.TxPoolSize <= 0 {
		c.TxPoolSize = int(float64(c.MaxOpenConns) * 0.8)
		if c.TxPoolSize < 1 {
			c.TxPoolSize = 1
		}
	}
	if c.TxAcquireTimeout <= 0 {
		c.TxAcquireTimeout = 5 * time.Second
	}
	if c.TxMaxRetries < 0 {
		c.TxMaxRetries = 0
	}
	if c.TxRetryBackoff <= 0 {
		c.TxRetryBackoff = 50 * time.Millisecond
	}
}
