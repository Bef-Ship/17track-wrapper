package config

import "testing"

func TestEnvBool(t *testing.T) {
	t.Setenv("CFG_BOOL_TEST", "true")
	if !EnvBool("CFG_BOOL_TEST", false) {
		t.Fatal("expected true")
	}
	t.Setenv("CFG_BOOL_TEST", "0")
	if EnvBool("CFG_BOOL_TEST", true) {
		t.Fatal("expected false")
	}
	t.Setenv("CFG_BOOL_TEST", "")
	if !EnvBool("CFG_BOOL_TEST", true) {
		t.Fatal("empty should use fallback true")
	}
}

func TestEventTaskType(t *testing.T) {
	t.Setenv("EVENT_TASK_TYPE", "")
	if got := EventTaskType(); got != "tracking.event" {
		t.Fatalf("default = %q", got)
	}
	t.Setenv("EVENT_TASK_TYPE", "befship.event")
	if got := EventTaskType(); got != "befship.event" {
		t.Fatalf("override = %q", got)
	}
}

func TestEnableMock(t *testing.T) {
	t.Setenv("ENABLE_MOCK", "")
	if EnableMock() {
		t.Fatal("default false")
	}
	t.Setenv("ENABLE_MOCK", "true")
	if !EnableMock() {
		t.Fatal("expected true")
	}
}
