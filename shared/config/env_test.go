package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

// TestLoadParsesTheUsualForms covers the syntax a .env file actually contains.
func TestLoadParsesTheUsualForms(t *testing.T) {
	path := writeEnv(t, `
# a comment
PLAIN=value
SPACED = spaced value
export EXPORTED=exported
DQUOTED="double quoted"
SQUOTED='single quoted'
WITH_HASH=before # trailing comment
ESCAPES="line1\nline2\ttabbed"
NO_EXPAND_IN_SINGLE='literal\n'
EMPTY=
URL=postgres://user:pass@localhost:5432/db?sslmode=disable
`)

	for k := range map[string]struct{}{
		"PLAIN": {}, "SPACED": {}, "EXPORTED": {}, "DQUOTED": {}, "SQUOTED": {},
		"WITH_HASH": {}, "ESCAPES": {}, "NO_EXPAND_IN_SINGLE": {}, "EMPTY": {}, "URL": {},
	} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	if err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, tc := range []struct{ key, want string }{
		{"PLAIN", "value"},
		{"SPACED", "spaced value"},
		{"EXPORTED", "exported"},
		{"DQUOTED", "double quoted"},
		{"SQUOTED", "single quoted"},
		{"WITH_HASH", "before"},
		{"ESCAPES", "line1\nline2\ttabbed"},
		{"NO_EXPAND_IN_SINGLE", `literal\n`},
		{"URL", "postgres://user:pass@localhost:5432/db?sslmode=disable"},
	} {
		if got := os.Getenv(tc.key); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestLoadDoesNotOverrideTheRealEnvironment is the important precedence rule: in
// a container the environment is authoritative and a committed .env must not
// shadow it.
func TestLoadDoesNotOverrideTheRealEnvironment(t *testing.T) {
	t.Setenv("ALREADY_SET", "from-environment")
	path := writeEnv(t, "ALREADY_SET=from-dotenv\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := os.Getenv("ALREADY_SET"); got != "from-environment" {
		t.Fatalf("ALREADY_SET = %q, want the real environment to win", got)
	}
}

// TestLoadSkipsMissingFiles lets the same call work in dev and in production.
func TestLoadSkipsMissingFiles(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("Load on a missing file should be a no-op, got %v", err)
	}
}

// TestLoadRejectsMalformedLines surfaces a typo instead of ignoring it.
func TestLoadRejectsMalformedLines(t *testing.T) {
	if err := Load(writeEnv(t, "THIS_LINE_HAS_NO_EQUALS\n")); err == nil {
		t.Fatal("expected an error for a line without '='")
	}
	if err := Load(writeEnv(t, "=novalue\n")); err == nil {
		t.Fatal("expected an error for an empty key")
	}
}

func TestString(t *testing.T) {
	t.Setenv("S_SET", "value")
	t.Setenv("S_EMPTY", "")

	if got := String("S_SET", "fallback"); got != "value" {
		t.Errorf("String(set) = %q", got)
	}
	if got := String("S_EMPTY", "fallback"); got != "fallback" {
		t.Errorf("String(empty) = %q, want the default", got)
	}
	if got := String("S_ABSENT", "fallback"); got != "fallback" {
		t.Errorf("String(absent) = %q", got)
	}
}

func TestMustString(t *testing.T) {
	t.Setenv("M_SET", "value")

	if v, err := MustString("M_SET"); err != nil || v != "value" {
		t.Errorf("MustString(set) = %q, %v", v, err)
	}
	if _, err := MustString("M_ABSENT"); err == nil {
		t.Error("MustString(absent) should report an error so boot fails loudly")
	}
}

func TestInt(t *testing.T) {
	t.Setenv("I_OK", "42")
	t.Setenv("I_BAD", "not-a-number")
	t.Setenv("I_NEG", "-7")

	if got := Int("I_OK", 1); got != 42 {
		t.Errorf("Int(ok) = %d", got)
	}
	if got := Int("I_BAD", 1); got != 1 {
		t.Errorf("Int(bad) = %d, want the default", got)
	}
	if got := Int("I_NEG", 1); got != -7 {
		t.Errorf("Int(negative) = %d", got)
	}
}

func TestBool(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"true", true}, {"TRUE", true}, {"1", true}, {"yes", true}, {"on", true}, {"t", true},
		{"false", false}, {"FALSE", false}, {"0", false}, {"no", false}, {"off", false},
	} {
		t.Setenv("B_VAL", tc.raw)
		if got := Bool("B_VAL", !tc.want); got != tc.want {
			t.Errorf("Bool(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	t.Setenv("B_VAL", "maybe")
	if got := Bool("B_VAL", true); !got {
		t.Error("Bool(unrecognised) should fall back to the default")
	}
}

// TestDuration is the reason every timeout in the repo is a time.Duration: the
// unit travels with the value.
func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"15m", 15 * time.Minute},
		{"500ms", 500 * time.Millisecond},
		{"1h30m", 90 * time.Minute},
		{"30s", 30 * time.Second},
	} {
		t.Setenv("D_VAL", tc.raw)
		if got := Duration("D_VAL", time.Second); got != tc.want {
			t.Errorf("Duration(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	// A bare number has no unit and must not be guessed at.
	t.Setenv("D_VAL", "600")
	if got := Duration("D_VAL", time.Second); got != time.Second {
		t.Errorf("Duration(unitless) = %v, want the default", got)
	}
}

func TestStrings(t *testing.T) {
	t.Setenv("L_VAL", "a, b ,c,")
	got := Strings("L_VAL", nil)
	want := []string{"a", "b", "c"}

	if len(got) != len(want) {
		t.Fatalf("Strings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Strings = %v, want %v", got, want)
		}
	}

	t.Setenv("L_VAL", " , ")
	if got := Strings("L_VAL", []string{"fallback"}); len(got) != 1 || got[0] != "fallback" {
		t.Errorf("Strings(all empty) = %v, want the default", got)
	}
}

// TestLoadAppDefaults documents the shape main.go boots with.
func TestLoadAppDefaults(t *testing.T) {
	app := LoadApp()

	if app.ServerPort != "8080" {
		t.Errorf("ServerPort = %q, want 8080", app.ServerPort)
	}
	if app.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 15s", app.ShutdownTimeout)
	}
	if app.IsProduction() {
		t.Error("IsProduction should be false by default")
	}
}

func TestIsProduction(t *testing.T) {
	for _, env := range []string{"production", "prod"} {
		t.Setenv("APP_ENV", env)
		if !LoadApp().IsProduction() {
			t.Errorf("APP_ENV=%s should be production", env)
		}
	}
	t.Setenv("APP_ENV", "staging")
	if LoadApp().IsProduction() {
		t.Error("APP_ENV=staging should not be production")
	}
}
