// Command tracking runs the 17TRACK wrapper. One binary, two roles:
//
//	api    — HTTP API (+ the /v1/webhooks/17track sync fallback)
//	worker — cron jobs (sync / stop / retrack / retry-registering / archive) and the
//	         event-bus consumers (17TRACK pushes, register retries, shipment callbacks)
//
// TRACKING_ROLE=api|worker|all selects it; when unset the role follows the executable
// name (tracking-worker → worker, anything else → api).
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/SolaTyolo/17track-wrapper/internal/application"
	"github.com/SolaTyolo/17track-wrapper/internal/callback"
	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/internal/httpapi"
	"github.com/SolaTyolo/17track-wrapper/internal/infrastructure"
	"github.com/SolaTyolo/17track-wrapper/internal/migrate"
	"github.com/SolaTyolo/17track-wrapper/pkg/config"
	"github.com/SolaTyolo/17track-wrapper/pkg/event"
	"github.com/SolaTyolo/17track-wrapper/pkg/event/asynqprovider"
	blog "github.com/SolaTyolo/17track-wrapper/pkg/log"
	"github.com/SolaTyolo/17track-wrapper/pkg/objstore"
	"github.com/SolaTyolo/17track-wrapper/pkg/track17"
)

func main() {
	blog.Init(config.Env("LOG_LEVEL", "info"))
	role := resolveRole(config.Env("TRACKING_ROLE", ""), os.Args[0])
	wlog := blog.Svc("tracking")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, config.TrackingDatabaseURL())
	if err != nil {
		blog.Fatalf("db: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		blog.Fatalf("db ping: %v", err)
	}
	if err := migrate.Apply(ctx, pool); err != nil {
		blog.Fatalf("migrate: %v", err)
	}

	rdb := openRedis(config.RedisURL())
	var idempStore domain.IdempotencyStore = infrastructure.NewMemoryStore()
	if rdb != nil {
		idempStore = infrastructure.NewRedisStore(rdb)
	}

	// No in-memory fallback: only the worker role consumes the bus, so without Redis
	// register retries and callbacks run inline instead of being queued for nobody.
	var bus event.Provider
	if b, err := asynqprovider.New(config.RedisURL()); err != nil {
		if config.IsProduction() {
			blog.Fatalf("event bus: %v", err)
		}
		wlog.Warn("event bus unavailable; register retries and callbacks run inline", "err", err.Error())
	} else {
		bus = b
		defer func() { _ = b.Close() }()
	}

	track17Client := track17.WithSharedLimit(track17.NewClientForAppEnv(), rdb, "track17:ratelimit")

	var cold domain.ColdStore
	archiveBucket := config.Env("TRACKING_ARCHIVE_BUCKET", "")
	if archiveBucket == "" {
		archiveBucket = config.S3DiskBucket()
	}
	if strings.EqualFold(config.Env("ARCHIVE_ENABLED", "false"), "true") && archiveBucket != "" {
		cold = infrastructure.NewObjArchive(newObj(archiveBucket, config.Env("S3_CDN_BASE_URL", "")))
	}

	retentionDays, err := config.EnvInt("ARCHIVE_RETENTION_DAYS", application.DefaultArchiveRetentionDays)
	if err != nil {
		blog.Fatalf("ARCHIVE_RETENTION_DAYS: %v", err)
	}
	archiveBatch, err := config.EnvInt("ARCHIVE_BATCH_SIZE", 200)
	if err != nil {
		blog.Fatalf("ARCHIVE_BATCH_SIZE: %v", err)
	}
	syncStaleHours, err := config.EnvInt("SYNC_STALE_HOURS", 12)
	if err != nil {
		blog.Fatalf("SYNC_STALE_HOURS: %v", err)
	}

	svc := application.NewService(
		infrastructure.NewPostgresRepository(pool),
		track17Client,
		idempStore,
		cold,
		application.Options{
			ArchiveRetentionDays: retentionDays,
			ArchiveBatchSize:     archiveBatch,
			SyncStaleAfter:       time.Duration(syncStaleHours) * time.Hour,
		},
	)
	if bus != nil {
		svc.WithRegisterQueue(infrastructure.NewRegisterQueue(bus))
	}

	projector := &callback.ShipmentProjector{
		URL:    strings.TrimSpace(config.Env("SHIPMENT_CALLBACK_URL", "")),
		Secret: config.Env("SHIPMENT_CALLBACK_SECRET", ""),
	}
	if projector.URL != "" {
		if bus != nil {
			// The worker role delivers to SHIPMENT_CALLBACK_URL with retries.
			svc.AddObserver(&callback.QueuedProjector{Bus: bus})
		} else {
			svc.AddObserver(projector)
		}
	}

	if role == roleWorker || role == roleAll {
		w := &worker{svc: svc, bus: bus, projector: projector, archive: cold != nil}
		if err := w.start(ctx); err != nil {
			blog.Fatalf("worker: %v", err)
		}
		defer w.stop()
	}
	if role == roleAPI || role == roleAll {
		tokens, err := httpapi.ParseServiceTokens(config.Env("TRACKING_SERVICE_TOKENS", ""))
		if err != nil {
			blog.Fatalf("TRACKING_SERVICE_TOKENS: %v", err)
		}
		if legacy := config.Env("TRACKING_API_TOKEN", ""); legacy != "" {
			if _, dup := tokens["default"]; !dup {
				tokens["default"] = legacy
			}
		}
		if len(tokens) == 0 {
			if config.IsProduction() {
				blog.Fatalf("TRACKING_SERVICE_TOKENS is required in production")
			}
			wlog.Warn("no service tokens: HTTP API is unauthenticated")
		}
		addr := config.Env("HTTP_ADDR", ":8092")
		srv := &http.Server{
			Addr:              addr,
			Handler:           httpapi.New(svc, tokens).WithReadiness(pool.Ping).Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      2 * time.Minute,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			wlog.Info("listening", "addr", addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				blog.Fatalf("http: %v", err)
			}
		}()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		}()
	}

	wlog.Info("started", "role", role)
	<-ctx.Done()
}

const (
	roleAPI    = "api"
	roleWorker = "worker"
	roleAll    = "all"
)

func resolveRole(env, argv0 string) string {
	switch r := strings.ToLower(strings.TrimSpace(env)); r {
	case roleAPI, roleWorker, roleAll:
		return r
	case "":
	default:
		blog.Fatalf("TRACKING_ROLE: unknown role %q (api|worker|all)", env)
	}
	if filepath.Base(argv0) == "tracking-worker" {
		return roleWorker
	}
	return roleAPI
}

func openRedis(redisURL string) redis.UniversalClient {
	if strings.TrimSpace(redisURL) == "" {
		return nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		blog.Warnf("redis parse: %v", err)
		return nil
	}
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		blog.Warnf("redis ping: %v", err)
		return nil
	}
	return rdb
}

func newObj(bucket, cdn string) *objstore.Client {
	endpoint := config.Env("OBJ_GATEWAY_URL", "")
	public := config.Env("OBJ_GATEWAY_PUBLIC_URL", "")
	secret := config.Env("OBJ_GATEWAY_JWT_SECRET", "")
	if secret == "" {
		secret = config.Env("GOTRUE_JWT_SECRET", "")
	}
	cdn = strings.TrimRight(strings.TrimSpace(cdn), "/")
	c, err := objstore.New(objstore.Config{
		Endpoint:       endpoint,
		PublicEndpoint: public,
		JWTSecret:      secret,
		ServiceToken:   config.Env("OBJ_GATEWAY_TOKEN", ""),
		Bucket:         bucket,
		PublicBase:     cdn,
		CDNBase:        cdn,
	})
	if err != nil {
		blog.Fatalf("obj-gateway (%s): %v", bucket, err)
	}
	return c
}
