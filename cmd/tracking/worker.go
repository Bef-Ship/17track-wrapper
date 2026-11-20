package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/SolaTyolo/17track-wrapper/internal/application"
	"github.com/SolaTyolo/17track-wrapper/internal/callback"
	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/internal/infrastructure"
	"github.com/SolaTyolo/17track-wrapper/pkg/config"
	"github.com/SolaTyolo/17track-wrapper/pkg/cronlock"
	"github.com/SolaTyolo/17track-wrapper/pkg/event"
	blog "github.com/SolaTyolo/17track-wrapper/pkg/log"
)

const (
	cronSpec = "0 0 0,6,18 * * *"
	// cronJobsTTL bounds one run of all jobs (cron lease and job ctx).
	cronJobsTTL = 2 * time.Hour
)

// worker runs cron jobs and event-bus consumers against the in-process service.
type worker struct {
	svc       *application.Service
	bus       event.Provider
	projector *callback.ShipmentProjector
	archive   bool

	cron   *cron.Cron
	locker *cronlock.Locker
}

func (w *worker) start(ctx context.Context) error {
	wlog := blog.Svc("tracking-worker")
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.UTC
	}
	if w.locker, err = cronlock.NewFromURL(config.RedisURL()); err != nil {
		return fmt.Errorf("cronlock: %w", err)
	}
	if w.locker == nil {
		wlog.Warn("REDIS_URL empty: cron jobs run unguarded on every replica")
	}
	w.cron = cron.New(cron.WithLocation(loc), cron.WithSeconds())
	if _, err := w.cron.AddFunc(cronSpec, func() {
		ran, err := w.locker.Run(ctx, "tracking.jobs", cronJobsTTL, w.runJobs)
		if err != nil {
			wlog.Error("cron lock failed", "err", err.Error())
		} else if !ran {
			wlog.Info("cron jobs skipped: running on another replica")
		}
	}); err != nil {
		return fmt.Errorf("cron: %w", err)
	}
	w.cron.Start()

	if w.bus == nil {
		wlog.Warn("event bus unavailable: 17TRACK pushes, register retries and callbacks are not consumed")
		return nil
	}
	webhookCh, err := event.Channel(event.SourceExternal, "17track")
	if err != nil {
		return err
	}
	w.subscribe(ctx, webhookCh, func(ctx context.Context, raw json.RawMessage) error {
		return permanentIf(w.svc.ApplyWebhookBody(ctx, raw), domain.ErrInvalid)
	})
	w.subscribe(ctx, infrastructure.RegisterChannel(), func(ctx context.Context, raw json.RawMessage) error {
		var job domain.RegisterJob
		if err := json.Unmarshal(raw, &job); err != nil {
			return fmt.Errorf("decode register job: %v: %w", err, event.ErrPermanent)
		}
		return w.svc.ProcessRegisterJob(ctx, job)
	})
	if w.projector.URL == "" {
		wlog.Warn("SHIPMENT_CALLBACK_URL empty: queued shipment callbacks are dropped")
	}
	w.subscribe(ctx, callback.QueueChannel(), func(ctx context.Context, raw json.RawMessage) error {
		var body callback.Payload
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("decode callback: %v: %w", err, event.ErrPermanent)
		}
		return w.projector.Deliver(ctx, body)
	})
	return nil
}

func (w *worker) subscribe(ctx context.Context, ch string, fn func(context.Context, json.RawMessage) error) {
	go func() {
		err := w.bus.Subscribe(ctx, ch, func(ctx context.Context, env *event.Envelope) error {
			return fn(ctx, env.RawEvent)
		})
		if err != nil && ctx.Err() == nil {
			blog.Svc("tracking-worker").Error("subscribe failed", "channel", ch, "err", err.Error())
		}
	}()
}

func (w *worker) stop() {
	if w.cron != nil {
		select {
		case <-w.cron.Stop().Done():
		case <-time.After(30 * time.Second):
			blog.Svc("tracking-worker").Warn("cron job still running at shutdown; exiting")
		}
	}
	_ = w.locker.Close()
}

type cronJob struct {
	name string
	run  func(context.Context) (int, error)
}

func (w *worker) runJobs(ctx context.Context) {
	wlog := blog.Svc("tracking-worker")
	jobs := []cronJob{
		{"sync-active", w.svc.SyncActive},
		{"stop-stale", w.svc.StopStale},
		{"retrack", w.svc.RetrackStopped},
		{"retry-registering", w.svc.RetryRegistering},
	}
	if w.archive {
		jobs = append(jobs, cronJob{"archive", w.svc.ArchiveDuePackages})
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		n, err := j.run(ctx)
		if err != nil {
			wlog.Error(j.name+" failed", "err", err.Error())
			continue
		}
		wlog.Info(j.name, "count", n)
	}
}

// permanentIf marks err as non-retryable when it wraps target.
func permanentIf(err, target error) error {
	if err != nil && errors.Is(err, target) {
		return fmt.Errorf("%w: %w", err, event.ErrPermanent)
	}
	return err
}
