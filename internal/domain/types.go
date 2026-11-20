package domain

import (
	"encoding/json"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"
)

// PackageEventsView is one package generation + its hot or archived events.
type PackageEventsView struct {
	Package Package         `json:"package"`
	Events  []TrackingEvent `json:"events"`
	Cold    bool            `json:"cold"`
}

// PackageRef addresses one generation: PackageID when set; else the generation of
// Number registered with ExternalRef (current or superseded, newest first); else the
// current generation of Number.
type PackageRef struct {
	Number      string
	PackageID   int64
	ExternalRef string
}

// PackageHistory lists every generation of a tracking number, newest first.
type PackageHistory struct {
	Number      string              `json:"number"`
	Generations []PackageEventsView `json:"generations"`
}

// RegisterItemInput is one register request element: the 17TRACK /register item
// (number, carrier, lang, order_no, destination_*, tag, …) plus service fields.
type RegisterItemInput struct {
	sdk.RegisterItem
	ExternalRef string `json:"external_ref,omitempty"`
	// NewGeneration forces a new generation even when the current one is not finished
	// (the carrier recycled the number before we saw delivery / cancel).
	NewGeneration bool `json:"new_generation,omitempty"`
}

// RegisterInput is POST /register body.
type RegisterInput struct {
	Items  []RegisterItemInput `json:"items"`
	Source string              `json:"source,omitempty"`
}

// ItemResult is one accepted or rejected element of a batch call: the 17TRACK
// {number, carrier, error} item plus the generation (package_id) it resolved to.
type ItemResult struct {
	Number             string     `json:"number"`
	Carrier            int        `json:"carrier"`
	PackageID          int64      `json:"package_id,omitempty"`
	SubscriptionStatus string     `json:"subscription_status,omitempty"`
	Error              *ErrorBody `json:"error,omitempty"`
}

// Reject marks the item rejected with err's 17TRACK-style code.
func (r *ItemResult) Reject(err error) { r.RejectWith(Body(err)) }

func (r *ItemResult) RejectWith(body ErrorBody) { r.Error = &body }

// BatchResult is the 17TRACK batch envelope data ({accepted, rejected}).
type BatchResult struct {
	Accepted []ItemResult `json:"accepted"`
	Rejected []ItemResult `json:"rejected"`
}

// SplitResults partitions items into accepted / rejected, keeping request order.
func SplitResults(items []ItemResult) BatchResult {
	out := BatchResult{Accepted: []ItemResult{}, Rejected: []ItemResult{}}
	for _, it := range items {
		if it.Error != nil {
			out.Rejected = append(out.Rejected, it)
		} else {
			out.Accepted = append(out.Accepted, it)
		}
	}
	return out
}

// CancelInput is POST /cancel body (17TRACK /stoptrack items).
type CancelInput struct {
	Items []sdk.TrackNumber `json:"items"`
}

// ChangeCarrierItemInput is the 17TRACK /changecarrier item; carrier_old defaults to
// the stored carrier.
type ChangeCarrierItemInput = sdk.ChangeCarrierItem

// ChangeCarrierInput is POST /change-carrier body (single or batch).
type ChangeCarrierInput struct {
	Items []ChangeCarrierItemInput `json:"items"`
}

// ListPackagesFilter supports GET /packages.
type ListPackagesFilter struct {
	Statuses           []string
	Carrier            *int
	ExternalRef        string
	OriginCountry      string
	DestinationCountry string
	IsArchived         *bool
	CreatedFrom        *time.Time
	CreatedTo          *time.Time
	SubscriptionStatus string
	HasReturn          *bool
	Tag                string
	Source             string
	// IncludeSuperseded also lists older generations of recycled numbers.
	IncludeSuperseded bool
	BeforeID          int64
	Limit             int
	Offset            int
}

// ListPackagesResult is a page of packages.
type ListPackagesResult struct {
	Items      []Package `json:"items"`
	NextCursor int64     `json:"next_cursor,omitempty"`
	Limit      int       `json:"limit"`
	Offset     int       `json:"offset"`
}

// 17TRACK webhook events.
const (
	EventTrackingUpdated = "TRACKING_UPDATED"
	EventTrackingStopped = "TRACKING_STOPPED"
)

// WebhookApplyInput is a normalized 17TRACK push.
type WebhookApplyInput struct {
	TrackingNo  string
	CallbackSeq string
	Raw         json.RawMessage
	Event       string
}

// RegisterJob is enqueued when 17TRACK register hits rate limits / transient errors.
type RegisterJob struct {
	PackageID int64            `json:"package_id"`
	Item      sdk.RegisterItem `json:"item"`
}

// ReturnHit is the result of return detection.
type ReturnHit struct {
	Type           string
	CarrierEventID string
	Supported      bool
	MatchedDesc    string
}

// GeoJSON FeatureCollection (route response).
type GeoJSONFeatureCollection struct {
	Type     string           `json:"type"`
	Features []GeoJSONFeature `json:"features"`
}

type GeoJSONFeature struct {
	Type       string          `json:"type"`
	Geometry   GeoJSONGeometry `json:"geometry"`
	Properties map[string]any  `json:"properties,omitempty"`
}

type GeoJSONGeometry struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}
