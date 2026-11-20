package track17

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed apicarrier.all.json
var apicarrierJSON []byte

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
