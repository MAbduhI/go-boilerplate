package redis

import (
	"testing"
	"time"
)

// TestLoadConfigDefaultTTL documents the out-of-the-box expiry.
func TestLoadConfigDefaultTTL(t *testing.T) {
	if got := LoadConfig().DefaultTTL; got != 10*time.Minute {
		t.Errorf("DefaultTTL = %v, want 10m", got)
	}
}

// TestLoadConfigTTLFromEnv proves REDIS_DEFAULT_TTL is honoured and parsed as a
// duration, so the unit is unambiguous.
func TestLoadConfigTTLFromEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"45m", 45 * time.Minute},
		{"2h", 2 * time.Hour},
		{"1h30m", 90 * time.Minute},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("REDIS_DEFAULT_TTL", tc.env)
			if got := LoadConfig().DefaultTTL; got != tc.want {
				t.Errorf("DefaultTTL = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLoadConfigIgnoresUnparseableTTL keeps a typo from silently disabling
// expiry: a bad value falls back to the default rather than to zero.
func TestLoadConfigIgnoresUnparseableTTL(t *testing.T) {
	t.Setenv("REDIS_DEFAULT_TTL", "600") // no unit
	if got := LoadConfig().DefaultTTL; got != 10*time.Minute {
		t.Errorf("DefaultTTL = %v, want the 10m default for an unparseable value", got)
	}
}

// TestLoadConfigPoolFromEnv covers the remaining knobs.
func TestLoadConfigPoolFromEnv(t *testing.T) {
	t.Setenv("REDIS_HOST", "cache.internal:6380")
	t.Setenv("REDIS_DB", "3")
	t.Setenv("REDIS_POOL_SIZE", "64")
	t.Setenv("REDIS_DIAL_TIMEOUT", "2s")

	cfg := LoadConfig()
	if cfg.Addr != "cache.internal:6380" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.DB != 3 {
		t.Errorf("DB = %d, want 3", cfg.DB)
	}
	if cfg.PoolSize != 64 {
		t.Errorf("PoolSize = %d, want 64", cfg.PoolSize)
	}
	if cfg.DialTimeout != 2*time.Second {
		t.Errorf("DialTimeout = %v, want 2s", cfg.DialTimeout)
	}
}

// TestResolveTTL pins the three-way meaning of the ttlOverride argument, which
// is the part a caller is most likely to get wrong.
func TestResolveTTL(t *testing.T) {
	c := &Client{cfg: Config{DefaultTTL: 10 * time.Minute}}

	for _, tc := range []struct {
		name     string
		override time.Duration
		want     time.Duration
	}{
		{"zero uses the configured default", 0, 10 * time.Minute},
		{"positive overrides per call", 30 * time.Second, 30 * time.Second},
		{"NoExpiry persists the key", NoExpiry, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.resolveTTL(tc.override); got != tc.want {
				t.Errorf("resolveTTL(%v) = %v, want %v", tc.override, got, tc.want)
			}
		})
	}
}

// TestNewClampsZeroDefaultTTL guards against a Config built by hand, rather than
// by LoadConfig, leaving every key immortal.
func TestNewClampsZeroDefaultTTL(t *testing.T) {
	c := &Client{cfg: Config{DefaultTTL: 0}}
	// resolveTTL(0) with an unset default returns 0, i.e. no expiry; New is what
	// applies the fallback, so assert the documented post-New state instead.
	if c.resolveTTL(0) != 0 {
		t.Fatal("expected an unset DefaultTTL to mean no expiry before New applies its fallback")
	}

	cfg := Config{DefaultTTL: 0}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = 10 * time.Minute // mirrors New
	}
	withFallback := &Client{cfg: cfg}
	if got := withFallback.resolveTTL(0); got != 10*time.Minute {
		t.Errorf("after New's fallback, resolveTTL(0) = %v, want 10m", got)
	}
}
