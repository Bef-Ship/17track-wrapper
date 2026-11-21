package asynqprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/SolaTyolo/17track-wrapper/pkg/event"
)

const (
	defaultTaskType = "tracking.event"
	idempTTL        = 24 * time.Hour
	idempPrefix     = "tracking:event:id:"
	maxRetry        = 8
)

// Option configures a Provider.
type Option func(*Provider)

// WithTaskType sets the Asynq task type for publish and subscribe.
// Empty values are ignored (keeps defaultTaskType / prior option).
func WithTaskType(taskType string) Option {
	return func(p *Provider) {
		if t := strings.TrimSpace(taskType); t != "" {
			p.taskType = t
		}
	}
}

// Provider is the V1 EventProvider: Asynq over Redis (persist, ACK, retry, archive/DLQ, idempotent eventId).
type Provider struct {
	opt      asynq.RedisConnOpt
	client   *asynq.Client
	rdb      redis.UniversalClient
	taskType string
}

func New(redisURL string, opts ...Option) (*Provider, error) {
	if strings.TrimSpace(redisURL) == "" {
		return nil, fmt.Errorf("asynq provider: REDIS_URL is required")
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("asynq provider: parse REDIS_URL: %w", err)
	}
	raw := opt.MakeRedisClient()
	rdb, ok := raw.(redis.UniversalClient)
	if !ok {
		return nil, fmt.Errorf("asynq provider: unexpected redis client %T", raw)
	}
	p := &Provider{
		opt:      opt,
		client:   asynq.NewClient(opt),
		rdb:      rdb,
		taskType: defaultTaskType,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

func (p *Provider) Publish(ctx context.Context, channel string, scope event.Scope, rawEvent any) error {
	env, err := event.WrapContext(ctx, channel, scope, rawEvent)
	if err != nil {
		return err
	}
	dedupe := event.DedupeKey(ctx) != ""
	key := idempPrefix + env.EventID
	if dedupe {
		n, err := p.rdb.Exists(ctx, key).Result()
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	task := asynq.NewTask(p.taskType, payload, asynq.MaxRetry(maxRetry))
	_, err = p.client.EnqueueContext(ctx, task,
		asynq.Queue(QueueName(channel)),
		asynq.TaskID(env.EventID),
	)
	if err != nil && !errors.Is(err, asynq.ErrDuplicateTask) && !errors.Is(err, asynq.ErrTaskIDConflict) {
		return err
	}
	if dedupe {
		if setErr := p.rdb.Set(ctx, key, "1", idempTTL).Err(); setErr != nil {
			return setErr
		}
	}
	return nil
}

func (p *Provider) Subscribe(ctx context.Context, channel string, handler event.Handler) error {
	if handler == nil {
		return fmt.Errorf("asynq provider: nil handler")
	}
	if _, _, err := event.ParseChannel(channel); err != nil {
		return err
	}
	q := QueueName(channel)
	srv := asynq.NewServer(p.opt, asynq.Config{
		Concurrency: 4,
		Queues:      map[string]int{q: 10},
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(p.taskType, func(ctx context.Context, t *asynq.Task) error {
		var env event.Envelope
		if err := json.Unmarshal(t.Payload(), &env); err != nil {
			return fmt.Errorf("decode envelope: %v: %w", err, asynq.SkipRetry)
		}
		if env.Channel != channel {
			return nil
		}
		err := handler(ctx, &env)
		if errors.Is(err, event.ErrPermanent) {
			return fmt.Errorf("%v: %w", err, asynq.SkipRetry)
		}
		return err
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(mux)
	}()
	select {
	case <-ctx.Done():
		srv.Shutdown()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Ping checks that Redis at redisURL is reachable (New does not connect).
func Ping(ctx context.Context, redisURL string) error {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return fmt.Errorf("asynq provider: parse REDIS_URL: %w", err)
	}
	rdb, ok := opt.MakeRedisClient().(redis.UniversalClient)
	if !ok {
		return fmt.Errorf("asynq provider: unexpected redis client")
	}
	defer func() { _ = rdb.Close() }()
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pctx).Err(); err != nil {
		return fmt.Errorf("asynq provider: redis ping: %w", err)
	}
	return nil
}

func (p *Provider) Close() error {
	var first error
	if p.client != nil {
		first = p.client.Close()
	}
	if p.rdb != nil {
		if err := p.rdb.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func QueueName(channel string) string {
	return strings.ReplaceAll(channel, ".", "_")
}
