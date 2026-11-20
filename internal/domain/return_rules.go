package domain

// DetectReturn finds the first return signal in events (17TRACK order: newest first),
// using only 17TRACK's own sub_status / stage codes. CarrierEventID is the event
// fingerprint. Supported is false when there are no events to inspect.
func DetectReturn(events []TrackingEvent) ReturnHit {
	if len(events) == 0 {
		return ReturnHit{}
	}
	for _, ev := range events {
		if t := returnType(ev); t != "" {
			return ReturnHit{Type: t, CarrierEventID: ev.EventFingerprint, Supported: true, MatchedDesc: ev.Description}
		}
	}
	return ReturnHit{Supported: true}
}

func returnType(ev TrackingEvent) string {
	switch ev.SubStatus {
	case SubStatusExceptionReturning:
		return ReturnRTS
	case SubStatusExceptionReturned:
		return ReturnWarehouse
	case SubStatusExceptionRejected, SubStatusDeliveryFailureRejected:
		return ReturnRefused
	}
	switch ev.Stage {
	case StageReturning:
		return ReturnRTS
	case StageReturned:
		return ReturnWarehouse
	}
	return ""
}
