// Package config provides a dependency-free .env loader plus typed env getters.
//
// Load parses KEY=VALUE files and seeds os.Environ, never overwriting a
// variable that the real environment already defines. That ordering matters:
// in containers the environment is authoritative and a stray committed .env
// must not shadow it.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Find returns the path of the first name found in the working directory or any
// parent, and whether it was found. The search stops at the filesystem root.
//
// This exists because a workspace binary does not run from the repository root:
// `go run ./cmd/api` inside app/ has app/ as its working directory, so a .env at
// the root would otherwise be invisible.
func Find(name string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// LoadNearest loads the first of names found in the working directory or any
// parent. A name that is nowhere to be found is skipped, so the same call works
// in development and in a container that has no env file at all.
func LoadNearest(names ...string) error {
	if len(names) == 0 {
		names = []string{".env"}
	}
	for _, name := range names {
		path, found := Find(name)
		if !found {
			continue
		}
		if err := Load(path); err != nil {
			return err
		}
	}
	return nil
}

// Load reads each path in order and sets any variable not already present in
// the process environment. Missing files are skipped silently so the same
// call works in dev (with .env) and in production (without).
func Load(paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	for _, path := range paths {
		// The caller chooses which env file to load; that is the whole point.
		f, err := os.Open(path) //nolint:gosec // path is operator-supplied config
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("config: open %s: %w", path, err)
		}
		err = parseInto(f, path)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func parseInto(f *os.File, path string) error {
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		key, value, ok, err := parseLine(sc.Text())
		if err != nil {
			return fmt.Errorf("config: %s:%d: %w", path, line, err)
		}
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("config: %s:%d: setenv %s: %w", path, line, key, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	return nil
}

// parseLine returns ok=false for blanks and comments.
func parseLine(raw string) (key, value string, ok bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false, nil
	}
	s = strings.TrimPrefix(s, "export ")

	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		return "", "", false, fmt.Errorf("missing '=' in %q", raw)
	}
	key = strings.TrimSpace(s[:eq])
	if key == "" {
		return "", "", false, fmt.Errorf("empty key in %q", raw)
	}
	return key, unquote(strings.TrimSpace(s[eq+1:])), true, nil
}

// unquote strips matching quotes. Only double quotes expand escapes, matching
// POSIX shell behaviour; bare values are truncated at an unquoted '#'.
func unquote(v string) string {
	if len(v) >= 2 {
		if q := v[0]; (q == '"' || q == '\'') && v[len(v)-1] == q {
			inner := v[1 : len(v)-1]
			if q == '\'' {
				return inner
			}
			return expandEscapes(inner)
		}
	}
	if i := strings.IndexByte(v, '#'); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

func expandEscapes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// String returns the value of key, or def when unset or empty.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// MustString returns the value of key or an error when unset, for credentials
// that have no safe default.
func MustString(key string) (string, error) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("config: required env %s is not set", key)
}

// Int returns key parsed as an int, or def when unset or unparseable.
func Int(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// Bool accepts the usual 1/t/true/yes/on spellings, case-insensitively.
func Bool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	case "0", "f", "false", "n", "no", "off":
		return false
	}
	return def
}

// Duration parses a Go duration string such as "15m" or "1h30m".
//
// Every TTL and timeout in this repo is a time.Duration precisely so the unit
// lives in the value; a bare integer leaves seconds-vs-milliseconds ambiguous.
func Duration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

// Strings splits a comma-separated value, trimming and dropping empties.
func Strings(key string, def []string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}
