package track17

import (
	"context"
	"log/slog"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// DefaultQPS is 17TRACK's documented request ceiling (~3 req/s).
const DefaultQPS = 3

// RateLimited wraps a Client and waits on a shared limiter before each call.
type RateLimited struct {
	inner  Client
	lim    *rate.Limiter
	shared *redisSpacer
}

// WithRateLimit returns a Client that enforces qps (burst=1). qps<=0 disables limiting.
func WithRateLimit(inner Client, qps float64) Client {
	if inner == nil || qps <= 0 {
		return inner
	}
	return &RateLimited{
		inner: inner,
		lim:   rate.NewLimiter(rate.Limit(qps), 1),
	}
}

func (c *RateLimited) Token() string { return c.inner.Token() }

// WithSharedLimit makes a client from WithRateLimit coordinate through Redis, so all
// replicas together stay under the same qps (17TRACK limits per account, not per
// process). Redis errors fall back to the local limiter.
func WithSharedLimit(c Client, rdb redis.UniversalClient, key string) Client {
	rl, ok := c.(*RateLimited)
	if !ok || rdb == nil || rl.lim == nil {
		return c
	}
	interval := time.Duration(float64(time.Second) / float64(rl.lim.Limit()))
	rl.shared = &redisSpacer{rdb: rdb, key: key, interval: interval}
	return rl
}

func (c *RateLimited) wait(ctx context.Context) error {
	if c == nil || c.lim == nil {
		return nil
	}
	if c.shared != nil {
		d, err := c.shared.reserve(ctx)
		if err == nil {
			if d <= 0 {
				return nil
			}
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
		slog.Warn("track17 shared rate limit unavailable; using local limiter", "err", err)
	}
	return c.lim.Wait(ctx)
}

// reserveSlot hands out call slots spaced interval apart (burst 1) using Redis time,
// returning how long the caller must wait for its slot.
var reserveSlot = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local interval = tonumber(ARGV[1])
local nxt = tonumber(redis.call('GET', KEYS[1]) or '0')
if nxt < now then nxt = now end
redis.call('SET', KEYS[1], nxt + interval, 'PX', nxt + interval - now + 1000)
return nxt - now
`)

type redisSpacer struct {
	rdb      redis.UniversalClient
	key      string
	interval time.Duration
}

func (r *redisSpacer) reserve(ctx context.Context) (time.Duration, error) {
	ms := r.interval.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	wait, err := reserveSlot.Run(ctx, r.rdb, []string{r.key}, ms).Int64()
	if err != nil {
		return 0, err
	}
	return time.Duration(wait) * time.Millisecond, nil
}

func (c *RateLimited) Register(ctx context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.Register(ctx, items)
}

func (c *RateLimited) StopTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.StopTrack(ctx, items)
}

func (c *RateLimited) Retrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.Retrack(ctx, items)
}

func (c *RateLimited) ChangeCarrier(ctx context.Context, items []sdk.ChangeCarrierItem) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.ChangeCarrier(ctx, items)
}

func (c *RateLimited) DeleteTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.DeleteTrack(ctx, items)
}

func (c *RateLimited) GetTrackInfo(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.TrackDetail], error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.GetTrackInfo(ctx, items)
}

var _ Client = (*RateLimited)(nil)
