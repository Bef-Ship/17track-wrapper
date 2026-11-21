package track17

import (
	"context"
	"strings"

	sdk "github.com/SolaTyolo/17track-sdk"
	"github.com/SolaTyolo/17track-wrapper/pkg/config"
)

// Client is the 17TRACK surface used by tracking (register / stop / retrack / change / delete / info).
// *sdk.Client, *Nop and *Selective satisfy this interface.
type Client interface {
	Token() string
	Register(ctx context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error)
	StopTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	Retrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	ChangeCarrier(ctx context.Context, items []sdk.ChangeCarrierItem) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	DeleteTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	GetTrackInfo(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.TrackDetail], error)
}

// NewClientFromToken builds a real SDK client. Empty token is allowed (calls fail at request time);
// mock packages use ENABLE_MOCK + NopCarrierKey via NewSelective — not a sentinel TRACK17_SECRET.
func NewClientFromToken(token string) Client {
	return sdk.New(strings.TrimSpace(token), ClientOptionsFromEnv()...)
}

// NewClientFromEnv builds the platform 17TRACK client from TRACK17_SECRET.
// When ENABLE_MOCK=true, wraps with Selective so NopCarrierKey never hits the real API.
func NewClientFromEnv() Client {
	real := WithRateLimit(NewClientFromToken(TokenFromEnv()), DefaultQPS)
	if config.EnableMock() {
		return NewSelective(real, NewNop())
	}
	return real
}

// NewClientForAppEnv is an alias of NewClientFromEnv (env-only platform token).
func NewClientForAppEnv() Client {
	return NewClientFromEnv()
}

// Compile-time check: upstream SDK client implements Client.
var _ Client = (*sdk.Client)(nil)
