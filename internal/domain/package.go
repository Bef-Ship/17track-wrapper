package domain

import (
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"
)

// Package is one generation of a tracking number: logistics state + 17TRACK
// subscription. Carriers recycle numbers, so only the current generation
// (SupersededAt == nil) is unique per tracking_number; older ones are history.
type Package struct {
	PackageID      int64  `json:"package_id"`
	TrackingNumber string `json:"tracking_number"`
	// Carrier is the 17TRACK carrier key (0 = auto-detect, not yet resolved).
	Carrier     int      `json:"carrier"`
	ExternalRef string   `json:"external_ref,omitempty"`
	Weight      *float64 `json:"weight,omitempty"`

	Status        string `json:"status,omitempty"`
	SubStatus     string `json:"sub_status,omitempty"`
	SubStatusDesc string `json:"sub_status_desc,omitempty"`

	ShippingDetail

	Milestones  Milestones `json:"milestones,omitempty"`
	PickedUpAt  *time.Time `json:"picked_up_at,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`

	LatestEventTime     *time.Time `json:"latest_event_time,omitempty"`
	LatestEventDesc     string     `json:"latest_event_desc,omitempty"`
	LatestEventLocation string     `json:"latest_event_location,omitempty"`

	IsArchived     bool       `json:"is_archived"`
	ArchiveKey     string     `json:"archive_key,omitempty"`
	LatestUpdateAt *time.Time `json:"latest_update_at,omitempty"`

	// Subscription control (17TRACK).
	Tag                *string    `json:"tag,omitempty"`
	SubscriptionStatus string     `json:"subscription_status"`
	Source             string     `json:"source"`
	IsRetracked        bool       `json:"is_retracked"`
	CancelledAt        *time.Time `json:"cancelled_at,omitempty"`
	// RegisterParams is the last /register request, replayed on retries.
	RegisterParams sdk.RegisterItem `json:"register_params"`
	// SupersededAt is set once a newer generation of the number was registered.
	SupersededAt *time.Time `json:"superseded_at,omitempty"`

	// Return detection.
	HasReturn             bool       `json:"has_return"`
	ReturnType            *string    `json:"return_type,omitempty"`
	ReturnDetectSupported bool       `json:"return_detect_supported"`
	ReturnDetectedAt      *time.Time `json:"return_detected_at,omitempty"`
	ReturnCarrierEventID  *string    `json:"return_carrier_event_id,omitempty"`
	LastCallbackAt        *time.Time `json:"last_callback_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ShippingDetail is route / service metadata.
type ShippingDetail struct {
	Origin                 Address    `json:"origin"`
	Destination            Address    `json:"destination"`
	ServiceType            string     `json:"service_type,omitempty"`
	Pieces                 *int       `json:"pieces,omitempty"`
	ReferenceNumber        string     `json:"reference_number,omitempty"`
	LastMileTrackingNumber string     `json:"last_mile_tracking_number,omitempty"`
	LastMileCarrierCode    string     `json:"last_mile_carrier_code,omitempty"`
	EstimatedDeliveryFrom  *time.Time `json:"estimated_delivery_from,omitempty"`
	EstimatedDeliveryTo    *time.Time `json:"estimated_delivery_to,omitempty"`
}

// Merge overwrites fields that are set in u.
func (d *ShippingDetail) Merge(u ShippingDetail) {
	d.Origin.Merge(u.Origin)
	d.Destination.Merge(u.Destination)
	setStr(&d.ServiceType, u.ServiceType)
	setStr(&d.ReferenceNumber, u.ReferenceNumber)
	setStr(&d.LastMileTrackingNumber, u.LastMileTrackingNumber)
	setStr(&d.LastMileCarrierCode, u.LastMileCarrierCode)
	if u.Pieces != nil {
		d.Pieces = u.Pieces
	}
	if u.EstimatedDeliveryFrom != nil {
		d.EstimatedDeliveryFrom = u.EstimatedDeliveryFrom
	}
	if u.EstimatedDeliveryTo != nil {
		d.EstimatedDeliveryTo = u.EstimatedDeliveryTo
	}
}

// Address is a coarse location; street is intentionally not stored (event coordinates
// live on TrackingEvent).
type Address struct {
	Country    string `json:"country,omitempty"`
	State      string `json:"state,omitempty"`
	City       string `json:"city,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
}

// Merge overwrites fields that are set in u.
func (a *Address) Merge(u Address) {
	setStr(&a.Country, u.Country)
	setStr(&a.State, u.State)
	setStr(&a.City, u.City)
	setStr(&a.PostalCode, u.PostalCode)
}

func setStr(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// Milestones maps a Stage* value to the time that stage was reached.
type Milestones map[string]time.Time

// Subscription status machine (former task_status) — service-owned, not 17TRACK.
const (
	SubRegistering = "REGISTERING"
	SubTracking    = "TRACKING"
	SubStopped     = "STOPPED"
	SubCancelled   = "CANCELLED"
	SubClosed      = "CLOSED"
	// SubNeedsCarrier: 17TRACK rejected auto-detect (ambiguous / unrecognized carrier).
	// Package exists locally but was never subscribed — pick a concrete carrier and retry.
	SubNeedsCarrier = "NEEDS_CARRIER"
)

// Package sources.
const (
	SourceAPI      = "api"
	SourceShipment = "shipment"
)

// Return types.
const (
	ReturnRTS       = "RTS"
	ReturnRefused   = "REFUSED"
	ReturnWarehouse = "RETURN_WAREHOUSE"
)

// IsDelivered reports whether package status is delivered.
func (p Package) IsDelivered() bool { return p.Status == StatusDelivered }

// IsActive reports whether 17TRACK is (or is about to be) tracking this generation.
func (p Package) IsActive() bool {
	return p.SubscriptionStatus == SubRegistering || p.SubscriptionStatus == SubTracking
}

// IsFinished reports whether this generation's shipment is over, so a register of the
// same number may be a carrier recycling it (see Service.Register).
func (p Package) IsFinished() bool {
	if p.IsArchived || p.SubscriptionStatus == SubCancelled {
		return true
	}
	return p.IsDelivered() && !p.IsActive()
}
