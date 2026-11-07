package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// TrackingEvent is one logistics scan / status line for a package.
type TrackingEvent struct {
	EventID          int64           `json:"event_id,omitempty"`
	PackageID        int64           `json:"package_id"`
	TrackingNumber   string          `json:"tracking_number"`
	EventTime        time.Time       `json:"event_time"`
	CarrierCode      string          `json:"carrier_code,omitempty"` // carrier of this leg; may differ from the package carrier
	Stage            string          `json:"stage,omitempty"`        // Stage* when this event marks a milestone
	SubStatus        string          `json:"sub_status,omitempty"`
	StatusCode       string          `json:"status_code,omitempty"` // carrier's raw status code
	Description      string          `json:"description,omitempty"`
	Location         string          `json:"location,omitempty"`
	Address          Address         `json:"address"`
	RawJSON          json.RawMessage `json:"raw_json,omitempty"`
	EventFingerprint string          `json:"event_fingerprint"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
}

// Fingerprint builds a stable idempotency key for an event line.
// Changing the formula breaks dedup against already-stored events.
func Fingerprint(trackingNumber string, eventTime time.Time, location, description, statusCode string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%d|%s|%s|%s",
		trackingNumber, eventTime.UTC().UnixNano(), location, description, statusCode)
	return hex.EncodeToString(h.Sum(nil))
}

// EventBatch is one normalized tracking update for one tracking number.
// Empty / nil fields mean "unchanged".
type EventBatch struct {
	TenantID       string
	TrackingNumber string
	CarrierCode    string
	ProviderName   string
	UpdateTime     time.Time // zero when the caller did not send one
	Status         string
	SubStatus      string
	SubStatusDesc  string
	ShippingDetail ShippingDetail
	Milestones     Milestones
	Events         []TrackingEvent
}

// ArchiveSnapshot is the cold JSON document stored for an archived package.
type ArchiveSnapshot struct {
	Package    Package         `json:"package"`
	Events     []TrackingEvent `json:"events"`
	ArchivedAt time.Time       `json:"archived_at"`
}
