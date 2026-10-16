package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTxPoolCapacityEnforced(t *testing.T) {
	c, _ := newMockClient(t)
	pool := NewTxPool(c, TxPoolConfig{
		Capacity:       2,
		AcquireTimeout: 50 * time.Millisecond,
		MaxRetries:     0,
	})

	started1 := make(chan struct{})
	release1 := make(chan struct{})
	started2 := make(chan struct{})
	release2 := make(chan struct{})

	// Tx 1
	go func() {
		_ = pool.Execute(context.Background(), func(ctx context.Context) error {
			close(started1)
			<-release1
			return nil
		})
	}()

	// Tx 2
	go func() {
		_ = pool.Execute(context.Background(), func(ctx context.Context) error {
			close(started2)
			<-release2
			return nil
		})
	}()

	<-started1
	<-started2

	// Verify both slots are active
	if active := pool.Active(); active != 2 {
		t.Fatalf("expected 2 active transactions, got %d", active)
	}

	// Tx 3 must fail with ErrTxPoolExhausted because capacity is 2 and both are busy
	err := pool.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrTxPoolExhausted) {
		t.Fatalf("expected ErrTxPoolExhausted, got %v", err)
	}

	// Release Tx 1
	close(release1)
	time.Sleep(20 * time.Millisecond)

	// Now a new transaction should succeed
	err = pool.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected slot acquisition after release, got %v", err)
	}

	close(release2)
}

func TestTxPoolReentrancyDoesNotConsumePermit(t *testing.T) {
	c, mock := newMockClient(t)
	c.txPool = NewTxPool(c, TxPoolConfig{
		Capacity:       1, // Only 1 permit!
		AcquireTimeout: 50 * time.Millisecond,
	})

	mock.ExpectBegin()
	mock.ExpectCommit()

	// Outer transaction consumes the single permit.
	// An inner transaction inside it must NOT block waiting for a permit, but use savepoints.
	err := c.WithTransaction(context.Background(), func(outerCtx context.Context) error {
		if !InTransaction(outerCtx) {
			t.Fatal("expected outer to be in transaction")
		}

		// Inner transaction: should succeed because it reuses ambient tx
		return c.WithTransaction(outerCtx, func(innerCtx context.Context) error {
			if !InTransaction(innerCtx) {
				t.Fatal("expected inner to be in transaction")
			}
			return nil
		})
	})

	if err != nil {
		t.Fatalf("re-entrant transaction failed: %v", err)
	}
}

func TestTxPoolStats(t *testing.T) {
	c, _ := newMockClient(t)
	pool := NewTxPool(c, TxPoolConfig{
		Capacity:       3,
		AcquireTimeout: 50 * time.Millisecond,
	})

	// 1 commit
	_ = pool.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	// 1 rollback
	_ = pool.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("business error")
	})

	stats := pool.Stats()
	if stats.Capacity != 3 {
		t.Errorf("expected Capacity 3, got %d", stats.Capacity)
	}
	if stats.Committed != 1 {
		t.Errorf("expected Committed 1, got %d", stats.Committed)
	}
	if stats.RolledBack != 1 {
		t.Errorf("expected RolledBack 1, got %d", stats.RolledBack)
	}
}

func TestTxPoolTransientErrorRetry(t *testing.T) {
	c, _ := newMockClient(t)
	pool := NewTxPool(c, TxPoolConfig{
		Capacity:       5,
		AcquireTimeout: 1 * time.Second,
		MaxRetries:     2,
		RetryBackoff:   5 * time.Millisecond,
	})

	var attempts int
	var mu sync.Mutex

	err := pool.Execute(context.Background(), func(ctx context.Context) error {
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()

		if current == 1 {
			// Simulate PostgreSQL 40P01 deadlock_detected error
			return errors.New("ERROR: deadlock detected (SQLSTATE 40P01)")
		}
		// Second attempt succeeds
		return nil
	})

	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}

	mu.Lock()
	finalAttempts := attempts
	mu.Unlock()

	if finalAttempts != 2 {
		t.Fatalf("expected 2 attempts (1 failure + 1 retry), got %d", finalAttempts)
	}

	stats := pool.Stats()
	if stats.Retried != 1 {
		t.Fatalf("expected stats.Retried == 1, got %d", stats.Retried)
	}
	if stats.Committed != 1 {
		t.Fatalf("expected stats.Committed == 1, got %d", stats.Committed)
	}
}

func TestTxPoolContextCancellation(t *testing.T) {
	c, _ := newMockClient(t)
	pool := NewTxPool(c, TxPoolConfig{
		Capacity:       1,
		AcquireTimeout: 1 * time.Second,
	})

	// Fill the 1 slot
	hold := make(chan struct{})
	go func() {
		_ = pool.Execute(context.Background(), func(ctx context.Context) error {
			<-hold
			return nil
		})
	}()

	time.Sleep(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := pool.Execute(ctx, func(ctx context.Context) error {
		return nil
	})

	if err == nil || !errors.Is(err, ErrTxPoolExhausted) {
		t.Fatalf("expected context cancellation / pool exhausted error, got: %v", err)
	}

	close(hold)
}
