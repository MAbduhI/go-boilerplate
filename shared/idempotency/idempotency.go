package idempotency

import (
	"context"
	"errors"
	"time"
)

var (
	ErrConflict = errors.New("idempotency: request with this key is already in progress")
)

type Status string

const (
	StatusInProgress Status = "IN_PROGRESS"
	StatusResolved   Status = "RESOLVED"
)

type Record struct {
	Key          string    `json:"key"`
	Status       Status    `json:"status"`
	ResponseCode int       `json:"response_code"`
	ResponseBody string    `json:"response_body"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type Store interface {
	LockOrGet(ctx context.Context, key string, ttl time.Duration) (*Record, bool, error)
	Resolve(ctx context.Context, key string, statusCode int, responseBody []byte, ttl time.Duration) error
	Release(ctx context.Context, key string) error
}
