package domain

import (
	"strings"

	sdk "github.com/SolaTyolo/17track-sdk"
)

// MapSDKStatus canonicalizes 17TRACK latest_status into package Status.
// Prefers main status; falls back to sub_status parent when main is empty/unknown.
func MapSDKStatus(status, subStatus string) string {
	s := strings.TrimSpace(status)
	if code, ok := lookupMainStatus(s); ok {
		return code
	}
	// Legacy snake_case / alias → 17TRACK canonical.
	switch strings.ToLower(strings.ReplaceAll(s, "_", "")) {
	case "notfound", "notfoundother":
		return StatusNotFound
	case "inforeceived":
		return StatusInfoReceived
	case "intransit":
		return StatusInTransit
	case "expired":
		return StatusExpired
	case "availableforpickup":
		return StatusAvailableForPickup
	case "outofdelivery":
		return StatusOutForDelivery
	case "deliveryfailure":
		return StatusDeliveryFailure
	case "delivered":
		return StatusDelivered
	case "exception":
		return StatusException
	case "registered":
		return StatusRegistered
	}
	if sub, ok := sdk.LookupSubStatus(strings.TrimSpace(subStatus)); ok {
		return sub.Parent
	}
	lower := strings.ToLower(subStatus)
	switch {
	case strings.Contains(lower, "delivered"):
		return StatusDelivered
	case strings.Contains(lower, "outofdelivery"), strings.Contains(lower, "out_for_delivery"):
		return StatusOutForDelivery
	case strings.Contains(lower, "intransit"), strings.Contains(lower, "in_transit"):
		return StatusInTransit
	case strings.Contains(lower, "exception"), strings.Contains(lower, "returning"), strings.Contains(lower, "returned"):
		return StatusException
	}
	if s == "" {
		return StatusNotFound
	}
	return StatusInTransit
}

func lookupMainStatus(code string) (string, bool) {
	if info, ok := sdk.LookupStatus(code); ok {
		return info.Code, true
	}
	for _, ms := range sdk.MainStatuses {
		if strings.EqualFold(ms.Code, code) {
			return ms.Code, true
		}
	}
	return "", false
}
