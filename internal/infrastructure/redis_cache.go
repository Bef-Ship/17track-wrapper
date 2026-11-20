package infrastructure

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
)

// RedisStore implements IdempotencyStore.
type RedisStore struct {
	rdb redis.UniversalClient
}

func NewRedisStore(rdb redis.UniversalClient) *RedisStore {
	return &RedisStore{rdb: rdb}
}

func (s *RedisStore) Claim(ctx context.Context, key string, ttlSeconds int) (bool, error) {
	if s == nil || s.rdb == nil {
		return true, nil
	}
	return s.rdb.SetNX(ctx, key, "1", ttlOrDay(ttlSeconds)).Result()
}

func (s *RedisStore) Release(ctx context.Context, key string) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Del(ctx, key).Err()
}

func ttlOrDay(ttlSeconds int) time.Duration {
	if ttlSeconds <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(ttlSeconds) * time.Second
}

// MemoryStore is an in-process fallback when Redis is unavailable.
type MemoryStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{seen: map[string]time.Time{}}
}

func (m *MemoryStore) Claim(_ context.Context, key string, ttlSeconds int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if exp, ok := m.seen[key]; ok && now.Before(exp) {
		return false, nil
	}
	m.seen[key] = now.Add(ttlOrDay(ttlSeconds))
	return true, nil
}

func (m *MemoryStore) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, key)
	return nil
}

var (
	_ domain.IdempotencyStore = (*RedisStore)(nil)
	_ domain.IdempotencyStore = (*MemoryStore)(nil)
)
