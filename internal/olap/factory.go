package olap

import (
	"fmt"
	"log/slog"

	"github.com/SolaTyolo/tracking-service/internal/config"
)

// Open builds the OLAP Writer from config. When disabled, returns Noop.
func Open(cfg config.Config) (Writer, error) {
	if !cfg.OLAPEnabled {
		slog.Info("olap disabled", "env", "OLAP_ENABLED=false")
		return Noop{}, nil
	}
	switch cfg.OLAPDriver {
	case "", "clickhouse":
		if cfg.ClickHouse.DSN == "" {
			return nil, fmt.Errorf("OLAP_ENABLED=true requires CLICKHOUSE_DSN")
		}
		w, err := OpenClickHouse(cfg.ClickHouse.DSN, cfg.ClickHouse.Table, cfg.ClickHouse.AsyncInsert)
		if err != nil {
			return nil, err
		}
		slog.Info("olap enabled", "driver", "clickhouse", "table", cfg.ClickHouse.Table,
			"async_insert", cfg.ClickHouse.AsyncInsert)
		return w, nil
	default:
		return nil, fmt.Errorf("unsupported OLAP_DRIVER %q", cfg.OLAPDriver)
	}
}
