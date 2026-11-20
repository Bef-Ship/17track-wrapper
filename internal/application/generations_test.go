package application

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
)

type memCold struct {
	objs map[string]domain.ArchiveSnapshot
}

func (c *memCold) Name() string { return "mem" }

func (c *memCold) Put(_ context.Context, key string, snap domain.ArchiveSnapshot) error {
	c.objs[key] = snap
	return nil
}

func (c *memCold) Get(_ context.Context, key string) (*domain.ArchiveSnapshot, error) {
	snap, ok := c.objs[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &snap, nil
}

// push sends a 17TRACK TRACKING_UPDATED push with one event per (time, lat, lng) triple.
func push(t *testing.T, svc *Service, tn, seq string, events ...[3]string) {
	t.Helper()
	evs := make([]map[string]any, 0, len(events))
	for _, e := range events {
		evs = append(evs, map[string]any{
			"time_iso":    e[0],
			"description": "scan " + e[0],
			"stage":       "InTransit",
			"address":     map[string]any{"coordinates": map[string]any{"latitude": e[1], "longitude": e[2]}},
		})
	}
	raw, _ := json.Marshal(map[string]any{"number": tn, "track_info": map[string]any{
		"latest_status": map[string]any{"status": "InTransit"},
		"tracking":      map[string]any{"providers": []any{map[string]any{"events": evs}}},
	}})
	if err := svc.ApplyWebhook(context.Background(), domain.WebhookApplyInput{
		TrackingNo: tn, Raw: raw, Event: domain.EventTrackingUpdated, CallbackSeq: seq,
	}); err != nil {
		t.Fatalf("push %s: %v", seq, err)
	}
}

func TestRegisterBatchesUpstreamAndStoresParams(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	items := make([]domain.RegisterItemInput, domain.MaxBatch)
	for i := range items {
		items[i] = domain.RegisterItemInput{RegisterItem: sdk.RegisterItem{
			Number: fmt.Sprintf("TN%02d", i), Carrier: 100002, Lang: "en", OrderNo: "SO-1", DestinationCountry: "US",
		}}
	}
	res, err := svc.Register(context.Background(), domain.RegisterInput{Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.registerCalls) != 1 || len(fs.registerCalls[0]) != domain.MaxBatch {
		t.Fatalf("register calls = %d", len(fs.registerCalls))
	}
	if sent := fs.registerCalls[0][0]; sent.Lang != "en" || sent.OrderNo != "SO-1" || sent.DestinationCountry != "US" {
		t.Fatalf("17TRACK params not forwarded: %+v", sent)
	}
	if len(res.Accepted) != domain.MaxBatch || len(res.Rejected) != 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, r := range res.Accepted {
		if r.SubscriptionStatus != domain.SubTracking {
			t.Fatalf("result = %+v", r)
		}
	}
	if p := repo.pkgs[res.Accepted[0].PackageID].RegisterParams; p.OrderNo != "SO-1" {
		t.Fatalf("register_params not stored: %+v", p)
	}

	_, err = svc.Register(context.Background(), domain.RegisterInput{Items: append(items, items[0])})
	if b := domain.Body(err); b.Code != domain.CodeTooManyNumbers {
		t.Fatalf("41 items: %+v", b)
	}
}

// only returns the single accepted or rejected item of res.
func only(t *testing.T, res domain.BatchResult, err error) domain.ItemResult {
	t.Helper()
	if err != nil || len(res.Accepted)+len(res.Rejected) != 1 {
		t.Fatalf("result = %+v err=%v", res, err)
	}
	if len(res.Accepted) == 1 {
		return res.Accepted[0]
	}
	return res.Rejected[0]
}

func TestRecycledNumberStartsNewGeneration(t *testing.T) {
	svc, repo, fs, _ := newTestService(t)
	ctx := context.Background()
	register := func(ref string) domain.ItemResult {
		in := item("TN-RE", 100002)
		in.ExternalRef = ref
		res, err := svc.Register(ctx, domain.RegisterInput{Items: []domain.RegisterItemInput{in}})
		return only(t, res, err)
	}
	first := register("order-A")
	push(t, svc, "TN-RE", "a1", [3]string{"2026-01-01T00:00:00Z", "1", "2"})

	// Still in transit: the same number for another order is rejected like 17TRACK does.
	again := register("order-B")
	if again.PackageID != first.PackageID || again.Error == nil || again.Error.Code != domain.CodeAlreadyRegistered {
		t.Fatalf("in-flight package recycled: %+v", again)
	}

	old := repo.pkgs[first.PackageID]
	old.Status = domain.StatusDelivered
	old.SubscriptionStatus = domain.SubStopped

	if same := register("order-A"); same.PackageID != first.PackageID {
		t.Fatalf("same order re-register created a generation: %+v", same)
	}
	second := register("order-B")
	if second.PackageID == first.PackageID || second.Error != nil || second.SubscriptionStatus != domain.SubTracking {
		t.Fatalf("expected new generation: %+v", second)
	}
	if repo.pkgs[first.PackageID].SupersededAt == nil {
		t.Fatal("old generation not superseded")
	}
	if len(fs.deleted) != 1 || fs.deleted[0].Number != "TN-RE" {
		t.Fatalf("old 17TRACK registration not deleted: %+v", fs.deleted)
	}

	push(t, svc, "TN-RE", "b1", [3]string{"2026-06-01T00:00:00Z", "3", "4"})
	if n := len(repo.events[second.PackageID]); n != 1 {
		t.Fatalf("new generation events = %d", n)
	}
	if n := len(repo.events[first.PackageID]); n != 1 {
		t.Fatalf("old generation events changed: %d", n)
	}

	h, err := svc.History(ctx, "TN-RE", true)
	if err != nil || len(h.Generations) != 2 {
		t.Fatalf("history = %+v %v", h, err)
	}
	if h.Generations[0].Package.PackageID != second.PackageID || h.Generations[1].Package.ExternalRef != "order-A" ||
		len(h.Generations[1].Events) != 1 {
		t.Fatalf("history order/content = %+v", h.Generations)
	}

	v, err := svc.GetPackageEvents(ctx, domain.PackageRef{Number: "TN-RE", ExternalRef: "order-A"})
	if err != nil || v.Package.PackageID != first.PackageID {
		t.Fatalf("lookup by external_ref = %+v %v", v.Package, err)
	}
	_, err = svc.GetPackageEvents(ctx, domain.PackageRef{Number: "TN-RE", ExternalRef: "order-X"})
	if b := domain.Body(err); b.Code != domain.CodeNotRegistered {
		t.Fatalf("unknown external_ref = %+v", b)
	}
}

func TestArchiveRestoreAndColdReads(t *testing.T) {
	repo, fs, cold := newMemRepo(), &fakeSDK{}, &memCold{objs: map[string]domain.ArchiveSnapshot{}}
	svc := NewService(repo, fs, nil, cold, Options{})
	ctx := context.Background()
	id := registerOne(t, svc, "TN-AR")
	push(t, svc, "TN-AR", "p1",
		[3]string{"2026-01-01T00:00:00Z", "31.2", "121.4"},
		[3]string{"2026-01-02T00:00:00Z", "", ""},
		[3]string{"2026-01-03T00:00:00Z", "40.7", "-74.0"})

	if n, err := svc.ArchiveDuePackages(ctx); err != nil || n != 0 {
		t.Fatalf("active package archived: %d %v", n, err)
	}
	pkg := repo.pkgs[id]
	pkg.SubscriptionStatus = domain.SubStopped
	pkg.UpdatedAt = time.Now().Add(-(DefaultArchiveRetentionDays + 1) * 24 * time.Hour)

	if n, err := svc.ArchiveDuePackages(ctx); err != nil || n != 1 {
		t.Fatalf("archive = %d %v", n, err)
	}
	if !repo.pkgs[id].IsArchived || len(repo.events[id]) != 0 {
		t.Fatalf("not archived: %+v events=%d", repo.pkgs[id], len(repo.events[id]))
	}
	if want := fmt.Sprintf("tracking-events/TN-AR/%d.json", id); repo.pkgs[id].ArchiveKey != want {
		t.Fatalf("archive key = %s, want %s", repo.pkgs[id].ArchiveKey, want)
	}

	v, err := svc.GetPackageEvents(ctx, domain.PackageRef{PackageID: id})
	if err != nil || !v.Cold || len(v.Events) != 3 {
		t.Fatalf("cold read = %+v %v", v, err)
	}
	route, err := svc.GetRoute(ctx, domain.PackageRef{Number: "TN-AR"})
	if err != nil || len(route.Features) != 1 {
		t.Fatalf("route = %+v %v", route, err)
	}
	if c := route.Features[0].Geometry.Coordinates; len(c) != 2 || c[0][0] != 121.4 || c[1][1] != 40.7 {
		t.Fatalf("route coords = %v", c)
	}

	restored, err := svc.Restore(ctx, id)
	if err != nil || restored.IsArchived || len(repo.events[id]) != 3 {
		t.Fatalf("restore = %+v %v events=%d", restored, err, len(repo.events[id]))
	}
	if time.Since(restored.UpdatedAt) > time.Minute {
		t.Fatal("restore should restart the retention clock")
	}
	if n, _ := svc.ArchiveDuePackages(ctx); n != 0 {
		t.Fatal("restored package re-archived immediately")
	}
}

func TestWebhookNotifiesOnlyOnChange(t *testing.T) {
	svc, _, _, obs := newTestService(t)
	registerOne(t, svc, "TN-N")
	ev1 := [3]string{"2026-01-01T00:00:00Z", "", ""}

	push(t, svc, "TN-N", "n1", ev1)
	if len(obs.updates) != 1 || len(obs.updates[0].NewEvents) != 1 {
		t.Fatalf("first push = %+v", obs.updates)
	}
	push(t, svc, "TN-N", "n2", ev1)
	if len(obs.updates) != 1 {
		t.Fatalf("unchanged push notified: %d", len(obs.updates))
	}
	push(t, svc, "TN-N", "n3", ev1, [3]string{"2026-01-02T00:00:00Z", "", ""})
	if len(obs.updates) != 2 || len(obs.updates[1].NewEvents) != 1 {
		t.Fatalf("new event push = %+v", obs.updates)
	}
}
