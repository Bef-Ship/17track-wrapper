package config

import (
	"os"
	"testing"
)

func TestLoadOLAPDefaultsOff(t *testing.T) {
	t.Setenv("OLAP_ENABLED", "")
	cfg := Load()
	if cfg.OLAPEnabled {
		t.Fatal("expected OLAP_ENABLED default false")
	}
}

func TestLoadOLAPEnabled(t *testing.T) {
	t.Setenv("OLAP_ENABLED", "true")
	t.Setenv("OLAP_DRIVER", "clickhouse")
	t.Setenv("CLICKHOUSE_TABLE", "events_olap")
	cfg := Load()
	if !cfg.OLAPEnabled {
		t.Fatal("expected OLAP enabled")
	}
	if cfg.ClickHouse.Table != "events_olap" {
		t.Fatalf("table=%s", cfg.ClickHouse.Table)
	}
	_ = os.Unsetenv("OLAP_ENABLED")
}
