package domain

import "testing"

func TestTrack17ConstAliases(t *testing.T) {
	if StatusDelivered != "Delivered" {
		t.Fatalf("StatusDelivered = %q", StatusDelivered)
	}
	if SubStatusExceptionReturning != "Exception_Returning" || !IsValidSubStatus(SubStatusExceptionReturning) {
		t.Fatalf("SubStatusExceptionReturning = %q", SubStatusExceptionReturning)
	}
	if got := SubStatusDescription(SubStatusDeliveredOther); got == "" {
		t.Fatal("expected SubStatusDescription for Delivered_Other")
	}
	if SubStatusDescription("nope") != "" {
		t.Fatal("unknown sub_status should have empty description")
	}
}
