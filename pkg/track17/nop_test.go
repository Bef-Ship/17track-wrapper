package track17

import (
	"context"
	"testing"

	sdk "github.com/SolaTyolo/17track-sdk"
)

func TestNop_GetTrackInfo(t *testing.T) {
	n := NewNop()
	if n.Token() == "" {
		t.Fatal("nop token must be non-empty for express flow gates")
	}
	ctx := context.Background()
	info, err := n.GetTrackInfo(ctx, []sdk.TrackNumber{{Number: "1ZNOP", Carrier: 100001}})
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
	reg, err := n.Register(ctx, []sdk.RegisterItem{{Number: "1ZNOP", Carrier: 100001}})
	if err != nil || len(reg.Accepted) != 1 {
		t.Fatalf("register: %+v %v", reg, err)
	}
}
