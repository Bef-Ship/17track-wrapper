package domain

import sdk "github.com/SolaTyolo/17track-sdk"

// 17TRACK logistics enums stored on tracking.package / tracking.tracking_event.
//
//	package.status           ← latest_status.status          (Status*)
//	package.sub_status       ← latest_status.sub_status      (SubStatus*)
//	package.sub_status_desc  ← latest_status.sub_status_descr (SubStatusDescription)
//	tracking_event.stage     ← event.stage / milestone.key_stage (Stage*)
//	tracking_event.sub_status← event.sub_status              (SubStatus*)
//
// Values are aliases of github.com/SolaTyolo/17track-sdk constants (except
// StatusRegistered / local-only stages that 17TRACK never emits).

// Package main status — latest_status.status (PascalCase).
const (
	// StatusRegistered is local-only until the first 17TRACK sync.
	StatusRegistered         = "registered"
	StatusNotFound           = sdk.StatusNotFound
	StatusInfoReceived       = sdk.StatusInfoReceived
	StatusInTransit          = sdk.StatusInTransit
	StatusExpired            = sdk.StatusExpired
	StatusAvailableForPickup = sdk.StatusAvailableForPickup
	StatusOutForDelivery     = sdk.StatusOutForDelivery
	StatusDeliveryFailure    = sdk.StatusDeliveryFailure
	StatusDelivered          = sdk.StatusDelivered
	StatusException          = sdk.StatusException
)

// Sub-status — latest_status.sub_status / event.sub_status (30 official codes).
const (
	SubStatusNotFoundOther                 = sdk.SubStatusNotFoundOther
	SubStatusNotFoundInvalidCode           = sdk.SubStatusNotFoundInvalidCode
	SubStatusInfoReceived                  = sdk.SubStatusInfoReceived
	SubStatusInTransitPickedUp             = sdk.SubStatusInTransitPickedUp
	SubStatusInTransitOther                = sdk.SubStatusInTransitOther
	SubStatusInTransitDeparture            = sdk.SubStatusInTransitDeparture
	SubStatusInTransitArrival              = sdk.SubStatusInTransitArrival
	SubStatusInTransitCustomsProc          = sdk.SubStatusInTransitCustomsProc
	SubStatusInTransitCustomsRel           = sdk.SubStatusInTransitCustomsRel
	SubStatusInTransitCustomsInfo          = sdk.SubStatusInTransitCustomsInfo
	SubStatusExpiredOther                  = sdk.SubStatusExpiredOther
	SubStatusAvailableForPickupOther       = sdk.SubStatusAvailableForPickupOther
	SubStatusOutForDeliveryOther           = sdk.SubStatusOutForDeliveryOther
	SubStatusDeliveryFailureOther          = sdk.SubStatusDeliveryFailureOther
	SubStatusDeliveryFailureNoBody         = sdk.SubStatusDeliveryFailureNoBody
	SubStatusDeliveryFailureSecurity       = sdk.SubStatusDeliveryFailureSecurity
	SubStatusDeliveryFailureRejected       = sdk.SubStatusDeliveryFailureRejected
	SubStatusDeliveryFailureInvalidAddress = sdk.SubStatusDeliveryFailureInvalidAddress
	SubStatusDeliveredOther                = sdk.SubStatusDeliveredOther
	SubStatusExceptionOther                = sdk.SubStatusExceptionOther
	SubStatusExceptionReturning            = sdk.SubStatusExceptionReturning
	SubStatusExceptionReturned             = sdk.SubStatusExceptionReturned
	SubStatusExceptionNoBody               = sdk.SubStatusExceptionNoBody
	SubStatusExceptionSecurity             = sdk.SubStatusExceptionSecurity
	SubStatusExceptionDamage               = sdk.SubStatusExceptionDamage
	SubStatusExceptionRejected             = sdk.SubStatusExceptionRejected
	SubStatusExceptionDelayed              = sdk.SubStatusExceptionDelayed
	SubStatusExceptionLost                 = sdk.SubStatusExceptionLost
	SubStatusExceptionDestroyed            = sdk.SubStatusExceptionDestroyed
	SubStatusExceptionCancel               = sdk.SubStatusExceptionCancel
)

// Milestone / event stage — event.stage and milestone.key_stage.
// SDK has no named Stage* consts; these match 17TRACK docs / payloads.
const (
	StageInfoReceived       = "InfoReceived"
	StagePickedUp           = "PickedUp"
	StageDeparture          = "Departure"
	StageArrival            = "Arrival"
	StageAvailableForPickup = "AvailableForPickup"
	StageOutForDelivery     = "OutForDelivery"
	StageDelivered          = "Delivered"
	StageReturning          = "Returning"
	StageReturned           = "Returned"
)

// IsValidSubStatus reports whether s is an official 17TRACK sub_status code.
func IsValidSubStatus(s string) bool {
	_, ok := sdk.LookupSubStatus(s)
	return ok
}

// SubStatusDescription returns the official Chinese description for a sub_status
// (maps to package.sub_status_desc when webhook omits sub_status_descr).
func SubStatusDescription(code string) string {
	if info, ok := sdk.LookupSubStatus(code); ok {
		return info.Description
	}
	return ""
}
