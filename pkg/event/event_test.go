package event

import (
	"encoding/json"
	"testing"
)

func TestScopeValidateV1(t *testing.T) {
	if err := ExternalWebhook("demo").Validate(); err != nil {
		t.Fatal(err)
	}
	if err := InternalBiz("demo").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := Scope{SourceType: SourceExternal, Scene: SceneBiz}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected illegal external+biz")
	}
	bad = Scope{SourceType: SourceInternal, Scene: SceneWebhook}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected illegal internal+webhook")
	}
}

func TestChannel(t *testing.T) {
	ch, err := Channel(SourceExternal, "wechat")
	if err != nil || ch != "external.wechat" {
		t.Fatalf("got %q %v", ch, err)
	}
	if err := ValidateChannel(ch, SourceExternal); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChannel("internal.user", SourceExternal); err == nil {
		t.Fatal("expected mismatch")
	}
	if _, err := Channel(SourceExternal, "we chat"); err == nil {
		t.Fatal("expected invalid source")
	}
}

func TestWrapPassthroughJSON(t *testing.T) {
	raw := []byte(`{"EventName":"user_add","id":1}`)
	env, err := Wrap("external.wechat", ExternalWebhook("t1"), raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(env.RawEvent) != string(raw) {
		t.Fatalf("raw mutated: %s", env.RawEvent)
	}
	if env.EventID == "" || env.Timestamp == "" {
		t.Fatal("missing eventId/timestamp")
	}
}

func TestWrapPassthroughXML(t *testing.T) {
	xml := `<xml><Event>subscribe</Event></xml>`
	env, err := Wrap("external.wechat", ExternalWebhook(""), xml)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := json.Unmarshal(env.RawEvent, &s); err != nil {
		t.Fatal(err)
	}
	if s != xml {
		t.Fatalf("xml mutated: %s", s)
	}
}
