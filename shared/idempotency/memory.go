package idempotency

import (
	"context"
	"sync"
	"time"
)

type MemoryStore struct {
	mu    sync.Mutex
	items map[string]*Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: make(map[string]*Record),
	}
}

func (s *MemoryStore) LockOrGet(ctx context.Context, key string, ttl time.Duration) (*Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if rec, exists := s.items[key]; exists {
		if now.Before(rec.ExpiresAt) {
			copied := *rec
			return &copied, false, nil
		}
		delete(s.items, key)
	}

	record := &Record{
		Key:       key,
		Status:    StatusInProgress,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	s.items[key] = record
	return nil, true, nil
}

func (s *MemoryStore) Resolve(ctx context.Context, key string, code int, body []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.items[key] = &Record{
		Key:          key,
		Status:       StatusResolved,
		ResponseCode: code,
		ResponseBody: string(body),
		CreatedAt:    now,
		ExpiresAt:    now.Add(ttl),
	}
	return nil
}

func (s *MemoryStore) Release(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.items, key)
	return nil
}
