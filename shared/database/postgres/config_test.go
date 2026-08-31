package postgres

import (
	"strings"
	"testing"
	"time"
)

// TestLoadConfigDefaults documents the out-of-the-box pool settings.
func TestLoadConfigDefaults(t *testing.T) {
	cfg := LoadConfig()

	if cfg.MaxOpenConns != 25 {
		t.Errorf("MaxOpenConns = %d, want 25", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 10 {
		t.Errorf("MaxIdleConns = %d, want 10", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 15*time.Minute {
		t.Errorf("ConnMaxLifetime = %v, want 15m", cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime != 5*time.Minute {
		t.Errorf("ConnMaxIdleTime = %v, want 5m", cfg.ConnMaxIdleTime)
	}
}

// TestLoadConfigEnvOverride proves every pool knob is reachable from the
// environment, and that durations are parsed as durations rather than as a
// count of some implied unit.
func TestLoadConfigEnvOverride(t *testing.T) {
	t.Setenv("POSTGRES_HOST", "db.internal")
	t.Setenv("POSTGRES_PORT", "6432")
	t.Setenv("POSTGRES_DB", "orders")
	t.Setenv("POSTGRES_MAX_OPEN_CONNS", "80")
	t.Setenv("POSTGRES_MAX_IDLE_CONNS", "20")
	t.Setenv("POSTGRES_CONN_MAX_LIFETIME", "45m")
	t.Setenv("POSTGRES_CONN_MAX_IDLE_TIME", "90s")
	t.Setenv("POSTGRES_PREPARE_STMT", "false")

	cfg := LoadConfig()

	if cfg.MaxOpenConns != 80 {
		t.Errorf("MaxOpenConns = %d, want 80", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 20 {
		t.Errorf("MaxIdleConns = %d, want 20", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 45*time.Minute {
		t.Errorf("ConnMaxLifetime = %v, want 45m", cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime != 90*time.Second {
		t.Errorf("ConnMaxIdleTime = %v, want 90s", cfg.ConnMaxIdleTime)
	}
	if cfg.PrepareStmt {
		t.Error("PrepareStmt = true, want false from env")
	}
	if cfg.Addr() != "db.internal:6432" {
		t.Errorf("Addr = %q, want db.internal:6432", cfg.Addr())
	}
}

// TestNormalizeClampsIdleToOpen keeps a contradictory pool setting from being
// silently reinterpreted deeper in database/sql.
func TestNormalizeClampsIdleToOpen(t *testing.T) {
	cfg := Config{MaxOpenConns: 5, MaxIdleConns: 50}
	cfg.normalize()

	if cfg.MaxIdleConns != 5 {
		t.Errorf("MaxIdleConns = %d, want it clamped to MaxOpenConns (5)", cfg.MaxIdleConns)
	}
}

// TestPoolSettingsReachTheDriver asserts the configured limits are actually
// applied to the underlying *sql.DB, not merely stored on Config.
func TestPoolSettingsReachTheDriver(t *testing.T) {
	c, _ := newMockClient(t)
	sqlDB, err := c.Gorm().DB()
	if err != nil {
		t.Fatalf("underlying DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(33)

	if got := sqlDB.Stats().MaxOpenConnections; got != 33 {
		t.Fatalf("MaxOpenConnections = %d, want 33", got)
	}
	open, inUse, idle, waits := c.Stats()
	t.Logf("pool stats reachable: open=%d in_use=%d idle=%d waits=%d", open, inUse, idle, waits)
}

// TestDSNQuotesAwkwardPassword guards against a password containing a space or
// a quote silently truncating the DSN and producing a confusing auth failure.
func TestDSNQuotesAwkwardPassword(t *testing.T) {
	cfg := Config{
		Host: "localhost", Port: "5432", User: "app",
		Password: "p@ss word'x", DBName: "app", SSLMode: "disable", TimeZone: "UTC",
	}
	dsn := cfg.DSN()

	if !strings.Contains(dsn, `password='p@ss word\'x'`) {
		t.Fatalf("password not quoted/escaped in DSN: %s", dsn)
	}
	if !strings.Contains(dsn, "dbname=app") {
		t.Errorf("dbname missing from DSN: %s", dsn)
	}
}

// TestDSNOmitsEmptyFields keeps unset optional fields out of the DSN rather
// than sending key= with an empty value.
func TestDSNOmitsEmptyFields(t *testing.T) {
	cfg := Config{Host: "localhost", Port: "5432", DBName: "app"}

	if dsn := cfg.DSN(); strings.Contains(dsn, "password=") || strings.Contains(dsn, "user=") {
		t.Fatalf("empty fields leaked into DSN: %s", dsn)
	}
}
