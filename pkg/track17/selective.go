package track17

import (
	"context"

	sdk "github.com/SolaTyolo/17track-sdk"
)

// Selective routes NopCarrierKey items to Nop and everything else to real.
// Used when ENABLE_MOCK=true so mock packages never hit the real 17TRACK API.
type Selective struct {
	real Client
	nop  *Nop
}

// NewSelective wraps real with a Nop for carrier key NopCarrierKey.
// real may be a rate-limited SDK client (token may be empty for nop-only local).
func NewSelective(real Client, nop *Nop) *Selective {
	if nop == nil {
		nop = NewNop()
	}
	return &Selective{real: real, nop: nop}
}

func (s *Selective) Token() string {
	if s.real != nil {
		if t := s.real.Token(); t != "" {
			return t
		}
	}
	// Non-empty so service.client() allows nop-only local (no TRACK17_SECRET).
	return s.nop.Token()
}

func (s *Selective) Register(ctx context.Context, items []sdk.RegisterItem) (*sdk.AcceptedRejected[sdk.RegisterAccepted], error) {
	nopItems, realItems := splitRegister(items)
	out := &sdk.AcceptedRejected[sdk.RegisterAccepted]{}
	if len(nopItems) > 0 {
		res, err := s.nop.Register(ctx, nopItems)
		if err != nil {
			return nil, err
		}
		mergeAcceptedRejected(out, res)
	}
	if len(realItems) > 0 {
		if s.real == nil {
			return nil, errRealRequired()
		}
		res, err := s.real.Register(ctx, realItems)
		if err != nil {
			return nil, err
		}
		mergeAcceptedRejected(out, res)
	}
	return out, nil
}

func (s *Selective) StopTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	return s.splitTrack(ctx, items,
		func(ctx context.Context, in []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
			return s.nop.StopTrack(ctx, in)
		},
		func(ctx context.Context, in []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
			return s.real.StopTrack(ctx, in)
		},
	)
}

func (s *Selective) Retrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	return s.splitTrack(ctx, items,
		func(ctx context.Context, in []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
			return s.nop.Retrack(ctx, in)
		},
		func(ctx context.Context, in []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
			return s.real.Retrack(ctx, in)
		},
	)
}

func (s *Selective) DeleteTrack(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	return s.splitTrack(ctx, items,
		func(ctx context.Context, in []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
			return s.nop.DeleteTrack(ctx, in)
		},
		func(ctx context.Context, in []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
			return s.real.DeleteTrack(ctx, in)
		},
	)
}

func (s *Selective) ChangeCarrier(ctx context.Context, items []sdk.ChangeCarrierItem) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	var nopItems, realItems []sdk.ChangeCarrierItem
	for _, it := range items {
		// Route by target carrier: switching onto NOP stays local.
		if IsNopCarrier(it.CarrierNew) {
			nopItems = append(nopItems, it)
		} else {
			realItems = append(realItems, it)
		}
	}
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	if len(nopItems) > 0 {
		res, err := s.nop.ChangeCarrier(ctx, nopItems)
		if err != nil {
			return nil, err
		}
		mergeSimple(out, res)
	}
	if len(realItems) > 0 {
		if s.real == nil {
			return nil, errRealRequired()
		}
		res, err := s.real.ChangeCarrier(ctx, realItems)
		if err != nil {
			return nil, err
		}
		mergeSimple(out, res)
	}
	return out, nil
}

func (s *Selective) GetTrackInfo(ctx context.Context, items []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.TrackDetail], error) {
	nopItems, realItems := splitTrack(items)
	out := &sdk.AcceptedRejected[sdk.TrackDetail]{}
	if len(nopItems) > 0 {
		res, err := s.nop.GetTrackInfo(ctx, nopItems)
		if err != nil {
			return nil, err
		}
		if res != nil {
			out.Accepted = append(out.Accepted, res.Accepted...)
			out.Rejected = append(out.Rejected, res.Rejected...)
		}
	}
	if len(realItems) > 0 {
		if s.real == nil {
			return nil, errRealRequired()
		}
		res, err := s.real.GetTrackInfo(ctx, realItems)
		if err != nil {
			return nil, err
		}
		if res != nil {
			out.Accepted = append(out.Accepted, res.Accepted...)
			out.Rejected = append(out.Rejected, res.Rejected...)
		}
	}
	return out, nil
}

type trackFn func(context.Context, []sdk.TrackNumber) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error)

func (s *Selective) splitTrack(ctx context.Context, items []sdk.TrackNumber, nopFn, realFn trackFn) (*sdk.AcceptedRejected[sdk.SimpleAccepted], error) {
	nopItems, realItems := splitTrack(items)
	out := &sdk.AcceptedRejected[sdk.SimpleAccepted]{}
	if len(nopItems) > 0 {
		res, err := nopFn(ctx, nopItems)
		if err != nil {
			return nil, err
		}
		mergeSimple(out, res)
	}
	if len(realItems) > 0 {
		if s.real == nil {
			return nil, errRealRequired()
		}
		res, err := realFn(ctx, realItems)
		if err != nil {
			return nil, err
		}
		mergeSimple(out, res)
	}
	return out, nil
}

func splitRegister(items []sdk.RegisterItem) (nopItems, realItems []sdk.RegisterItem) {
	for _, it := range items {
		if IsNopCarrier(it.Carrier) {
			nopItems = append(nopItems, it)
		} else {
			realItems = append(realItems, it)
		}
	}
	return nopItems, realItems
}

func splitTrack(items []sdk.TrackNumber) (nopItems, realItems []sdk.TrackNumber) {
	for _, it := range items {
		if IsNopCarrier(it.Carrier) {
			nopItems = append(nopItems, it)
		} else {
			realItems = append(realItems, it)
		}
	}
	return nopItems, realItems
}

func mergeAcceptedRejected(dst, src *sdk.AcceptedRejected[sdk.RegisterAccepted]) {
	if src == nil {
		return
	}
	dst.Accepted = append(dst.Accepted, src.Accepted...)
	dst.Rejected = append(dst.Rejected, src.Rejected...)
}

func mergeSimple(dst, src *sdk.AcceptedRejected[sdk.SimpleAccepted]) {
	if src == nil {
		return
	}
	dst.Accepted = append(dst.Accepted, src.Accepted...)
	dst.Rejected = append(dst.Rejected, src.Rejected...)
}

func errRealRequired() error {
	return sdk.ErrEmptyToken
}

var _ Client = (*Selective)(nil)
