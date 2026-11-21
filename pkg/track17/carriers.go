package track17

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed apicarrier.all.json
var apicarrierJSON []byte

// NopCarrierKey is the mock 17TRACK-style carrier key appended when ENABLE_MOCK=true.
// Outside the real apicarrier catalog; only the Nop / Selective client accepts it.
const NopCarrierKey = 900001

// IsNopCarrier reports whether key is the mock NOP tracking carrier.
func IsNopCarrier(key int) bool {
	return key == NopCarrierKey
}

// Carrier is one 17TRACK apicarrier catalog entry (register / change-carrier UI).
// Source: https://res.17track.net/asset/carrier/info/apicarrier.all.json (slimmed fields).
type Carrier struct {
	Key        int    `json:"key"`
	Name       string `json:"name"`
	NameZhCN   string `json:"name_zh_cn"`
	CountryISO string `json:"country_iso"`
}

var (
	carriersOnce sync.Once
	carriersList []Carrier
	carriersErr  error
)

// NopCarrier is the selectable mock tracking carrier (ENABLE_MOCK).
func NopCarrier() Carrier {
	return Carrier{
		Key:        NopCarrierKey,
		Name:       "NOP (mock)",
		NameZhCN:   "NOP（模拟）",
		CountryISO: "XX",
	}
}

// ListCarriers returns the hardcoded 17TRACK carrier catalog.
func ListCarriers() ([]Carrier, error) {
	carriersOnce.Do(func() {
		var list []Carrier
		if err := json.Unmarshal(apicarrierJSON, &list); err != nil {
			carriersErr = err
			return
		}
		carriersList = list
	})
	return carriersList, carriersErr
}

// ListCarriersWithMock returns the 17TRACK catalog, optionally appending NopCarrier.
func ListCarriersWithMock(includeNop bool) ([]Carrier, error) {
	list, err := ListCarriers()
	if err != nil || !includeNop {
		return list, err
	}
	out := make([]Carrier, 0, len(list)+1)
	out = append(out, list...)
	out = append(out, NopCarrier())
	return out, nil
}
