package track17

import (
	"context"
	"time"

	sdk "github.com/SolaTyolo/17track-sdk"
)

const nopToken = "nop-track17-token"

// NopToken is the sentinel token used by NewNop (non-production).
func NopToken() string { return nopToken }

// Nop is a no-op 17TRACK client that returns fake Accepted payloads.
type Nop struct{}

// NewNop returns a Client suitable for non-production environments.
func NewNop() *Nop { return &Nop{} }

func (n *Nop) Token() string { return nopToken }

func (n *Nop) Register(_ context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error) {
	out := &sdk.AcceptedRejected[sdk.RegisterAccepted]{}
	for _, it := range items {
		out.Accepted = append(out.Accepted, sdk.RegisterAccepted{
			Number:  it.Number,
			Carrier: it.Carrier,
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
		out.Accepted = append(out.Accepted, fakeDetail(it, pickup, transit, now))
	}
	return out, nil
}

func simpleAccepted(items []sdk.TrackNumber) *sdk.AcceptedRejected[sdk.SimpleAccepted] {
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	for _, it := range items {
		out.Accepted = append(out.Accepted, sdk.SimpleAccepted{Number: it.Number, Carrier: it.Carrier})
	}
	return out
}

func fakeDetail(it sdk.TrackNumber, pickup, transit, latest time.Time) sdk.TrackDetail {
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
		Number:  it.Number,
		Carrier: it.Carrier,
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
						Key:      it.Carrier,
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

var _ Client = (*Nop)(nil)
