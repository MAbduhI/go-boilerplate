// Package logger wraps log/slog to give the repo one structured logger with no
// extra dependency.
//
// This replaces the reference project's string-level logger, which formatted
// with log.Printf and therefore produced unparseable output. Levels are
// preserved as names ("trace".."panic") so existing LOG_LEVEL values keep
// working, but every record is now structured key/value data.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// LevelTrace extends slog's built-in levels; slog.LevelDebug is -4.
const LevelTrace = slog.Level(-8)

var (
	current atomic.Pointer[slog.Logger]
	level   = new(slog.LevelVar)
)

func init() {
	level.Set(slog.LevelInfo)
	current.Store(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

// Config selects verbosity and encoding.
type Config struct {
	// Level is one of trace, debug, info, warn/warning, error.
	Level string
	// Format is "json" or "text".
	Format string
	// Output defaults to os.Stdout when nil.
	Output io.Writer
	// Source adds file:line to every record; useful in dev, costly in prod.
	Source bool
}

// Init installs the process logger and also routes the stdlib slog default to
// it, so third-party code calling slog.Info lands in the same stream.
func Init(cfg Config) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}
	level.Set(ParseLevel(cfg.Level))

	opts := &slog.HandlerOptions{
		Level:       level,
		AddSource:   cfg.Source,
		ReplaceAttr: replaceAttr,
	}

	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(out, opts)
	} else {
		h = slog.NewJSONHandler(out, opts)
	}

	l := slog.New(h)
	current.Store(l)
	slog.SetDefault(l)
	return l
}

// replaceAttr renders the custom trace level by name instead of "DEBUG-4".
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.LevelKey {
		if lv, ok := a.Value.Any().(slog.Level); ok && lv == LevelTrace {
			a.Value = slog.StringValue("TRACE")
		}
	}
	return a
}

// ParseLevel maps a level name to a slog.Level, defaulting to info.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "trace":
		return LevelTrace
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "fatal", "panic":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetLevel changes verbosity at runtime; all existing loggers observe it
// because they share one LevelVar.
func SetLevel(name string) { level.Set(ParseLevel(name)) }

// L returns the process logger. It is never nil.
func L() *slog.Logger { return current.Load() }

// With returns a child logger carrying attrs.
func With(args ...any) *slog.Logger { return L().With(args...) }

// Module returns a logger tagged with a component name, the structured
// equivalent of the reference logger's "loc" argument.
func Module(name string) *slog.Logger { return L().With(slog.String("module", name)) }

type ctxKey struct{}

// IntoContext stores l for retrieval by FromContext, letting a request-scoped
// logger (with request_id and friends) flow down through usecases.
func IntoContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the logger stored by IntoContext, or the process logger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return L()
}

func Trace(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).Log(ctx, LevelTrace, msg, args...)
}
func Debug(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).DebugContext(ctx, msg, args...)
}
func Info(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).InfoContext(ctx, msg, args...)
}
func Warn(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).WarnContext(ctx, msg, args...)
}
func Error(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).ErrorContext(ctx, msg, args...)
}

// Fatal logs at error level and exits. Reserved for boot failures; never call
// it from request handling.
func Fatal(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).ErrorContext(ctx, msg, args...)
	os.Exit(1)
}
