package s3store

import "testing"

func TestNormalizeKey(t *testing.T) {
	got, err := NormalizeKey(" tracking-events/TN/1.json ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tracking-events/TN/1.json" {
		t.Fatalf("got %q", got)
	}
	if _, err := NormalizeKey("../etc/passwd"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := NormalizeKey(""); err == nil {
		t.Fatal("expected empty error")
	}
}

func TestNewUnconfigured(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Configured() {
		t.Fatal("empty config should be unconfigured")
	}
}
