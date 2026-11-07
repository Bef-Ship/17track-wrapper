package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/olap"
)

func TestTenantIsolation(t *testing.T) {
	svc := NewTracking(newMemStore(), olap.Noop{}, nil, 90, 0)
	ctx := context.Background()
	for _, tenant := range []string{"a", "b"} {
		if _, err := svc.RegisterPackage(ctx, &domain.Package{
			TenantID: tenant, TrackingNumber: "SAME", CarrierCode: "ups",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.IngestBatch(ctx, domain.EventBatch{
		TenantID: "a", TrackingNumber: "SAME", Status: domain.StatusInTransit, UpdateTime: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := svc.GetPackageEvents(ctx, "a", "SAME")
	b, _, _ := svc.GetPackageEvents(ctx, "b", "SAME")
	if a.Status != domain.StatusInTransit || b.Status != domain.StatusRegistered {
		t.Fatalf("update leaked across tenants: a=%s b=%s", a.Status, b.Status)
	}
	if err := svc.IngestBatch(ctx, domain.EventBatch{TenantID: "c", TrackingNumber: "SAME"}); err == nil {
		t.Fatal("unknown tenant must not find the package")
	}
}

func TestArchiveRunsAllBatches(t *testing.T) {
	st := newMemStore()
	svc := NewTracking(st, olap.Noop{}, newMemCold(), 90, 2)
	ctx := context.Background()
	delivered := time.Now().UTC().Add(-100 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		if _, err := svc.RegisterPackage(ctx, &domain.Package{
			TrackingNumber: fmt.Sprintf("TN%d", i), CarrierCode: "ups", DeliveredAt: &delivered,
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := svc.ArchiveDuePackages(ctx)
	if err != nil || n != 5 {
		t.Fatalf("archived=%d err=%v, want all 5 across batches of 2", n, err)
	}
}

func TestArchiveSkipsWhenLocked(t *testing.T) {
	st := newMemStore()
	svc := NewTracking(st, olap.Noop{}, newMemCold(), 90, 0)
	st.lock.Lock()
	defer st.lock.Unlock()
	if _, err := svc.ArchiveDuePackages(context.Background()); !errors.Is(err, ErrJobRunning) {
		t.Fatalf("err=%v want ErrJobRunning", err)
	}
}

func TestNextDaily(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ now, want string }{
		{"2026-11-07T02:00:00+08:00", "2026-11-07T03:00:00+08:00"},
		{"2026-11-07T03:00:00+08:00", "2026-11-08T03:00:00+08:00"},
		// 20:00 UTC is already 04:00 on Nov 8 in Shanghai, so the next run is Nov 9.
		{"2026-11-07T20:00:00Z", "2026-11-09T03:00:00+08:00"},
	}
	for _, c := range cases {
		now, _ := time.Parse(time.RFC3339, c.now)
		want, _ := time.Parse(time.RFC3339, c.want)
		if got := nextDaily(now, 3, 0, loc); !got.Equal(want) {
			t.Errorf("nextDaily(%s)=%s want %s", c.now, got, want)
		}
	}
}
