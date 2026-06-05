# Contract: Health Check Endpoint

## `GET /api/v1/healthz`

Liveness/readiness probe. The only endpoint this feature exposes.

### Request

- **Method**: `GET`
- **Path**: `/api/v1/healthz`
- **Auth**: none (FR-010 — no authentication required)
- **Body**: none

### Behavior

- Returns `200 OK` once the server has finished startup (config loaded, migrations
  applied, admin bootstrap complete, TLS listener accepting). Before that point the
  process is not yet serving, so the probe simply fails to connect.
- Subject to the global rate limiter (FR-021, **not exempt** — spec decision). Under
  a flood it can return `429`; probes should be configured below the configured rate.

### Response — 200

- **Content-Type**: `application/json`
- **Body** (`pkg/protocol.HealthResponse`):

```json
{
  "status": "ok",
  "version": "0.1.0+<git-short-sha>"
}
```

| Field     | Type   | Notes |
|-----------|--------|-------|
| `status`  | string | Always `"ok"` when 200. Reserved for future `"degraded"` states. |
| `version` | string | Build version string injected at compile time via `-ldflags`. |

### Response — 429 (rate limited)

See [errors.md](./errors.md). Includes `Retry-After` header.

### Acceptance criteria (maps to spec)

| ID | Check |
|----|-------|
| US1-1 | After startup, `GET /api/v1/healthz` over HTTPS returns 200 within 5 s. |
| US1 (TLS) | Endpoint is reachable **only** over HTTPS; no plaintext HTTP listener exists. |
| US3-1 | Each request produces one INFO log line with method, path, status, duration. |
| SC-002 | Cold start to first 200 ≤ 3 s. |
| SC-005 | Response and logs are valid JSON. |
