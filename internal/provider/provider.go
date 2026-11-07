// Package provider parses the generic tracking payload accepted by
// POST /v1/tracking-events into domain.EventBatch. Receiving vendor webhooks
// and converting them to this format is the caller's job.
package provider

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
)

// Payload is one tracking update. All times are RFC3339; omitted fields mean "unchanged".
//
//	{
//	  "tracking_number": "1Z999", "carrier_code": "ups", "update_time": "...",
//	  "status": "in_transit", "sub_status": "in_transit_customs_processing", "sub_status_desc": "...",
//	  "origin": {"country": "CN", "state": "GD", "city": "Shenzhen", "postal_code": "518000"},
//	  "destination": {"country": "US", ...},
//	  "service_type": "Express", "pieces": 1, "reference_number": "...",
//	  "last_mile": {"tracking_number": "...", "carrier_code": "usps"},
//	  "estimated_delivery": {"from": "...", "to": "..."},
//	  "milestones": [{"stage": "picked_up", "time": "..."}],
//	  "events": [{
//	    "event_time": "...", "carrier_code": "ups", "stage": "picked_up", "sub_status": "...",
//	    "status_code": "PU", "description": "...", "location": "...",
//	    "address": {"country": "CN", "city": "Shenzhen"}, "raw": {}
//	  }]
//	}
//
// A request may also be {"batches": [Payload, ...]} for multiple tracking numbers.
type Payload struct {
	TrackingNumber    string          `json:"tracking_number"`
	CarrierCode       string          `json:"carrier_code"`
	UpdateTime        string          `json:"update_time"`
	Status            string          `json:"status"`
	SubStatus         string          `json:"sub_status"`
	SubStatusDesc     string          `json:"sub_status_desc"`
	Origin            domain.Address  `json:"origin"`
	Destination       domain.Address  `json:"destination"`
	ServiceType       string          `json:"service_type"`
	Pieces            *int            `json:"pieces"`
	ReferenceNumber   string          `json:"reference_number"`
	LastMile          wireLastMile    `json:"last_mile"`
	EstimatedDelivery wireTimeRange   `json:"estimated_delivery"`
	Milestones        []wireMilestone `json:"milestones"`
	Events            []wireEvent     `json:"events"`
}

type wireLastMile struct {
	TrackingNumber string `json:"tracking_number"`
	CarrierCode    string `json:"carrier_code"`
}

type wireTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type wireMilestone struct {
	Stage string `json:"stage"`
	Time  string `json:"time"`
}

type wireEvent struct {
	EventTime   string          `json:"event_time"`
	CarrierCode string          `json:"carrier_code"`
	Stage       string          `json:"stage"`
	SubStatus   string          `json:"sub_status"`
	StatusCode  string          `json:"status_code"`
	Description string          `json:"description"`
	Location    string          `json:"location"`
	Address     domain.Address  `json:"address"`
	Raw         json.RawMessage `json:"raw"`
}

// Parse turns a request body into one EventBatch per tracking number.
func Parse(body []byte) ([]domain.EventBatch, error) {
	var root struct {
		Batches []json.RawMessage `json:"batches"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	raws := root.Batches
	if len(raws) == 0 {
		raws = []json.RawMessage{body}
	}
	out := make([]domain.EventBatch, 0, len(raws))
	for i, raw := range raws {
		var p Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("batches[%d]: %w", i, err)
		}
		batch, err := toBatch(p)
		if err != nil {
			if len(root.Batches) > 0 {
				return nil, fmt.Errorf("batches[%d]: %w", i, err)
			}
			return nil, err
		}
		out = append(out, batch)
	}
	return out, nil
}

func toBatch(p Payload) (domain.EventBatch, error) {
	if p.TrackingNumber == "" {
		return domain.EventBatch{}, fmt.Errorf("tracking_number required")
	}
	if p.Status != "" && !domain.IsValidStatus(p.Status) {
		return domain.EventBatch{}, fmt.Errorf("status: unknown value %q", p.Status)
	}

	var updateTime time.Time
	if p.UpdateTime != "" {
		t, err := parseTime(p.UpdateTime)
		if err != nil {
			return domain.EventBatch{}, fmt.Errorf("update_time: %w", err)
		}
		updateTime = t
	}
	etaFrom, err := parseOptionalTime(p.EstimatedDelivery.From)
	if err != nil {
		return domain.EventBatch{}, fmt.Errorf("estimated_delivery.from: %w", err)
	}
	etaTo, err := parseOptionalTime(p.EstimatedDelivery.To)
	if err != nil {
		return domain.EventBatch{}, fmt.Errorf("estimated_delivery.to: %w", err)
	}

	var milestones domain.Milestones
	for i, m := range p.Milestones {
		if !domain.IsValidStage(m.Stage) {
			return domain.EventBatch{}, fmt.Errorf("milestones[%d].stage: unknown value %q", i, m.Stage)
		}
		t, err := parseTime(m.Time)
		if err != nil {
			return domain.EventBatch{}, fmt.Errorf("milestones[%d].time: %w", i, err)
		}
		if milestones == nil {
			milestones = domain.Milestones{}
		}
		milestones[m.Stage] = t
	}

	events := make([]domain.TrackingEvent, 0, len(p.Events))
	for i, e := range p.Events {
		et, err := parseTime(e.EventTime)
		if err != nil {
			return domain.EventBatch{}, fmt.Errorf("events[%d].event_time: %w", i, err)
		}
		if e.Stage != "" && !domain.IsValidStage(e.Stage) {
			return domain.EventBatch{}, fmt.Errorf("events[%d].stage: unknown value %q", i, e.Stage)
		}
		events = append(events, domain.TrackingEvent{
			TrackingNumber:   p.TrackingNumber,
			EventTime:        et,
			CarrierCode:      e.CarrierCode,
			Stage:            e.Stage,
			SubStatus:        e.SubStatus,
			StatusCode:       e.StatusCode,
			Description:      e.Description,
			Location:         e.Location,
			Address:          e.Address,
			RawJSON:          e.Raw,
			EventFingerprint: domain.Fingerprint(p.TrackingNumber, et, e.Location, e.Description, e.StatusCode),
		})
	}

	return domain.EventBatch{
		TrackingNumber: p.TrackingNumber,
		CarrierCode:    p.CarrierCode,
		UpdateTime:     updateTime,
		Status:         p.Status,
		SubStatus:      p.SubStatus,
		SubStatusDesc:  p.SubStatusDesc,
		ShippingDetail: domain.ShippingDetail{
			Origin:                 p.Origin,
			Destination:            p.Destination,
			ServiceType:            p.ServiceType,
			Pieces:                 p.Pieces,
			ReferenceNumber:        p.ReferenceNumber,
			LastMileTrackingNumber: p.LastMile.TrackingNumber,
			LastMileCarrierCode:    p.LastMile.CarrierCode,
			EstimatedDeliveryFrom:  etaFrom,
			EstimatedDeliveryTo:    etaTo,
		},
		Milestones: milestones,
		Events:     events,
	}, nil
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func parseOptionalTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := parseTime(s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
