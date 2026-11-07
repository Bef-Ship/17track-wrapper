package provider

import (
	"testing"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
)

func TestParseSingle(t *testing.T) {
	body := []byte(`{
	  "tracking_number": "TN9",
	  "carrier_code": "ups",
	  "update_time": "2026-11-07T08:00:00Z",
	  "status": "in_transit",
	  "sub_status": "in_transit_picked_up",
	  "origin": {"country": "CN", "city": "Shenzhen"},
	  "destination": {"country": "US"},
	  "pieces": 2,
	  "last_mile": {"tracking_number": "LM1", "carrier_code": "usps"},
	  "estimated_delivery": {"from": "2026-11-10T00:00:00Z", "to": "2026-11-12T00:00:00Z"},
	  "milestones": [{"stage": "picked_up", "time": "2026-11-07T07:00:00+08:00"}],
	  "events": [
	    {"event_time": "2026-11-07T07:00:00Z", "carrier_code": "ups", "stage": "picked_up",
	     "location": "A", "description": "scan", "status_code": "S", "address": {"country": "CN"}}
	  ]
	}`)
	batches, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0].Events) != 1 {
		t.Fatalf("%+v", batches)
	}
	b := batches[0]
	ev := b.Events[0]
	if ev.EventFingerprint == "" || ev.Stage != domain.StagePickedUp || ev.Address.Country != "CN" {
		t.Fatalf("event=%+v", ev)
	}
	if !ev.EventTime.Equal(time.Date(2026, 11, 7, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("time=%v", ev.EventTime)
	}
	pickedUp := b.Milestones[domain.StagePickedUp]
	if !pickedUp.Equal(time.Date(2026, 11, 6, 23, 0, 0, 0, time.UTC)) {
		t.Fatalf("picked_up milestone=%v", pickedUp)
	}
	d := b.ShippingDetail
	if d.Origin.City != "Shenzhen" || d.Destination.Country != "US" || d.Pieces == nil || *d.Pieces != 2 ||
		d.LastMileTrackingNumber != "LM1" || d.EstimatedDeliveryTo == nil {
		t.Fatalf("shipping detail=%+v", d)
	}
}

func TestParseBatches(t *testing.T) {
	body := []byte(`{"batches": [
	  {"tracking_number": "A1"},
	  {"tracking_number": "B2", "status": "delivered"}
	]}`)
	batches, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || batches[1].Status != domain.StatusDelivered {
		t.Fatalf("%+v", batches)
	}
	if !batches[0].UpdateTime.IsZero() {
		t.Fatalf("omitted update_time must stay zero, got %v", batches[0].UpdateTime)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"invalid json":      `{`,
		"missing number":    `{"carrier_code": "ups"}`,
		"unknown status":    `{"tracking_number": "T", "status": "flying"}`,
		"unknown stage":     `{"tracking_number": "T", "milestones": [{"stage": "x", "time": "2026-11-07T00:00:00Z"}]}`,
		"bad event time":    `{"tracking_number": "T", "events": [{"event_time": "yesterday"}]}`,
		"bad batch element": `{"batches": [{"tracking_number": ""}]}`,
	}
	for name, body := range cases {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
