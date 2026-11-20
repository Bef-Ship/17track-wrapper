// Package cronlock makes a cron job run on one worker replica at a time via a Redis lease.
package cronlock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const keyPrefix = "tracking:cronlock:"

// Locker hands out per-job leases. A nil Locker runs every job unguarded.
type Locker struct {
	rdb redis.UniversalClient
}

// NewFromURL returns nil (unguarded) when redisURL is empty.
func NewFromURL(redisURL string) (*Locker, error) {
	if strings.TrimSpace(redisURL) == "" {
		return nil, nil
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cronlock: parse REDIS_URL: %w", err)
	}
	rdb, ok := opt.MakeRedisClient().(redis.UniversalClient)
	if !ok {
		return nil, fmt.Errorf("cronlock: unexpected redis client")
	}
	return New(rdb), nil
}

func New(rdb redis.UniversalClient) *Locker {
	if rdb == nil {
		return nil
	}
	return &Locker{rdb: rdb}
}

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

// Run executes fn when this replica wins the lease for name. ttl must exceed the job's
// worst-case runtime; fn's context is bounded by ttl so the lease cannot lapse mid-run.
// Returns false when another replica holds the lease.
func (l *Locker) Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context)) (bool, error) {
	jobCtx, cancel := context.WithTimeout(ctx, ttl)
	defer cancel()
	if l == nil {
		fn(jobCtx)
		return true, nil
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return false, err
	}
	token := hex.EncodeToString(tok)
	key := keyPrefix + name
	ok, err := l.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cronlock %s: %w", name, err)
	}
	if !ok {
		return false, nil
	}
	defer releaseScript.Run(context.WithoutCancel(ctx), l.rdb, []string{key}, token)
	fn(jobCtx)
	return true, nil
}

func (l *Locker) Close() error {
	if l == nil {
		return nil
	}
	return l.rdb.Close()
}
