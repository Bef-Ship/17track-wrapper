package track17

import (
	"context"
	"strings"

	sdk "github.com/SolaTyolo/17track-sdk"
	"github.com/SolaTyolo/17track-wrapper/pkg/config"
)

// Client is the 17TRACK surface used by tracking (register / stop / retrack / change / delete / info).
// *sdk.Client and *Nop satisfy this interface.
type Client interface {
	Token() string
	Register(ctx context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error)
	StopTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	Retrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	ChangeCarrier(ctx context.Context, items []sdk.ChangeCarrierItem) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	DeleteTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)
	GetTrackInfo(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.TrackDetail], error)
}

// NewClientFromToken picks Nop when token is the sentinel, otherwise a real SDK client.
// Empty token returns Nop only outside production (local DX); production empty → real
// client with empty token so calls fail loudly via ErrEmptyToken / ErrNotConfigured.
func NewClientFromToken(token string) Client {
	token = strings.TrimSpace(token)
	if token == NopToken() {
		return NewNop()
	}
	if token == "" && !config.IsProduction() {
		return NewNop()
	}
	return sdk.New(token, ClientOptionsFromEnv()...)
}

// NewClientFromEnv builds a client from TRACK17_SECRET (platform account only).
// TRACK17_SECRET=nop-track17-token → Nop; otherwise real SDK (rate-limited).
func NewClientFromEnv() Client {
	return WithRateLimit(NewClientFromToken(TokenFromEnv()), DefaultQPS)
}

// NewClientForAppEnv is an alias of NewClientFromEnv (env-only platform token).
func NewClientForAppEnv() Client {
	return NewClientFromEnv()
}

// Compile-time check: upstream SDK client implements Client.
var _ Client = (*sdk.Client)(nil)
