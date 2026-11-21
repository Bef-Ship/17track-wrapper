package track17

import (
	"context"
	"encoding/json"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"
)

// Nop is a local mock 17TRACK client used for NopCarrierKey when ENABLE_MOCK=true.
// It never calls the real API; Register is accepted and GetTrackInfo returns fake events.
type Nop struct{}

// NewNop returns a Client for the mock NOP carrier path.
func NewNop() *Nop { return &Nop{} }

func (n *Nop) Token() string { return "nop-carrier" }

func (n *Nop) Register(_ context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error) {
	out := &sdk.AcceptedRejected[sdk.RegisterAccepted]{}
	for _, it := range items {
		carrier := it.Carrier
		if carrier <= 0 {
			carrier = NopCarrierKey
		}
		out.Accepted = append(out.Accepted, sdk.RegisterAccepted{
			Number:  it.Number,
			Carrier: carrier,
			Lang:    it.Lang,
		})
	}
	return out, nil
}

func (n *Nop) StopTrack(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	return simpleAccepted(items), nil
}

func (n *Nop) Retrack(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	return simpleAccepted(items), nil
}

func (n *Nop) DeleteTrack(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	return simpleAccepted(items), nil
}

func (n *Nop) ChangeCarrier(_ context.Context, items []sdk.ChangeCarrierItem) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	for _, it := range items {
		out.Accepted = append(out.Accepted, sdk.SimpleAccepted{Number: it.Number, Carrier: it.CarrierNew})
	}
	return out, nil
}

func (n *Nop) GetTrackInfo(_ context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.TrackDetail], error) {
	out := &sdk.AcceptedRejected[sdk.TrackDetail]{}
	now := time.Now().UTC()
	pickup := now.Add(-48 * time.Hour)
	transit := now.Add(-24 * time.Hour)
	for _, it := range items {
		carrier := it.Carrier
		if carrier <= 0 {
			carrier = NopCarrierKey
		}
		out.Accepted = append(out.Accepted, FakeDetail(it.Number, carrier, pickup, transit, now))
	}
	return out, nil
}

func simpleAccepted(items []sdk.TrackNumber) *sdk.AcceptedRejected[sdk.SimpleAccepted] {
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	for _, it := range items {
		out.Accepted = append(out.Accepted, sdk.SimpleAccepted(it))
	}
	return out
}

// FakeDetail builds a 17TRACK-shaped TrackDetail with pickup / transit / latest events.
func FakeDetail(number string, carrier int, pickup, transit, latest time.Time) sdk.TrackDetail {
	if carrier <= 0 {
		carrier = NopCarrierKey
	}
	pickupEv := sdk.TrackEvent{
		TimeISO:     pickup.Format(time.RFC3339),
		TimeUTC:     pickup.UTC().Format(time.RFC3339),
		TimeRaw:     &sdk.TimeRaw{Timezone: "+00:00"},
		Description: "[NOP] Package picked up",
		Location:    "Origin Hub",
		Stage:       "PickedUp",
		SubStatus:   sdk.SubStatusInTransitPickedUp,
	}
	transitEv := sdk.TrackEvent{
		TimeISO:     transit.Format(time.RFC3339),
		TimeUTC:     transit.UTC().Format(time.RFC3339),
		TimeRaw:     &sdk.TimeRaw{Timezone: "+00:00"},
		Description: "[NOP] In transit to destination",
		Location:    "Transit Hub",
		Stage:       "InTransit",
		SubStatus:   "",
	}
	latestEv := sdk.TrackEvent{
		TimeISO:     latest.Format(time.RFC3339),
		TimeUTC:     latest.UTC().Format(time.RFC3339),
		TimeRaw:     &sdk.TimeRaw{Timezone: "+00:00"},
		Description: "[NOP] Arrived at local facility",
		Location:    "Local Facility",
		Stage:       "InTransit",
		SubStatus:   "",
	}
	return sdk.TrackDetail{
		Number:  number,
		Carrier: carrier,
		Tag:     "nop",
		Lang:    "en",
		TrackInfo: &sdk.TrackInfo{
			LatestStatus: &sdk.LatestStatus{
				Status:    sdk.StatusInTransit,
				SubStatus: "",
			},
			LatestEvent: &latestEv,
			Tracking: &sdk.Tracking{
				Providers: []sdk.ProviderPackage{{
					Provider: &sdk.ProviderInfo{
						Key:      carrier,
						Name:     "NOP Carrier",
						Alias:    "nop",
						Homepage: "https://nop.tracking.local",
					},
					LatestSyncStatus: "Success",
					LatestSyncTime:   latest.Format(time.RFC3339),
					Events:           []sdk.TrackEvent{latestEv, transitEv, pickupEv},
				}},
			},
		},
	}
}

// NopWebhookBody builds a raw 17TRACK TRACKING_UPDATED push body
// ({"event":"TRACKING_UPDATED","data":{…}}) for ApplyWebhookBody / gateway relay.
func NopWebhookBody(number string, carrier int) ([]byte, error) {
	now := time.Now().UTC()
	detail := FakeDetail(number, carrier, now.Add(-48*time.Hour), now.Add(-24*time.Hour), now)
	return json.Marshal(map[string]any{
		"event": "TRACKING_UPDATED",
		"data":  detail,
	})
}

var _ Client = (*Nop)(nil)
