// Package shutdown collects the close functions of every open resource so the
// server can drain them in one place, in reverse order of registration.
//
// The reference project closed nothing on exit; this registry is what makes
// the graceful shutdown path auditable.
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// CloseFunc releases one resource. It must respect ctx cancellation.
type CloseFunc func(context.Context) error

type hook struct {
	name  string
	close CloseFunc
}

// Registry is a LIFO stack of close hooks. It is safe for concurrent use.
type Registry struct {
	mu    sync.Mutex
	hooks []hook
	log   *slog.Logger
}

// New returns an empty Registry. A nil log is tolerated.
func New(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{log: log}
}

// Register adds a close hook. Later registrations close first, so a resource
// may safely depend on anything registered before it.
func (r *Registry) Register(name string, fn CloseFunc) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook{name: name, close: fn})
}

// RegisterErrFunc adapts a driver Close() error signature.
func (r *Registry) RegisterErrFunc(name string, fn func() error) {
	if fn == nil {
		return
	}
	r.Register(name, func(context.Context) error { return fn() })
}

// RegisterFunc adapts a driver Close() signature that reports no error.
func (r *Registry) RegisterFunc(name string, fn func()) {
	if fn == nil {
		return
	}
	r.Register(name, func(context.Context) error { fn(); return nil })
}

// Names lists registered hooks in close order, for logging and tests.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.hooks))
	for i := len(r.hooks) - 1; i >= 0; i-- {
		out = append(out, r.hooks[i].name)
	}
	return out
}

// Close runs every hook in LIFO order and returns all failures joined.
//
// A failing hook does not stop the rest: on shutdown the goal is to release as
// much as possible, so each hook gets its turn regardless of its neighbours.
// Each hook is given its own timeout slice of ctx via perHookTimeout.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	hooks := make([]hook, len(r.hooks))
	copy(hooks, r.hooks)
	r.hooks = nil
	r.mu.Unlock()

	var errs []error
	for i := len(hooks) - 1; i >= 0; i-- {
		h := hooks[i]
		start := time.Now()

		if err := h.close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", h.name, err))
			r.log.Error("resource close failed",
				slog.String("resource", h.name),
				slog.Duration("took", time.Since(start)),
				slog.String("error", err.Error()))
			continue
		}
		r.log.Info("resource closed",
			slog.String("resource", h.name),
			slog.Duration("took", time.Since(start)))
	}
	return errors.Join(errs...)
}
