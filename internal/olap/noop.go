package olap

import (
	"context"
	"log/slog"
)

// Noop is used when OLAP_ENABLED=false.
type Noop struct{}

func (Noop) Name() string { return "noop" }

func (Noop) EnsureSchema(context.Context) error { return nil }

func (n Noop) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	slog.DebugContext(ctx, "olap noop skip insert", "count", len(events))
	return nil
}

func (Noop) Close() error { return nil }
