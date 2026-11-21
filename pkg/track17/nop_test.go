package track17

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/SolaTyolo/17track-sdk"
)

func TestNop_GetTrackInfo(t *testing.T) {
	n := NewNop()
	if n.Token() == "" {
		t.Fatal("nop token must be non-empty for client() gates")
	}
	ctx := context.Background()
	info, err := n.GetTrackInfo(ctx, []sdk.TrackNumber{{Number: "1ZNOP", Carrier: NopCarrierKey}})
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || len(info.Accepted) != 1 {
		t.Fatalf("expected 1 accepted, got %+v", info)
	}
	p := ParseDetail(&info.Accepted[0])
	if p.Status != sdk.StatusInTransit || len(p.Events) == 0 {
		t.Fatalf("parsed fake track: %+v", p)
	}
	reg, err := n.Register(ctx, []sdk.RegisterItem{{Number: "1ZNOP", Carrier: NopCarrierKey}})
	if err != nil || len(reg.Accepted) != 1 {
		t.Fatalf("register: %+v %v", reg, err)
	}
}

func TestNopWebhookBody(t *testing.T) {
	raw, err := NopWebhookBody("1ZNOP", NopCarrierKey)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Event != "TRACKING_UPDATED" || len(env.Data) == 0 {
		t.Fatalf("body: %+v", env)
	}
	parsed, err := ParseDetailJSON(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Status != sdk.StatusInTransit || len(parsed.Events) == 0 {
		t.Fatalf("parsed: %+v", parsed)
	}
}

func TestSelective_RoutesNop(t *testing.T) {
	real := NewNop() // stand-in; Selective still routes by carrier key
	sel := NewSelective(real, NewNop())
	ctx := context.Background()
	res, err := sel.Register(ctx, []sdk.RegisterItem{
		{Number: "NOP1", Carrier: NopCarrierKey},
		{Number: "REAL1", Carrier: 100002},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accepted) != 2 {
		t.Fatalf("accepted=%d", len(res.Accepted))
	}
}
