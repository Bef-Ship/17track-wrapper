package service

import (
	"testing"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
)

func TestApplyBatchMerge(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pkg := &domain.Package{
		CarrierCode:    "ups",
		Status:         domain.StatusRegistered,
		ShippingDetail: domain.ShippingDetail{Origin: domain.Address{Country: "CN", City: "Shenzhen"}},
	}

	applyBatch(pkg, domain.EventBatch{
		UpdateTime: t0.Add(time.Hour),
		Status:     domain.StatusInTransit,
		SubStatus:  "in_transit_picked_up",
		ShippingDetail: domain.ShippingDetail{
			Origin:      domain.Address{PostalCode: "518000"},
			Destination: domain.Address{Country: "US"},
		},
		Events: []domain.TrackingEvent{
			{EventTime: t0.Add(30 * time.Minute), Stage: domain.StagePickedUp, Description: "late scan"},
			{EventTime: t0, Stage: domain.StagePickedUp, Description: "picked up", Location: "SZ"},
		},
	})

	if pkg.Status != domain.StatusInTransit || pkg.SubStatus != "in_transit_picked_up" {
		t.Fatalf("status=%s/%s", pkg.Status, pkg.SubStatus)
	}
	if pkg.Origin.City != "Shenzhen" || pkg.Origin.PostalCode != "518000" || pkg.Destination.Country != "US" {
		t.Fatalf("addresses not merged: %+v / %+v", pkg.Origin, pkg.Destination)
	}
	if pkg.PickedUpAt == nil || !pkg.PickedUpAt.Equal(t0) {
		t.Fatalf("picked_up_at=%v, want earliest staged event", pkg.PickedUpAt)
	}
	if pkg.LatestEventDesc != "late scan" {
		t.Fatalf("latest event=%q", pkg.LatestEventDesc)
	}

	// Delivered milestone without explicit status flips status and sets delivered_at.
	deliveredAt := t0.Add(48 * time.Hour)
	applyBatch(pkg, domain.EventBatch{
		UpdateTime: deliveredAt,
		Milestones: domain.Milestones{domain.StageDelivered: deliveredAt},
	})
	if pkg.Status != domain.StatusDelivered || pkg.DeliveredAt == nil || !pkg.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("status=%s delivered_at=%v", pkg.Status, pkg.DeliveredAt)
	}
	if !pkg.PickedUpAt.Equal(t0) || pkg.Destination.Country != "US" {
		t.Fatal("empty batch fields must not clear existing values")
	}
	if pkg.SubStatus != "" {
		t.Fatalf("sub_status %q left over from in_transit", pkg.SubStatus)
	}
}

func TestApplyBatchDeliveredFallback(t *testing.T) {
	ut := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	pkg := &domain.Package{}
	applyBatch(pkg, domain.EventBatch{UpdateTime: ut, Status: domain.StatusDelivered})
	if pkg.DeliveredAt == nil || !pkg.DeliveredAt.Equal(ut) {
		t.Fatalf("delivered_at=%v, want update time fallback", pkg.DeliveredAt)
	}
}
