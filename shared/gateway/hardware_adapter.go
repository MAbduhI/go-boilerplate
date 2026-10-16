package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RouterNode represents a network edge hardware router node.
type RouterNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Vendor   string `json:"vendor"`
	HostIP   string `json:"host_ip"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// CircuitBreakerState represents the status of router API connectivity.
type CircuitBreakerState string

const (
	StateClosed   CircuitBreakerState = "CLOSED"
	StateHalfOpen CircuitBreakerState = "HALF_OPEN"
	StateOpen     CircuitBreakerState = "OPEN"
)

var (
	ErrCircuitBreakerOpen = errors.New("router gateway circuit breaker is OPEN: hardware temporarily unreachable")
	ErrRouterTimeout      = errors.New("router gateway operation timed out")
)

type CircuitBreaker struct {
	mu          sync.Mutex
	state       CircuitBreakerState
	failures    int
	maxFailures int
	timeout     time.Duration
	lastFailure time.Time
}

func NewCircuitBreaker(maxFailures int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       StateClosed,
		maxFailures: maxFailures,
		timeout:     timeout,
	}
}

func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()
	if cb.state == StateOpen {
		if time.Since(cb.lastFailure) > cb.timeout {
			cb.state = StateHalfOpen
		} else {
			cb.mu.Unlock()
			return ErrCircuitBreakerOpen
		}
	}
	cb.mu.Unlock()

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()
		if cb.failures >= cb.maxFailures {
			cb.state = StateOpen
		}
		return err
	}

	cb.failures = 0
	cb.state = StateClosed
	return nil
}

type HardwareAdapter interface {
	ApplyBandwidthLimit(ctx context.Context, router *RouterNode, pppoeUser string, downKbps, upKbps int, idempotencyKey string) error
	ApplyIsolir(ctx context.Context, router *RouterNode, pppoeUser, ipAddress string) error
	RemoveIsolir(ctx context.Context, router *RouterNode, pppoeUser, ipAddress string) error
}

type RouterGateway struct {
	cb             *CircuitBreaker
	dispatched     []string
	executedKeys   map[string]string // Idempotency cache: key -> cached command output
	executionCount int               // Counter for actual hardware transmissions
	mu             sync.Mutex
}

func NewRouterGateway() *RouterGateway {
	return &RouterGateway{
		cb:           NewCircuitBreaker(3, 5*time.Second),
		dispatched:   make([]string, 0),
		executedKeys: make(map[string]string),
	}
}

// ApplyBandwidthLimit sends RADIUS CoA / Simple Queue bandwidth shaping.
// It is strictly idempotent via idempotencyKey: duplicate requests with the same key
// return immediate success without duplicate router transmissions.
func (g *RouterGateway) ApplyBandwidthLimit(ctx context.Context, router *RouterNode, pppoeUser string, downKbps, upKbps int, idempotencyKey string) error {
	return g.cb.Execute(func() error {
		g.mu.Lock()
		defer g.mu.Unlock()

		// 1. Idempotency Check: Return cached dispatch if already executed
		if idempotencyKey != "" {
			if _, exists := g.executedKeys[idempotencyKey]; exists {
				return nil
			}
		}

		// 2. Timeout / context check
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w: %v", ErrRouterTimeout, ctx.Err())
			}
			return ctx.Err()
		default:
		}

		cmd := fmt.Sprintf("[%s] CoA/Queue updated for user '%s' on %s (%s) -> Down: %d Kbps, Up: %d Kbps (Key: %s)",
			router.Vendor, pppoeUser, router.Name, router.HostIP, downKbps, upKbps, idempotencyKey)

		g.dispatched = append(g.dispatched, cmd)
		g.executionCount++
		if idempotencyKey != "" {
			g.executedKeys[idempotencyKey] = cmd
		}
		return nil
	})
}

func (g *RouterGateway) ApplyIsolir(ctx context.Context, router *RouterNode, pppoeUser, ipAddress string) error {
	return g.cb.Execute(func() error {
		g.mu.Lock()
		defer g.mu.Unlock()

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w: %v", ErrRouterTimeout, ctx.Err())
			}
			return ctx.Err()
		default:
		}

		cmd := fmt.Sprintf("[%s] Firewall Address-List ISOLIR added for '%s' (IP: %s) on %s",
			router.Vendor, pppoeUser, ipAddress, router.Name)
		g.dispatched = append(g.dispatched, cmd)
		g.executionCount++
		return nil
	})
}

func (g *RouterGateway) RemoveIsolir(ctx context.Context, router *RouterNode, pppoeUser, ipAddress string) error {
	return g.cb.Execute(func() error {
		g.mu.Lock()
		defer g.mu.Unlock()

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%w: %v", ErrRouterTimeout, ctx.Err())
			}
			return ctx.Err()
		default:
		}

		cmd := fmt.Sprintf("[%s] Firewall Address-List ISOLIR removed for '%s' (IP: %s) on %s (<1s unblock)",
			router.Vendor, pppoeUser, ipAddress, router.Name)
		g.dispatched = append(g.dispatched, cmd)
		g.executionCount++
		return nil
	})
}

func (g *RouterGateway) GetDispatchedCommands() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.dispatched))
	copy(out, g.dispatched)
	return out
}

func (g *RouterGateway) GetExecutionCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.executionCount
}

func (g *RouterGateway) IsKeyExecuted(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, exists := g.executedKeys[key]
	return exists
}
