package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *goredis.Client
	prefix string
}

func NewRedisStore(client *goredis.Client, prefix string) *RedisStore {
	if prefix == "" {
		prefix = "idemp"
	}
	return &RedisStore{
		client: client,
		prefix: prefix,
	}
}

func (s *RedisStore) k(key string) string {
	return fmt.Sprintf("%s:%s", s.prefix, key)
}

func (s *RedisStore) LockOrGet(ctx context.Context, key string, ttl time.Duration) (*Record, bool, error) {
	redisKey := s.k(key)

	ok, err := s.client.SetNX(ctx, redisKey, string(StatusInProgress), 30*time.Second).Result()
	if err != nil {
		return nil, false, err
	}
	if ok {
		return nil, true, nil
	}

	val, err := s.client.Get(ctx, redisKey).Result()
	if err != nil {
		if err == goredis.Nil {
			return s.LockOrGet(ctx, key, ttl)
		}
		return nil, false, err
	}

	if val == string(StatusInProgress) {
		return &Record{Key: key, Status: StatusInProgress}, false, nil
	}

	var rec Record
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		return nil, false, err
	}

	return &rec, false, nil
}

func (s *RedisStore) Resolve(ctx context.Context, key string, code int, body []byte, ttl time.Duration) error {
	redisKey := s.k(key)
	now := time.Now().UTC()
	rec := Record{
		Key:          key,
		Status:       StatusResolved,
		ResponseCode: code,
		ResponseBody: string(body),
		CreatedAt:    now,
		ExpiresAt:    now.Add(ttl),
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	return s.client.Set(ctx, redisKey, string(data), ttl).Err()
}

func (s *RedisStore) Release(ctx context.Context, key string) error {
	return s.client.Del(ctx, s.k(key)).Err()
}
