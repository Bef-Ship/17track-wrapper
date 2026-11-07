package olap

import (
	"context"
	"errors"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
)

// ErrDisabled is returned by read queries when no OLAP backend is configured.
var ErrDisabled = errors.New("olap disabled")

// Event is a denormalized row written to the OLAP store for BI / analytics.
// Package fields are a snapshot taken when the event was written.
type Event struct {
	TenantID       string     `json:"tenant_id"`
	PackageID      int64      `json:"package_id"`
	TrackingNumber string     `json:"tracking_number"`
	CarrierCode    string     `json:"carrier_code"`
	ProviderName   string     `json:"provider_name"`
	ExternalRef    string     `json:"external_ref"`
	ServiceType    string     `json:"service_type"`
	OriginCountry  string     `json:"origin_country"`
	DestCountry    string     `json:"destination_country"`
	PackageStatus  string     `json:"package_status"`
	PickedUpAt     *time.Time `json:"picked_up_at,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`

	EventTime        time.Time `json:"event_time"`
	EventCarrierCode string    `json:"event_carrier_code"`
	Stage            string    `json:"stage"`
	SubStatus        string    `json:"sub_status"`
	StatusCode       string    `json:"status_code"`
	Location         string    `json:"location"`
	Country          string    `json:"country"`
	Description      string    `json:"description"`
	Fingerprint      string    `json:"fingerprint"`
}

// FromDomain maps a hot-store event + package context into an OLAP row.
func FromDomain(pkg *domain.Package, ev domain.TrackingEvent) Event {
	return Event{
		TenantID:       pkg.TenantID,
		PackageID:      pkg.PackageID,
		TrackingNumber: ev.TrackingNumber,
		CarrierCode:    pkg.CarrierCode,
		ProviderName:   pkg.ProviderName,
		ExternalRef:    pkg.ExternalRef,
		ServiceType:    pkg.ServiceType,
		OriginCountry:  pkg.Origin.Country,
		DestCountry:    pkg.Destination.Country,
		PackageStatus:  pkg.Status,
		PickedUpAt:     pkg.PickedUpAt,
		DeliveredAt:    pkg.DeliveredAt,

		EventTime:        ev.EventTime,
		EventCarrierCode: ev.CarrierCode,
		Stage:            ev.Stage,
		SubStatus:        ev.SubStatus,
		StatusCode:       ev.StatusCode,
		Location:         ev.Location,
		Country:          ev.Address.Country,
		Description:      ev.Description,
		Fingerprint:      ev.EventFingerprint,
	}
}

// Writer is the analytics sink. Implementations must be safe for concurrent use.
// When OLAP is disabled, use Noop.
type Writer interface {
	// Name returns the backend name for logs (e.g. "clickhouse", "noop").
	Name() string
	// EnsureSchema creates tables / indexes if needed.
	EnsureSchema(ctx context.Context) error
	// InsertEvents appends events idempotently when possible (fingerprint).
	InsertEvents(ctx context.Context, events []Event) error
	Close() error
}

// EventFilter narrows OLAP reads to one tenant and an event_time range [From, To).
// Empty string fields are ignored.
type EventFilter struct {
	TenantID           string
	From, To           time.Time
	TrackingNumber     string
	CarrierCode        string
	EventCarrierCode   string
	ProviderName       string
	PackageStatus      string
	Stage              string
	SubStatus          string
	OriginCountry      string
	DestinationCountry string
	Country            string
}

// EventQuery lists event rows, newest first.
type EventQuery struct {
	EventFilter
	Limit  int
	Offset int
}

// StatsQuery counts events / distinct packages grouped by StatsDimensions.
type StatsQuery struct {
	EventFilter
	GroupBy []string
	Limit   int
}

// StatsRow is one group of a stats query.
type StatsRow struct {
	Dims     map[string]string `json:"dims"`
	Events   uint64            `json:"events"`
	Packages uint64            `json:"packages"`
}

// StatsDimensions are the allowed StatsQuery.GroupBy values.
var StatsDimensions = []string{
	"day", "month", "carrier_code", "event_carrier_code", "provider_name", "package_status",
	"stage", "sub_status", "origin_country", "destination_country", "country", "service_type",
}

// Reader serves analytics queries. Writers that support reads implement it too.
type Reader interface {
	QueryEvents(ctx context.Context, q EventQuery) ([]Event, error)
	Stats(ctx context.Context, q StatsQuery) ([]StatsRow, error)
}
