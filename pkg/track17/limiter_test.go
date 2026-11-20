package track17

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSharedLimitSpacesCallsAcrossClients(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	a := WithSharedLimit(WithRateLimit(NewNop(), 10), rdb, "t:rl").(*RateLimited)
	b := WithSharedLimit(WithRateLimit(NewNop(), 10), rdb, "t:rl").(*RateLimited)

	var waits []time.Duration
	for _, c := range []*RateLimited{a, b, a} {
		d, err := c.shared.reserve(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		waits = append(waits, d)
	}
	if waits[0] != 0 || waits[1] < 90*time.Millisecond || waits[2] < 190*time.Millisecond {
		t.Fatalf("slots not spaced across clients: %v", waits)
	}
}

func TestSharedLimitFallsBackWithoutRedis(t *testing.T) {
	c := WithSharedLimit(WithRateLimit(NewNop(), 10), nil, "t:rl")
	if c.(*RateLimited).shared != nil {
		t.Fatal("nil redis should keep the local limiter only")
	}
}
