package domain

import "testing"

func TestDetectReturnUses17TrackCodes(t *testing.T) {
	hit := DetectReturn([]TrackingEvent{
		{EventFingerprint: "new", SubStatus: SubStatusExceptionReturned},
		{EventFingerprint: "old", SubStatus: SubStatusExceptionReturning},
	})
	if hit.Type != ReturnWarehouse || hit.CarrierEventID != "new" || !hit.Supported {
		t.Fatalf("hit = %+v", hit)
	}
	if hit := DetectReturn([]TrackingEvent{{Stage: StageReturning}}); hit.Type != ReturnRTS {
		t.Fatalf("stage hit = %+v", hit)
	}
}

func TestDetectReturnIgnoresDescriptionText(t *testing.T) {
	hit := DetectReturn([]TrackingEvent{{Description: "Return to sender", SubStatus: SubStatusInTransitOther}})
	if hit.Type != "" || !hit.Supported {
		t.Fatalf("hit = %+v", hit)
	}
	if DetectReturn(nil).Supported {
		t.Fatal("empty events should mark unsupported")
	}
}

func TestMapSDKStatus(t *testing.T) {
	if got := MapSDKStatus("Delivered", ""); got != StatusDelivered {
		t.Fatalf("got %s", got)
	}
	if got := MapSDKStatus("Exception", "Exception_Returning"); got != StatusException {
		t.Fatalf("got %s", got)
	}
	if got := MapSDKStatus("in_transit", ""); got != StatusInTransit {
		t.Fatalf("legacy snake_case: got %s", got)
	}
	if got := MapSDKStatus("", "InTransit_PickedUp"); got != StatusInTransit {
		t.Fatalf("sub_status parent: got %s", got)
	}
}
