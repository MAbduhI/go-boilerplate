package shutdown

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestCloseRunsInReverseOrder is the property the whole registry exists for: a
// resource may depend on anything registered before it, so it must be torn down
// first.
func TestCloseRunsInReverseOrder(t *testing.T) {
	r := New(quietLogger())
	var order []string

	for _, name := range []string{"postgres", "redis", "kafka"} {
		n := name
		r.Register(n, func(context.Context) error {
			order = append(order, n)
			return nil
		})
	}

	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []string{"kafka", "redis", "postgres"}
	if len(order) != len(want) {
		t.Fatalf("closed %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("close order = %v, want %v", order, want)
		}
	}
}

// TestCloseContinuesAfterFailure checks a failing hook does not strand the rest:
// on shutdown the goal is to release as much as possible.
func TestCloseContinuesAfterFailure(t *testing.T) {
	r := New(quietLogger())
	boom := errors.New("kafka flush failed")
	closed := map[string]bool{}

	r.Register("postgres", func(context.Context) error { closed["postgres"] = true; return nil })
	r.Register("kafka", func(context.Context) error { return boom })
	r.Register("redis", func(context.Context) error { closed["redis"] = true; return nil })

	err := r.Close(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Close error = %v, want it to wrap %v", err, boom)
	}
	for _, name := range []string{"postgres", "redis"} {
		if !closed[name] {
			t.Errorf("%s was not closed after an earlier hook failed", name)
		}
	}
}

// TestCloseJoinsEveryError checks all failures are reported, not just the first.
func TestCloseJoinsEveryError(t *testing.T) {
	r := New(quietLogger())
	first := errors.New("first")
	second := errors.New("second")

	r.Register("a", func(context.Context) error { return first })
	r.Register("b", func(context.Context) error { return second })

	err := r.Close(context.Background())
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("Close error = %v, want both %v and %v", err, first, second)
	}
}

// TestCloseIsIdempotent guards the shutdown path against running a hook twice
// when Close is reached from both the signal path and an error path.
func TestCloseIsIdempotent(t *testing.T) {
	r := New(quietLogger())
	calls := 0
	r.Register("postgres", func(context.Context) error { calls++; return nil })

	_ = r.Close(context.Background())
	_ = r.Close(context.Background())

	if calls != 1 {
		t.Fatalf("hook ran %d times, want 1", calls)
	}
}

// TestNamesReportsCloseOrder covers the value main.go logs before shutting down.
func TestNamesReportsCloseOrder(t *testing.T) {
	r := New(quietLogger())
	r.Register("postgres", func(context.Context) error { return nil })
	r.Register("redis", func(context.Context) error { return nil })

	names := r.Names()
	if len(names) != 2 || names[0] != "redis" || names[1] != "postgres" {
		t.Fatalf("Names() = %v, want [redis postgres]", names)
	}
}

// TestAdaptersAndNilHooks covers the driver-signature helpers, and confirms a nil
// hook is dropped rather than panicking during shutdown.
func TestAdaptersAndNilHooks(t *testing.T) {
	r := New(quietLogger())
	errCalled, plainCalled := false, false

	r.RegisterErrFunc("es", func() error { errCalled = true; return nil })
	r.RegisterFunc("worker", func() { plainCalled = true })
	r.Register("nil-hook", nil)
	r.RegisterErrFunc("nil-err", nil)
	r.RegisterFunc("nil-plain", nil)

	if got := len(r.Names()); got != 2 {
		t.Fatalf("registered %d hooks, want 2 (nil hooks dropped)", got)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !errCalled || !plainCalled {
		t.Error("adapter hooks did not run")
	}
}

// TestNilLoggerIsTolerated keeps a caller that has not initialised logging from
// panicking inside shutdown.
func TestNilLoggerIsTolerated(t *testing.T) {
	r := New(nil)
	r.Register("x", func(context.Context) error { return nil })
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
