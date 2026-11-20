package event

import "fmt"

// SourceType is who produced the event (production subject).
type SourceType string

const (
	SourceExternal SourceType = "external"
	SourceInternal SourceType = "internal"
)

// Scene is the technical path that produced the event.
type Scene string

const (
	SceneWebhook   Scene = "webhook"
	SceneBiz       Scene = "biz"
	SceneScheduled Scene = "scheduled"
)

// Scope is envelope metadata. It must not be mixed into rawEvent.
type Scope struct {
	TenantID   string     `json:"tenantId"`
	SourceType SourceType `json:"sourceType"`
	Scene      Scene      `json:"scene"`
}

// ExternalWebhook is the V1-legal scope for third-party HTTP callbacks.
func ExternalWebhook(tenantID string) Scope {
	return Scope{TenantID: tenantID, SourceType: SourceExternal, Scene: SceneWebhook}
}

// InternalBiz is the V1-legal scope for in-process Publish from our services.
func InternalBiz(tenantID string) Scope {
	return Scope{TenantID: tenantID, SourceType: SourceInternal, Scene: SceneBiz}
}

// Validate enforces V1 legal combinations. Other pairs are rejected until V2 rules land.
func (s Scope) Validate() error {
	switch s.SourceType {
	case SourceExternal:
		if s.Scene != SceneWebhook {
			return fmt.Errorf("event scope: sourceType=%s requires scene=%s, got %s", s.SourceType, SceneWebhook, s.Scene)
		}
	case SourceInternal:
		if s.Scene != SceneBiz {
			return fmt.Errorf("event scope: sourceType=%s requires scene=%s, got %s", s.SourceType, SceneBiz, s.Scene)
		}
	default:
		return fmt.Errorf("event scope: unknown sourceType %q", s.SourceType)
	}
	return nil
}
