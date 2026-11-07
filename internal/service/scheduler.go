package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RunArchiveSchedule runs ArchiveDuePackages every day at hour:minute in loc
// until ctx is cancelled. Every replica may run the schedule; the job lock
// lets only one of them archive at a time.
func (t *Tracking) RunArchiveSchedule(ctx context.Context, hour, minute int, loc *time.Location) {
	for {
		next := nextDaily(time.Now(), hour, minute, loc)
		slog.InfoContext(ctx, "archive job scheduled", "next_run", next)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		start := time.Now()
		n, err := t.ArchiveDuePackages(ctx)
		switch {
		case errors.Is(err, ErrJobRunning):
			slog.InfoContext(ctx, "archive job skipped: running on another instance")
		case err != nil:
			slog.ErrorContext(ctx, "scheduled archive job failed", "archived", n, "err", err)
		default:
			slog.InfoContext(ctx, "scheduled archive job done", "archived", n, "took", time.Since(start))
		}
	}
}

// nextDaily returns the first hour:minute in loc strictly after now.
func nextDaily(now time.Time, hour, minute int, loc *time.Location) time.Time {
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !next.After(local) {
		next = time.Date(local.Year(), local.Month(), local.Day()+1, hour, minute, 0, 0, loc)
	}
	return next
}
