package memprovider

import (
	"context"
	"testing"

	"github.com/SolaTyolo/17track-wrapper/pkg/event"
)

func TestPublishDedupesOnlyWithKey(t *testing.T) {
	p := New()
	ch, err := event.Channel(event.SourceInternal, "test_dedupe")
	if err != nil {
		t.Fatal(err)
	}
	scope := event.InternalBiz("C000001")
	ctx := context.Background()
	body := map[string]string{"action": "changed"}

	for i := 0; i < 2; i++ {
		if err := p.Publish(ctx, ch, scope, body); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.Published) != 2 {
		t.Fatalf("identical payloads without key: got %d publishes, want 2", len(p.Published))
	}

	keyed := event.WithDedupeKey(ctx, "outbox:1")
	for i := 0; i < 2; i++ {
		if err := p.Publish(keyed, ch, scope, body); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.Published) != 3 {
		t.Fatalf("same dedupe key: got %d publishes, want 3", len(p.Published))
	}
}
