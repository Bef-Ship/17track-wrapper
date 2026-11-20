package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SolaTyolo/17track-wrapper/internal/application"
	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/pkg/event"
)

const queueSource = "shipment_callback"

// ShipmentProjector POSTs package updates to the backend shipment projection URL.
type ShipmentProjector struct {
	URL    string
	Secret string
	Client *http.Client
}

var (
	_ application.Observer = (*ShipmentProjector)(nil)
	_ application.Observer = (*QueuedProjector)(nil)
)

// Payload is the body POSTed to SHIPMENT_CALLBACK_URL; sent only when a package's
// events or status / subscription / carrier / return state changed.
type Payload struct {
	// PackageID identifies the generation (tracking numbers can be recycled).
	PackageID          int64  `json:"package_id"`
	TrackingNo         string `json:"tracking_number"`
	Carrier            int    `json:"carrier"`
	SubscriptionStatus string `json:"subscription_status"`
	Status             string `json:"status"`
	SubStatus          string `json:"sub_status,omitempty"`
	Source             string `json:"source"`
	ExternalRef        string `json:"external_ref,omitempty"`
	Active             bool   `json:"active"`
	Pushed             bool   `json:"pushed"`
	// Package is the full stored snapshot; NewEvents the events first stored by this change.
	Package   domain.Package         `json:"package"`
	NewEvents []domain.TrackingEvent `json:"new_events"`
	// Parsed is the raw 17TRACK detail (track17.ParsedTrack), kept for existing receivers.
	Parsed json.RawMessage `json:"parsed,omitempty"`
	// OccurredAt lets the receiver drop deliveries older than state it already applied
	// (queued retries can arrive out of order).
	OccurredAt time.Time `json:"occurred_at"`
}

// BuildPayload snapshots u for delivery.
func BuildPayload(u application.Update) (Payload, error) {
	pkg := u.Package
	body := Payload{
		PackageID:          pkg.PackageID,
		TrackingNo:         pkg.TrackingNumber,
		Carrier:            pkg.Carrier,
		SubscriptionStatus: pkg.SubscriptionStatus,
		Status:             pkg.Status,
		SubStatus:          pkg.SubStatus,
		Source:             pkg.Source,
		ExternalRef:        pkg.ExternalRef,
		Active:             pkg.IsActive(),
		Pushed:             u.Pushed,
		Package:            pkg,
		NewEvents:          u.NewEvents,
		OccurredAt:         time.Now().UTC(),
	}
	if body.NewEvents == nil {
		body.NewEvents = []domain.TrackingEvent{}
	}
	if u.Parsed != nil {
		raw, err := json.Marshal(u.Parsed)
		if err != nil {
			return Payload{}, err
		}
		body.Parsed = raw
	}
	return body, nil
}

func (p *ShipmentProjector) OnTrackingUpdate(ctx context.Context, u application.Update) error {
	body, err := BuildPayload(u)
	if err != nil {
		return err
	}
	return p.Deliver(ctx, body)
}

// Deliver POSTs body to URL; a non-2xx response is an error so callers can retry.
func (p *ShipmentProjector) Deliver(ctx context.Context, body Payload) error {
	url := strings.TrimSpace(p.URL)
	if url == "" {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	cli := p.Client
	if cli == nil {
		cli = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s := strings.TrimSpace(p.Secret); s != "" {
		req.Header.Set("X-Tracking-Callback-Secret", s)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("shipment callback %s: status %d", url, resp.StatusCode)
	}
	return nil
}

// QueuedProjector enqueues payloads on the event bus; the worker role delivers them via
// ShipmentProjector with provider retries, so a backend outage does not drop updates.
type QueuedProjector struct {
	Bus event.Provider
}

func (q *QueuedProjector) OnTrackingUpdate(ctx context.Context, u application.Update) error {
	body, err := BuildPayload(u)
	if err != nil {
		return err
	}
	ch, err := event.Channel(event.SourceInternal, queueSource)
	if err != nil {
		return err
	}
	return q.Bus.Publish(ctx, ch, event.InternalBiz(""), body)
}

// QueueChannel is the event channel consumed by the worker's callback deliverer.
func QueueChannel() string {
	ch, _ := event.Channel(event.SourceInternal, queueSource)
	return ch
}
