# 17track-wrapper

Service wrapping **17TRACK**: package registration, webhook ingest, lifecycle jobs, and hot/cold event storage. Request fields follow 17TRACK names (`number`, `carrier`, `lang`, `order_no`, …); `carrier` is always the 17TRACK carrier key (`GET /v1/carriers`, 0 = auto-detect).

Module: `github.com/SolaTyolo/17track-wrapper` · Repo: https://github.com/SolaTyolo/17track-wrapper

Schema `tracking.*` is owned by this service (like GoTrue for `auth`): embedded SQL under `migrations/` is applied on startup into `tracking.schema_migrations`.

## Run

One binary, `cmd/tracking`, with a role (`TRACKING_ROLE=api|worker|all`; unset → `worker` when invoked as `tracking-worker`, else `api`):

```bash
cp .env.example .env
go run ./cmd/tracking                        # api: HTTP API
TRACKING_ROLE=worker go run ./cmd/tracking   # worker: cron jobs + asynq consumers
TRACKING_ROLE=all go run ./cmd/tracking      # both in one process (local dev)
```

The worker runs the jobs in-process (sync stale TRACKING packages, stop stale, retrack, retry stuck registers, archive) and consumes 17TRACK pushes, register retries and shipment callbacks from Redis, so it needs the same DB / 17TRACK / archive env as the API.

## HTTP API

Contract: [`api/openapi.yaml`](api/openapi.yaml), also served at `GET /openapi.yaml`. On every push to `main` that touches the spec, [Docs](.github/workflows/docs.yml) publishes a Redoc site to GitHub Pages (`https://<owner>.github.io/<repo>/` after enabling Pages → Source: GitHub Actions once). Responses use the 17TRACK envelope `{code, message, data}` (`code: 0` = success) and 17TRACK error codes. Batch calls return `data: {accepted, rejected}`, where each rejected item carries `error: {code, message}`; 17TRACK's own rejections are passed through. Business errors are HTTP 200 with `code != 0`, as in 17TRACK. Only an invalid token (401), internal errors (500) and unavailable archives (503) use other statuses.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/packages/register` | Register up to 40 numbers: `items[]` are 17TRACK `/register` items plus `external_ref`, `new_generation`. An unfinished number is rejected with `-18019901` |
| `POST` | `/v1/packages/cancel` | Stop tracking `items[]` of `{number}` (17TRACK `/stoptrack`) |
| `POST` | `/v1/packages/refresh` | Pull latest from 17TRACK `{number}` (restores from archive first) |
| `POST` | `/v1/packages/change-carrier` | `items[]` are 17TRACK `/changecarrier` items |
| `POST` | `/v1/packages/restore` | Move an archived generation's events back to hot storage `{package_id}` |
| `GET` | `/v1/packages` | List current generations (`include_superseded=true` for all, `carrier=<key>`) |
| `GET` | `/v1/packages/events` | One generation + events: `?package_id=`, `?number=&external_ref=` or `?number=` (current); archived ones are read from cold storage |
| `GET` | `/v1/packages/history` | Every generation of a number, newest first (`&events=true` to include events) |
| `GET` | `/v1/packages/route` | GeoJSON route from stored event coordinates (same lookup as events) |
| `GET` | `/v1/carriers` | 17TRACK carrier keys |
| `POST` | `/v1/webhooks/17track` | 17TRACK push (sync fallback; normally via the event bus). Non-2xx on failure so the gateway retries |
| `GET` | `/healthz`, `/readyz`, `/openapi.yaml` | Liveness / readiness (DB ping) / contract (no auth) |

**Service auth:** `TRACKING_SERVICE_TOKENS=backend:<tok>,gateway:<tok>` gives each calling service its own token. Send it as the `17token` header (as with 17TRACK) or as `Authorization: Bearer <tok>`. Without a valid token the response is HTTP 401 with `code: -18010002`. The legacy `TRACKING_API_TOKEN` still works as the token named `default`. In production the API refuses to start without any token.

## Recycled tracking numbers

Carriers reuse numbers, so each `tracking.package` row is one **generation** of a number; only the current one (`superseded_at IS NULL`) is unique. Registering a number whose current generation is finished (delivered and no longer tracked, cancelled, or archived) for a different `external_ref` — or archived at all, or with `new_generation: true` — deletes the old 17TRACK registration, supersedes the old row and creates a new one. Webhooks, polling and jobs only touch current generations. Older ones stay queryable by `package_id` (returned by register and every callback, so upstream should store it), by `number` + `external_ref`, or through `/v1/packages/history`.

## Archive

With `ARCHIVE_ENABLED=true`, the worker moves the events of inactive packages (not `REGISTERING` / `TRACKING`) untouched for `ARCHIVE_RETENTION_DAYS` (default 100) to obj-gateway at `tracking-events/<number>/<package_id>.json` and deletes the hot event rows. The package row itself (with `archive_key`) stays in Postgres, so every lookup above still resolves the generation. Archived events stay readable without restoring; `POST /v1/packages/restore` (or a refresh) brings them back and restarts the retention clock.

## Shipment callback

Set `SHIPMENT_CALLBACK_URL` to POST package updates to the backend (header `X-Tracking-Callback-Secret`). Updates are sent only when events were added or status / sub_status / subscription / carrier / return type changed. The payload carries `package_id`, `carrier`, the full `package`, `new_events`, the raw 17TRACK `parsed` detail and `occurred_at` (lets the receiver drop out-of-order deliveries). With Redis the API enqueues on `internal.shipment_callback` and the worker delivers with retries.

## CI / Release

| Workflow | Trigger | Purpose |
| --- | --- | --- |
| `CI` | PR / push `main` | gofmt, vet, golangci-lint, race tests, build |
| `Docs` | push `main` (openapi) / manual | publish Redoc site to GitHub Pages |
| `Release` | push `main` / GitHub Release / manual | build & push `ccr…/solat/tracking:<tag>` |

Repo secrets: `CCR_USERNAME`, `CCR_PASSWORD`.
