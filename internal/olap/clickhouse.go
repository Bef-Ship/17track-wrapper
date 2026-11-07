package olap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ClickHouseWriter persists tracking events into ClickHouse / TCHouse-C.
type ClickHouseWriter struct {
	conn        driver.Conn
	table       string
	asyncInsert bool
}

// OpenClickHouse connects using a ClickHouse DSN (clickhouse://user:pass@host:9000/db).
// asyncInsert lets the server buffer the small per-request inserts into larger parts.
func OpenClickHouse(dsn, table string, asyncInsert bool) (*ClickHouseWriter, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse dsn: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}
	if table == "" {
		table = "tracking_event_olap"
	}
	return &ClickHouseWriter{conn: conn, table: table, asyncInsert: asyncInsert}, nil
}

func (w *ClickHouseWriter) Name() string { return "clickhouse" }

func (w *ClickHouseWriter) EnsureSchema(ctx context.Context) error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s
(
    tenant_id LowCardinality(String),
    package_id UInt64,
    tracking_number String,
    carrier_code String,
    provider_name String,
    external_ref String,
    service_type LowCardinality(String),
    origin_country LowCardinality(String),
    destination_country LowCardinality(String),
    package_status LowCardinality(String),
    picked_up_at Nullable(DateTime64(3, 'UTC')),
    delivered_at Nullable(DateTime64(3, 'UTC')),
    event_time DateTime64(3, 'UTC'),
    event_carrier_code LowCardinality(String),
    stage LowCardinality(String),
    sub_status LowCardinality(String),
    status_code String,
    location String,
    country LowCardinality(String),
    description String,
    fingerprint String,
    created_at DateTime DEFAULT now()
)
ENGINE = ReplacingMergeTree(created_at)
PARTITION BY toYYYYMM(event_time)
ORDER BY (tenant_id, event_time, package_id, fingerprint)
`, w.table)
	if err := w.conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	slog.InfoContext(ctx, "olap clickhouse schema ready", "table", w.table)
	return nil
}

func (w *ClickHouseWriter) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	if w.asyncInsert {
		// wait_for_async_insert=1 still reports failures to the caller.
		ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
			"async_insert":          1,
			"wait_for_async_insert": 1,
		}))
	}
	batch, err := w.conn.PrepareBatch(ctx, fmt.Sprintf(`
INSERT INTO %s (
    tenant_id, package_id, tracking_number, carrier_code, provider_name, external_ref,
    service_type, origin_country, destination_country, package_status, picked_up_at, delivered_at,
    event_time, event_carrier_code, stage, sub_status, status_code, location, country, description,
    fingerprint
)`, w.table))
	if err != nil {
		return err
	}
	for _, ev := range events {
		if err := batch.Append(
			ev.TenantID,
			uint64(ev.PackageID),
			ev.TrackingNumber,
			ev.CarrierCode,
			ev.ProviderName,
			ev.ExternalRef,
			ev.ServiceType,
			ev.OriginCountry,
			ev.DestCountry,
			ev.PackageStatus,
			ev.PickedUpAt,
			ev.DeliveredAt,
			ev.EventTime.UTC(),
			ev.EventCarrierCode,
			ev.Stage,
			ev.SubStatus,
			ev.StatusCode,
			ev.Location,
			ev.Country,
			ev.Description,
			ev.Fingerprint,
		); err != nil {
			return err
		}
	}
	if err := batch.Send(); err != nil {
		return err
	}
	slog.DebugContext(ctx, "olap clickhouse insert", "count", len(events), "table", w.table)
	return nil
}

func (w *ClickHouseWriter) Close() error {
	return w.conn.Close()
}

// where renders the filter as a WHERE clause with positional args.
// Rows are not FINAL-deduplicated; duplicates are rare and merge away eventually.
func (f EventFilter) where() (string, []any) {
	conds := []string{"tenant_id = ?", "event_time >= ?", "event_time < ?"}
	args := []any{f.TenantID, f.From, f.To}
	for _, c := range []struct{ col, v string }{
		{"tracking_number", f.TrackingNumber},
		{"carrier_code", f.CarrierCode},
		{"event_carrier_code", f.EventCarrierCode},
		{"provider_name", f.ProviderName},
		{"package_status", f.PackageStatus},
		{"stage", f.Stage},
		{"sub_status", f.SubStatus},
		{"origin_country", f.OriginCountry},
		{"destination_country", f.DestinationCountry},
		{"country", f.Country},
	} {
		if c.v != "" {
			conds = append(conds, c.col+" = ?")
			args = append(args, c.v)
		}
	}
	return strings.Join(conds, " AND "), args
}

func (w *ClickHouseWriter) QueryEvents(ctx context.Context, q EventQuery) ([]Event, error) {
	where, args := q.where()
	rows, err := w.conn.Query(ctx, fmt.Sprintf(`
SELECT tenant_id, package_id, tracking_number, carrier_code, provider_name, external_ref,
       service_type, origin_country, destination_country, package_status, picked_up_at, delivered_at,
       event_time, event_carrier_code, stage, sub_status, status_code, location, country, description,
       fingerprint
FROM %s
WHERE %s
ORDER BY event_time DESC, package_id DESC
LIMIT ? OFFSET ?`, w.table, where), append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var ev Event
		var packageID uint64
		if err := rows.Scan(
			&ev.TenantID, &packageID, &ev.TrackingNumber, &ev.CarrierCode, &ev.ProviderName, &ev.ExternalRef,
			&ev.ServiceType, &ev.OriginCountry, &ev.DestCountry, &ev.PackageStatus, &ev.PickedUpAt, &ev.DeliveredAt,
			&ev.EventTime, &ev.EventCarrierCode, &ev.Stage, &ev.SubStatus, &ev.StatusCode, &ev.Location, &ev.Country,
			&ev.Description, &ev.Fingerprint,
		); err != nil {
			return nil, err
		}
		ev.PackageID = int64(packageID)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// statsExpr maps StatsDimensions to SQL; every dimension is rendered as String.
var statsExpr = map[string]string{
	"day":                 "toString(toDate(event_time))",
	"month":               "formatDateTime(event_time, '%Y-%m')",
	"carrier_code":        "carrier_code",
	"event_carrier_code":  "toString(event_carrier_code)",
	"provider_name":       "provider_name",
	"package_status":      "toString(package_status)",
	"stage":               "toString(stage)",
	"sub_status":          "toString(sub_status)",
	"origin_country":      "toString(origin_country)",
	"destination_country": "toString(destination_country)",
	"country":             "toString(country)",
	"service_type":        "toString(service_type)",
}

func (w *ClickHouseWriter) Stats(ctx context.Context, q StatsQuery) ([]StatsRow, error) {
	if len(q.GroupBy) == 0 {
		return nil, fmt.Errorf("group_by required")
	}
	selects := make([]string, 0, len(q.GroupBy))
	order := "events DESC"
	for _, dim := range q.GroupBy {
		expr, ok := statsExpr[dim]
		if !ok {
			return nil, fmt.Errorf("unknown group_by dimension %q", dim)
		}
		selects = append(selects, expr+" AS "+dim)
		if dim == "day" || dim == "month" {
			order = dim + " ASC, events DESC"
		}
	}
	where, args := q.where()
	rows, err := w.conn.Query(ctx, fmt.Sprintf(`
SELECT %s, count() AS events, uniqExact(package_id) AS packages
FROM %s
WHERE %s
GROUP BY %s
ORDER BY %s
LIMIT ?`, strings.Join(selects, ", "), w.table, where, strings.Join(q.GroupBy, ", "), order),
		append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatsRow{}
	for rows.Next() {
		dims := make([]string, len(q.GroupBy))
		dest := make([]any, 0, len(dims)+2)
		for i := range dims {
			dest = append(dest, &dims[i])
		}
		var row StatsRow
		dest = append(dest, &row.Events, &row.Packages)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row.Dims = make(map[string]string, len(dims))
		for i, dim := range q.GroupBy {
			row.Dims[dim] = dims[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
