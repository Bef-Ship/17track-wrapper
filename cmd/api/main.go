package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/SolaTyolo/tracking-service/internal/archive"
	"github.com/SolaTyolo/tracking-service/internal/config"
	"github.com/SolaTyolo/tracking-service/internal/olap"
	"github.com/SolaTyolo/tracking-service/internal/reqid"
	"github.com/SolaTyolo/tracking-service/internal/server"
	"github.com/SolaTyolo/tracking-service/internal/service"
	"github.com/SolaTyolo/tracking-service/internal/store/postgres"
)

func main() {
	cfg := config.Load()
	setupLogger(cfg.LogLevel)
	if err := run(cfg); err != nil {
		slog.Error("tracking-service exited", "err", err)
		os.Exit(1)
	}
	slog.Info("tracking-service stopped")
}

// run blocks until SIGINT/SIGTERM, then drains HTTP, waits for the archive
// scheduler and closes connections, in that order.
func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database open: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("postgres ready")

	olapWriter, err := olap.Open(cfg)
	if err != nil {
		return fmt.Errorf("olap open: %w", err)
	}
	defer olapWriter.Close()
	if cfg.OLAPEnabled {
		if err := olapWriter.EnsureSchema(ctx); err != nil {
			return fmt.Errorf("olap schema: %w", err)
		}
	}

	var cold archive.ColdStore
	if cfg.ArchiveEnabled {
		s3Store, err := archive.NewS3(cfg.S3)
		if err != nil {
			return fmt.Errorf("archive open: %w", err)
		}
		cold = s3Store
		slog.Info("archive ready", "backend", cold.Name())
	}

	tracking := service.NewTracking(st, olapWriter, cold, cfg.ArchiveRetentionDays, cfg.ArchiveBatchSize)

	var jobs sync.WaitGroup
	defer jobs.Wait()
	defer stop() // runs before jobs.Wait, so the scheduler sees cancellation
	if cfg.ArchiveEnabled && cfg.ArchiveSchedule != "off" {
		at, err := time.Parse("15:04", cfg.ArchiveSchedule)
		if err != nil {
			return fmt.Errorf("invalid ARCHIVE_SCHEDULE %q, expect HH:MM or off", cfg.ArchiveSchedule)
		}
		loc, err := time.LoadLocation(cfg.ArchiveTimezone)
		if err != nil {
			return fmt.Errorf("invalid ARCHIVE_TIMEZONE %q: %w", cfg.ArchiveTimezone, err)
		}
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			tracking.RunArchiveSchedule(ctx, at.Hour(), at.Minute(), loc)
		}()
	}

	if cfg.APIKey == "" {
		slog.Warn("API_KEY not set: /v1 endpoints are unauthenticated")
	}
	return server.New(tracking, server.Options{
		OLAPName:  olapWriter.Name(),
		OLAPOn:    cfg.OLAPEnabled,
		ArchiveOn: cfg.ArchiveEnabled,
		APIKey:    cfg.APIKey,
	}).Run(ctx, cfg.HTTPAddr)
}

func setupLogger(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	slog.SetDefault(slog.New(reqid.Handler{Handler: h}))
}
