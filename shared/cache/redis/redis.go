// Package redis wraps go-redis with a configurable default TTL.
//
// The RESP protocol is not worth reimplementing, so the driver is fetched; this
// wrapper exists to centralise configuration and to make expiry explicit.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// ErrCacheMiss reports a key that is absent or expired. Compare with errors.Is
// rather than against the driver's redis.Nil, so callers do not import go-redis.
var ErrCacheMiss = errors.New("redis: cache miss")

// Config describes the connection, the pool and the default expiry.
type Config struct {
	Addr     string
	Username string
	Password string
	DB       int

	// DefaultTTL applies to every Set that passes a zero ttlOverride.
	//
	// A default expiry is deliberate: a cache whose entries never expire is a
	// memory leak that only shows up in production.
	DefaultTTL time.Duration

	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxRetries   int
}

// LoadConfig reads Config from REDIS_* environment variables.
func LoadConfig() Config {
	return Config{
		Addr:     config.String("REDIS_HOST", "localhost:6379"),
		Username: config.String("REDIS_USERNAME", ""),
		Password: config.String("REDIS_PASSWORD", ""),
		DB:       config.Int("REDIS_DB", 0),

		DefaultTTL: config.Duration("REDIS_DEFAULT_TTL", 10*time.Minute),

		PoolSize:     config.Int("REDIS_POOL_SIZE", 10),
		MinIdleConns: config.Int("REDIS_MIN_IDLE_CONNS", 2),
		DialTimeout:  config.Duration("REDIS_DIAL_TIMEOUT", 5*time.Second),
		ReadTimeout:  config.Duration("REDIS_READ_TIMEOUT", 3*time.Second),
		WriteTimeout: config.Duration("REDIS_WRITE_TIMEOUT", 3*time.Second),
		MaxRetries:   config.Int("REDIS_MAX_RETRIES", 3),
	}
}

// Client is the Redis wrapper.
type Client struct {
	rdb *goredis.Client
	cfg Config
	log *slog.Logger
}

// New connects and verifies the connection.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = 10 * time.Minute
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		MaxRetries:   cfg.MaxRetries,
	})

	c := &Client{rdb: rdb, cfg: cfg, log: logger.Module("redis")}

	if err := c.Ping(ctx); err != nil {
		_ = rdb.Close()
		return nil, err
	}

	c.log.Info("connected",
		slog.String("addr", cfg.Addr),
		slog.Int("db", cfg.DB),
		slog.Duration("default_ttl", cfg.DefaultTTL),
		slog.Int("pool_size", cfg.PoolSize))

	return c, nil
}

// Redis exposes the underlying client for commands this wrapper does not cover.
func (c *Client) Redis() *goredis.Client { return c.rdb }

// DefaultTTL reports the configured default expiry.
func (c *Client) DefaultTTL() time.Duration { return c.cfg.DefaultTTL }

// Config reports the resolved configuration.
func (c *Client) Config() Config { return c.cfg }

// resolveTTL applies the override, falling back to the configured default.
//
// A negative override means "no expiry", which has to be spelled explicitly
// because zero already means "use the default".
func (c *Client) resolveTTL(override time.Duration) time.Duration {
	switch {
	case override > 0:
		return override
	case override < 0:
		return 0 // keyless expiry: persist until evicted or deleted
	default:
		return c.cfg.DefaultTTL
	}
}

// Set stores value under key.
//
// Pass 0 for ttlOverride to use DefaultTTL, a positive duration for a per-key
// expiry, or NoExpiry to persist the key.
func (c *Client) Set(ctx context.Context, key string, value any, ttlOverride time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, c.resolveTTL(ttlOverride)).Err(); err != nil {
		return fmt.Errorf("redis: set %s: %w", key, err)
	}
	return nil
}

// NoExpiry is the ttlOverride that persists a key past the default TTL.
const NoExpiry = -1 * time.Nanosecond

// Get returns the raw string at key, or ErrCacheMiss.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	v, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return "", ErrCacheMiss
		}
		return "", fmt.Errorf("redis: get %s: %w", key, err)
	}
	return v, nil
}

// SetJSON marshals value as JSON and stores it.
func (c *Client) SetJSON(ctx context.Context, key string, value any, ttlOverride time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis: marshal %s: %w", key, err)
	}
	return c.Set(ctx, key, raw, ttlOverride)
}

// GetJSON reads key and unmarshals it into dest, returning ErrCacheMiss when
// the key is absent.
func (c *Client) GetJSON(ctx context.Context, key string, dest any) error {
	raw, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return fmt.Errorf("redis: unmarshal %s: %w", key, err)
	}
	return nil
}

// Delete removes keys, reporting how many existed.
func (c *Client) Delete(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	n, err := c.rdb.Del(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: delete: %w", err)
	}
	return n, nil
}

// Exists reports how many of keys are present.
func (c *Client) Exists(ctx context.Context, keys ...string) (int64, error) {
	n, err := c.rdb.Exists(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: exists: %w", err)
	}
	return n, nil
}

// TTL reports the remaining lifetime of key.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	d, err := c.rdb.TTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: ttl %s: %w", key, err)
	}
	return d, nil
}

// Remember returns the cached value for key, computing and caching it on a miss.
//
// This is the pattern worth having in one place: without it, every caller
// reimplements get-miss-compute-set and they drift on error handling — in
// particular on whether a cache write failure should fail the request. Here it
// does not: the value is already computed, so a failed write is logged and the
// value returned.
func (c *Client) Remember(ctx context.Context, key string, dest any, ttlOverride time.Duration, compute func(context.Context) (any, error)) error {
	err := c.GetJSON(ctx, key, dest)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrCacheMiss) {
		// A real Redis failure must not take the request down; fall through to
		// the origin and log it.
		c.log.Warn("cache read failed, falling back to origin",
			slog.String("key", key), slog.String("error", err.Error()))
	}

	value, err := compute(ctx)
	if err != nil {
		return err
	}

	if err := c.SetJSON(ctx, key, value, ttlOverride); err != nil {
		c.log.Warn("cache write failed",
			slog.String("key", key), slog.String("error", err.Error()))
	}

	// Round-trip through JSON so dest is populated exactly as a cache hit would
	// leave it, rather than by a type assertion that could differ.
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis: marshal computed value for %s: %w", key, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("redis: unmarshal computed value for %s: %w", key, err)
	}
	return nil
}

// Ping verifies connectivity.
func (c *Client) Ping(ctx context.Context) error {
	if c.cfg.DialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.DialTimeout)
		defer cancel()
	}
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping %s: %w", c.cfg.Addr, err)
	}
	return nil
}

// Close releases the pool. Registered with the shutdown registry.
func (c *Client) Close() error { return c.rdb.Close() }
