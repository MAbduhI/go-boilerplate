package idempotency

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_Sequential(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	key := "test-order-123"

	// 1. Lock new key
	rec, acquired, err := store.LockOrGet(ctx, key, 10*time.Minute)
	if err != nil || !acquired {
		t.Fatalf("expected lock acquired, got acquired=%v, err=%v", acquired, err)
	}

	// 2. Resolve with response
	body := []byte(`{"order_id":123,"status":"PAID"}`)
	if err := store.Resolve(ctx, key, 201, body, 10*time.Minute); err != nil {
		t.Fatalf("failed to resolve: %v", err)
	}

	// 3. Second call returns cached
	rec, acquired, err = store.LockOrGet(ctx, key, 10*time.Minute)
	if err != nil || acquired {
		t.Fatalf("expected cached record, got acquired=%v, err=%v", acquired, err)
	}
	if rec.ResponseCode != 201 || rec.ResponseBody != string(body) {
		t.Fatalf("unexpected cached payload: %+v", rec)
	}
}

func TestMemoryStore_ConcurrentLock(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	key := "concurrent-test"

	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	acquiredCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			_, acquired, _ := store.LockOrGet(ctx, key, time.Minute)
			if acquired {
				mu.Lock()
				acquiredCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if acquiredCount != 1 {
		t.Fatalf("expected exactly 1 goroutine to acquire lock, got: %d", acquiredCount)
	}
}
