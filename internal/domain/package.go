package domain

import "time"

// Package is the minimal tracking unit. One package owns one tracking_number
// (or shares one with siblings — uniqueness is still on tracking_number).
// Shipment / order aggregation lives in upstream services via ExternalRef.
type Package struct {
	PackageID      int64    `json:"package_id"`
	TenantID       string   `json:"tenant_id"`
	TrackingNumber string   `json:"tracking_number"` // unique per tenant
	CarrierCode    string   `json:"carrier_code"`
	ProviderName   string   `json:"provider_name"`          // data source label set at registration, e.g. carrier_direct
	ExternalRef    string   `json:"external_ref,omitempty"` // caller-owned id (order/shipment/…)
	LabelURL       string   `json:"label_url,omitempty"`
	Weight         *float64 `json:"weight,omitempty"` // kg
	DimLength      *float64 `json:"dim_length,omitempty"`
	DimWidth       *float64 `json:"dim_width,omitempty"`
	DimHeight      *float64 `json:"dim_height,omitempty"`

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
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ShippingDetail is route / service metadata that can be set at registration
// and refined by later tracking updates.
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

// Address is a coarse location; street and coordinates are intentionally not stored.
type Address struct {
	Country    string `json:"country,omitempty"` // ISO 3166-1 alpha-2
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

// DefaultTenant is used when a request carries no tenant id.
const DefaultTenant = "default"

// Milestones maps a Stage* value to the time that stage was reached.
type Milestones map[string]time.Time

// Package status values. StatusRegistered is set before any tracking update;
// the rest form the main tracking status of a package.
const (
	StatusRegistered         = "registered"
	StatusNotFound           = "not_found"
	StatusInfoReceived       = "info_received"
	StatusInTransit          = "in_transit"
	StatusExpired            = "expired"
	StatusAvailableForPickup = "available_for_pickup"
	StatusOutForDelivery     = "out_for_delivery"
	StatusDeliveryFailure    = "delivery_failure"
	StatusDelivered          = "delivered"
	StatusException          = "exception"
)

var validStatuses = map[string]bool{
	StatusRegistered: true, StatusNotFound: true, StatusInfoReceived: true,
	StatusInTransit: true, StatusExpired: true, StatusAvailableForPickup: true,
	StatusOutForDelivery: true, StatusDeliveryFailure: true, StatusDelivered: true,
	StatusException: true,
}

// IsValidStatus reports whether s is a known package status.
func IsValidStatus(s string) bool { return validStatuses[s] }

// Milestone stages: key points of the shipping process, used by
// Package.Milestones and TrackingEvent.Stage.
const (
	StageInfoReceived       = "info_received"
	StagePickedUp           = "picked_up"
	StageDeparture          = "departure"
	StageArrival            = "arrival"
	StageAvailableForPickup = "available_for_pickup"
	StageOutForDelivery     = "out_for_delivery"
	StageDelivered          = "delivered"
	StageReturning          = "returning"
	StageReturned           = "returned"
)

var validStages = map[string]bool{
	StageInfoReceived: true, StagePickedUp: true, StageDeparture: true,
	StageArrival: true, StageAvailableForPickup: true, StageOutForDelivery: true,
	StageDelivered: true, StageReturning: true, StageReturned: true,
}

// IsValidStage reports whether s is a known milestone stage.
func IsValidStage(s string) bool { return validStages[s] }

func setStr(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}
