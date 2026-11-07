# tracking-service

BefShip package-level logistics tracking service: provider-agnostic ingest, PostgreSQL hot store, optional ClickHouse OLAP, and optional S3-compatible cold archive.

**Scope:** `package` ↔ `tracking_event` only. No `shipment` entity — callers pass `external_ref` for order/shipment correlation upstream.

## Architecture

```
Upstream caller ── POST /v1/tracking-events (tracking payload)
        │
        ▼  (synchronous; one transaction per tracking number holding the package row lock;
        │   200 only after commit, retry-safe via event fingerprint)
  PostgreSQL (package + tracking_event)
        ├──► OLAP Writer (ClickHouse), newly inserted events only   [OLAP_ENABLED=true]
        └──► Daily archive → S3 (COS via S3 API) (delivered > 90d)   [ARCHIVE_ENABLED=true]
```

- **Hot:** PG holds open packages + events for packages delivered within retention.
- **Cold:** Archived event JSON for single-package lookup after retention.
- **OLAP:** Full event stream for BI; abstracted as `olap.Writer` (noop when disabled).

## Quick start

```bash
# Postgres required
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable'
export OLAP_ENABLED=false
export ARCHIVE_ENABLED=false

make run
# listen :8092
```

```bash
# Register a package
curl -s -X POST localhost:8092/v1/packages -d '{
  "tracking_number":"1Z999",
  "carrier_code":"ups",
  "provider_name":"carrier_direct",
  "external_ref":"order-1001",
  "origin":{"country":"CN","city":"Shenzhen"},
  "destination":{"country":"US","city":"Los Angeles"}
}'

# Create tracking events
curl -s -X POST localhost:8092/v1/tracking-events -d '{
  "tracking_number":"1Z999",
  "update_time":"2026-11-07T06:00:00Z",
  "status":"in_transit",
  "sub_status":"in_transit_picked_up",
  "events":[
    {"event_time":"2026-11-07T05:00:00Z","stage":"picked_up","location":"SZ","description":"Picked up","status_code":"PU",
     "address":{"country":"CN","city":"Shenzhen"}}
  ]
}'

# Query events (routes to PG or cold archive)
curl -s localhost:8092/v1/packages/1Z999/events
```

## Environment

| Variable | Default | Notes |
|----------|---------|-------|
| `HTTP_ADDR` | `:8092` | |
| `API_KEY` | — | when set, `/v1/*` requires `Authorization: Bearer <key>` or `X-API-Key: <key>` |
| `DATABASE_URL` | local postgres | hot store; pool size via DSN params, e.g. `?pool_max_conns=20` |
| `LOG_LEVEL` | `info` | slog JSON; every line logged during a request carries `request_id` |
| `OLAP_ENABLED` | `false` | master switch for analytics sink |
| `OLAP_DRIVER` | `clickhouse` | only clickhouse today |
| `CLICKHOUSE_DSN` | — | required when OLAP on |
| `CLICKHOUSE_TABLE` | `tracking_event_olap` | |
| `CLICKHOUSE_ASYNC_INSERT` | `true` | server-side buffering of the small per-request inserts |
| `ARCHIVE_ENABLED` | `false` | master switch for cold archive |
| `ARCHIVE_RETENTION_DAYS` | `90` | delivered packages older than this are archived |
| `ARCHIVE_BATCH_SIZE` | `200` | packages per round; a run loops until nothing is due |
| `ARCHIVE_SCHEDULE` | `03:00` | daily `HH:MM`; `off` disables the in-process scheduler |
| `ARCHIVE_TIMEZONE` | `Asia/Shanghai` | IANA zone for `ARCHIVE_SCHEDULE` |
| `S3_ENDPOINT` | — | e.g. COS `https://cos.ap-shanghai.myqcloud.com` |
| `S3_REGION` | `ap-shanghai` | |
| `S3_BUCKET` | — | required when archive on |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | — | required when archive on; or `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` |

## Tracking payload

`POST /v1/tracking-events` accepts the generic payload below (or `{"batches":[...]}` for several tracking numbers). Receiving vendor webhooks and converting them to this format is the caller's job. All times are RFC3339; omitted fields keep their current value.

| Field | Stored as |
|-------|-----------|
| `tracking_number` | required; package must be registered first |
| `carrier_code` | `package.carrier_code` |
| `update_time` | `package.latest_update_at`. A payload older than the stored value still stores its events but does not change package fields. When omitted, the payload is always applied and `latest_update_at` is left as is |
| `status` / `sub_status` / `sub_status_desc` | `package.status` (enum below) / free-form sub-status |
| `origin`, `destination` `{country, state, city, postal_code}` | `package.origin_*`, `package.destination_*` |
| `service_type`, `pieces`, `reference_number` | same-name package columns |
| `last_mile {tracking_number, carrier_code}` | `package.last_mile_*` |
| `estimated_delivery {from, to}` | `package.estimated_delivery_from/to` |
| `milestones[] {stage, time}` | `package.milestones` (JSONB); `picked_up` / `delivered` also fill `picked_up_at` / `delivered_at` |
| `events[] {event_time, carrier_code, stage, sub_status, status_code, description, location, address, raw}` | `tracking_event` rows; latest one also denormalized to `package.latest_event_*` |

**`status`:** `registered` (before any update), `not_found`, `info_received`, `in_transit`, `expired`, `available_for_pickup`, `out_for_delivery`, `delivery_failure`, `delivered`, `exception`.

**`stage`** (milestones and events): `info_received`, `picked_up`, `departure`, `arrival`, `available_for_pickup`, `out_for_delivery`, `delivered`, `returning`, `returned`. An event with a `stage` fills that milestone if it isn't set yet; a `delivered` milestone without an explicit `status` sets `status=delivered`.

**`sub_status`** is not validated; recommended values are `<status>_<reason>`, e.g. `in_transit_picked_up`, `in_transit_customs_processing`, `delivery_failure_invalid_address`, `exception_returned`, `exception_lost`.

`provider_name` is set at package registration and not overwritten by events. Street addresses and coordinates are intentionally not stored.

## Tenants

Every request may carry `X-Tenant-ID` (`[A-Za-z0-9._-]{1,64}`); without it the tenant is `default`. Tracking numbers are unique per tenant, and every read, write, OLAP row and archive key is scoped by tenant.

## API

| Method & path | Purpose |
|---------------|---------|
| `GET /health` | Liveness (no dependencies checked) |
| `GET /ready` | Readiness: `503` while PostgreSQL is unreachable |
| `GET /metrics` | Prometheus metrics |
| `POST /v1/packages` | Register a package |
| `GET /v1/packages` | List packages (newest first) |
| `GET /v1/packages/{trackingNumber}/events` | Package + events (PG or cold archive) |
| `POST /v1/tracking-events` | Ingest tracking payload |
| `GET /v1/analytics/events` | OLAP event detail |
| `GET /v1/analytics/stats` | OLAP aggregates |
| `POST /v1/admin/archive` | Run archive now |

`/health`, `/ready` and `/metrics` never require `API_KEY`. `X-Request-ID` is echoed back (generated when absent). `500` responses only carry `{"error": "internal error", "request_id": "..."}`; the details are in the log line with the same `request_id`.

**`POST /v1/tracking-events`** processes each tracking number in its own transaction and returns one result per batch:

```json
{"ingested": 1, "failed": 1, "results": [
  {"tracking_number": "1Z999", "status": "ok"},
  {"tracking_number": "1Z000", "status": "not_found", "error": "package not registered"}
]}
```

Status is `200` when all succeeded, `207` when some failed, otherwise `404` (all unknown) or `500`. Failed batches are safe to resend.

**`GET /v1/packages`** query params: `status` (comma list), `carrier_code`, `provider_name`, `external_ref`, `origin_country`, `destination_country`, `is_archived`, `created_from` / `created_to` (RFC3339), `limit` (default 50, max 200), `cursor`. Response is `{"items": [...], "next_cursor": "..."}`; pass `next_cursor` back as `cursor` until it is empty.

```bash
curl -s -H 'X-Tenant-ID: acme' 'localhost:8092/v1/packages?status=in_transit,exception&limit=20'
```

**Analytics** (need `OLAP_ENABLED=true`, otherwise `409`). Shared filters: `from` / `to` (RFC3339), `tracking_number`, `carrier_code`, `event_carrier_code`, `provider_name`, `package_status`, `stage`, `sub_status`, `origin_country`, `destination_country`, `country` (event location).

- `GET /v1/analytics/events`: defaults to the last 7 days; `limit` (default 100, max 1000), `offset` (max 10000); newest first.
- `GET /v1/analytics/stats`: defaults to the last 30 days; `group_by` takes 1–3 of `day`, `month`, `carrier_code`, `event_carrier_code`, `provider_name`, `package_status`, `stage`, `sub_status`, `origin_country`, `destination_country`, `country`, `service_type`; `limit` (default 1000, max 10000). Each row returns `dims`, `events` and distinct `packages`.

```bash
curl -s 'localhost:8092/v1/analytics/stats?group_by=day,carrier_code&stage=delivered'
```

`package_status` in OLAP is the package status at the time the event was written, not the current status. Only events newly inserted into PostgreSQL are sent to OLAP, so payload replays do not inflate counts; queries skip `FINAL` for speed.

## OLAP

```go
// internal/olap.Writer
EnsureSchema / InsertEvents / Close
// internal/olap.Reader
QueryEvents / Stats
```

- `OLAP_ENABLED=false` → `olap.Noop` (no network).
- `OLAP_ENABLED=true` + `OLAP_DRIVER=clickhouse` → TCHouse-C / ClickHouse `ReplacingMergeTree`, partitioned by month, ordered by `(tenant_id, event_time, package_id, fingerprint)`.
- OLAP is written after the PostgreSQL commit with a 5s timeout. A failed write is logged and counted in `tracking_olap_insert_failures_total`; there is no automatic backfill.

## Database migrations

PostgreSQL schema lives in `internal/store/postgres/migrations/*.sql` ([goose](https://github.com/pressly/goose) format, embedded in the binary) and is applied on startup under a Postgres session lock, so replicas starting together migrate once. Add a new numbered file for every schema change; never edit an applied one. The ClickHouse table is still created with `CREATE TABLE IF NOT EXISTS`.

## Metrics

| Metric | Meaning |
|--------|---------|
| `tracking_http_requests_total{method,route,code}` | requests per route pattern |
| `tracking_http_request_duration_seconds{method,route}` | latency histogram |
| `tracking_events_inserted_total` | new events stored (duplicates excluded) |
| `tracking_olap_insert_failures_total` | events missing from analytics |
| `tracking_packages_archived_total` | packages moved to cold storage |

## Cold archive via S3 (Tencent COS)

Core only speaks S3. With `ARCHIVE_ENABLED=false`, nothing is archived and `POST /v1/admin/archive` returns `409`. Point the endpoint at COS’s S3-compatible gateway:

```bash
export ARCHIVE_ENABLED=true
export S3_ENDPOINT=https://cos.ap-shanghai.myqcloud.com
export S3_REGION=ap-shanghai
export S3_BUCKET=your-bucket-appid
export S3_ACCESS_KEY=...
export S3_SECRET_KEY=...
```

Same client works with AWS S3 (leave `S3_ENDPOINT` empty) or MinIO. Objects are written to `tracking-events/<tenant>/<yyyy/mm/dd>/<tracking_number>.json`.

## Jobs

When archive is enabled, each instance schedules a run daily at `ARCHIVE_SCHEDULE` (`ARCHIVE_TIMEZONE`). A run takes a Postgres advisory lock, so with several replicas only one archives at a time and the rest skip. `POST /v1/admin/archive` triggers the same run synchronously and returns `409` if one is already in progress.

Each package is archived in its own transaction under the package row lock, so an ingest arriving at the same moment either lands before the snapshot or waits and then unarchives the package.

On `SIGTERM` the service stops accepting connections, waits up to 25s for in-flight requests, lets a running archive job stop after its current package, then closes connections.

(planned) reconcile job: provider poll to backfill missed events.

## Release (Tencent CCR)

`.github/workflows/build-push-image.yml` runs `go vet` + `go test`, then builds a multi-arch (`linux/amd64,linux/arm64`) image and pushes it to `ccr.ccs.tencentyun.com/solat/tracking-service`.

- **Trigger:** publish a GitHub Release with a semver tag (e.g. `v1.2.3`) → pushes `:v1.2.3`, `:1.2.3`, and `:latest` (not for pre-releases).
- **Manual:** Actions → *Build and push image* → enter a tag; `push` defaults to `false` (build-only check).
- **Repo secrets:** `TENCENT_REGISTRY_USERNAME`, `TENCENT_REGISTRY_PASSWORD`.

```bash
docker pull ccr.ccs.tencentyun.com/solat/tracking-service:v1.2.3
```

## Layout

```
cmd/api/                 process entry
internal/domain/         Package, TrackingEvent
internal/store/          Store port + postgres (+ migrations/)
internal/olap/           Writer port + noop + ClickHouse adapter
internal/archive/        ColdStore port + S3 (COS via S3 endpoint)
internal/provider/       generic tracking payload → EventBatch
internal/service/        ingest, query, archive/unarchive
internal/server/         HTTP, API key, request log/metrics middleware
internal/metrics/        Prometheus collectors
internal/reqid/          request id in context and slog
```
