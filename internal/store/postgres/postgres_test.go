package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

// Runs against a real database only when TEST_DATABASE_URL is set, e.g.
// TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable'
func TestPackageAndEventRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	tn := fmt.Sprintf("IT-%d", time.Now().UnixNano())
	pieces := 2
	pkg := &domain.Package{
		TrackingNumber: tn,
		CarrierCode:    "ups",
		ShippingDetail: domain.ShippingDetail{
			Origin: domain.Address{Country: "CN", City: "Shenzhen"},
			Pieces: &pieces,
		},
	}
	if err := st.CreatePackage(ctx, pkg); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 11, 7, 1, 2, 3, 0, time.UTC)
	pkg.Status = domain.StatusDelivered
	pkg.SubStatus = "delivered_other"
	pkg.Milestones = domain.Milestones{domain.StagePickedUp: at, domain.StageDelivered: at.Add(time.Hour)}
	pkg.DeliveredAt = &at
	pkg.Destination.Country = "US"
	if err := st.UpdatePackage(ctx, pkg); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetPackageByTrackingNumber(ctx, domain.DefaultTenant, tn)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusDelivered || got.SubStatus != "delivered_other" ||
		got.Origin.City != "Shenzhen" || got.Destination.Country != "US" ||
		got.Pieces == nil || *got.Pieces != 2 || len(got.Milestones) != 2 ||
		!got.Milestones[domain.StagePickedUp].Equal(at) {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	ev := domain.TrackingEvent{
		PackageID:        pkg.PackageID,
		TrackingNumber:   tn,
		EventTime:        at,
		CarrierCode:      "ups",
		Stage:            domain.StagePickedUp,
		Description:      "picked up",
		Address:          domain.Address{Country: "CN", City: "Shenzhen"},
		EventFingerprint: domain.Fingerprint(tn, at, "", "picked up", ""),
	}
	ins, err := st.InsertEvents(ctx, []domain.TrackingEvent{ev, ev})
	if err != nil || len(ins) != 1 || ins[0].EventID == 0 {
		t.Fatalf("inserted=%+v err=%v, want 1 (duplicate fingerprint skipped)", ins, err)
	}
	if again, err := st.InsertEvents(ctx, []domain.TrackingEvent{ev}); err != nil || len(again) != 0 {
		t.Fatalf("replay inserted=%d err=%v, want 0", len(again), err)
	}
	events, err := st.ListEventsByPackage(ctx, pkg.PackageID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	if events[0].Stage != domain.StagePickedUp || events[0].Address.City != "Shenzhen" {
		t.Fatalf("event mismatch: %+v", events[0])
	}

	// Same tracking number in another tenant is a separate package with its own events.
	other := &domain.Package{TenantID: "tenant-b", TrackingNumber: tn, CarrierCode: "dhl"}
	if err := st.CreatePackage(ctx, other); err != nil {
		t.Fatalf("same number in other tenant: %v", err)
	}
	ev.PackageID = other.PackageID
	if ins, err := st.InsertEvents(ctx, []domain.TrackingEvent{ev}); err != nil || len(ins) != 1 {
		t.Fatalf("other tenant event inserted=%d err=%v", len(ins), err)
	}
	if _, err := st.GetPackageByTrackingNumber(ctx, "tenant-c", tn); err == nil {
		t.Fatal("lookup must be tenant-scoped")
	}

	page, err := st.ListPackages(ctx, store.PackageFilter{
		TenantID: "tenant-b", Statuses: []string{domain.StatusRegistered}, Limit: 10,
	})
	if err != nil || len(page) == 0 || page[0].TenantID != "tenant-b" {
		t.Fatalf("list=%+v err=%v", page, err)
	}
	next, err := st.ListPackages(ctx, store.PackageFilter{TenantID: "tenant-b", BeforeID: page[0].PackageID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range next {
		if p.PackageID >= page[0].PackageID {
			t.Fatalf("cursor not applied: %d >= %d", p.PackageID, page[0].PackageID)
		}
	}

	release, ok, err := st.TryLock(ctx, "it-lock")
	if err != nil || !ok {
		t.Fatalf("lock ok=%v err=%v", ok, err)
	}
	if _, ok2, _ := st.TryLock(ctx, "it-lock"); ok2 {
		t.Fatal("second TryLock must fail while held")
	}
	release()
	release2, ok, err := st.TryLock(ctx, "it-lock")
	if err != nil || !ok {
		t.Fatalf("relock ok=%v err=%v", ok, err)
	}
	release2()

	// A failing transaction leaves no trace.
	errBoom := errors.New("boom")
	err = st.WithTx(ctx, func(tx store.Store) error {
		p, err := tx.GetPackageForUpdate(ctx, domain.DefaultTenant, tn)
		if err != nil {
			return err
		}
		p.Status = domain.StatusException
		if err := tx.UpdatePackage(ctx, p); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("WithTx err=%v", err)
	}
	if got, _ := st.GetPackageByTrackingNumber(ctx, domain.DefaultTenant, tn); got.Status != domain.StatusDelivered {
		t.Fatalf("rollback failed, status=%s", got.Status)
	}

	// GetPackageForUpdate blocks other writers until the holder commits.
	locked, unlock := make(chan struct{}), make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		holder <- st.WithTx(ctx, func(tx store.Store) error {
			if _, err := tx.GetPackageForUpdate(ctx, domain.DefaultTenant, tn); err != nil {
				return err
			}
			close(locked)
			<-unlock
			return nil
		})
	}()
	<-locked
	tctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	err = st.WithTx(tctx, func(tx store.Store) error {
		_, err := tx.GetPackageForUpdate(tctx, domain.DefaultTenant, tn)
		return err
	})
	cancel()
	close(unlock)
	if err == nil {
		t.Fatal("second GetPackageForUpdate must wait for the row lock")
	}
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
}
