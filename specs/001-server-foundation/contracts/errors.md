# Contract: Shared JSON Error Envelope

Every error response the server emits — now and in all later features — uses this
single shape, so the future Windows client parses one error format everywhere
(constitution Principle III: consistency on the machine-facing surface).

## Envelope (`pkg/protocol.ErrorResponse`)

```json
{
  "error":   "rate_limited",
  "message": "Too many requests. Please retry shortly."
}
```

| Field     | Type   | Notes |
|-----------|--------|-------|
| `error`   | string | Stable machine-readable code (snake_case). Clients switch on this. |
| `message` | string | Human-readable sentence. Safe to surface to users; contains **no** secrets, stack traces, or internal IDs (constitution Security; Principle III). |

## Codes defined in this feature

| HTTP status | `error` code     | When | Extra headers |
|-------------|------------------|------|---------------|
| `429`       | `rate_limited`   | Global token bucket exhausted (FR-023). | `Retry-After: <seconds>` |
| `500`       | `internal_error` | Unhandled panic caught by recovery middleware. Detail goes to the ERROR log only, never the body. | — |
| `404`       | `not_found`      | Unknown route. | — |
| `405`       | `method_not_allowed` | Known path, wrong method. | `Allow: <methods>` |

## Rules

- The body **never** echoes request-supplied data unsanitized and **never** contains
  a secret or a raw Go error chain (FR-019, constitution Security).
- `Content-Type: application/json` on every error response.
- The recovery middleware logs the panic with stack at ERROR level and returns the
  generic `internal_error` envelope — the client sees a sentence, the operator sees
  the detail in the log (Principle III).
