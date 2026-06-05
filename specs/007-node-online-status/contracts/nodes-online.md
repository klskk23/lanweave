# Contract: Node list with online status

**Feature**: 007-node-online-status | **Date**: 2026-06-05

This feature changes **one** existing endpoint's response shape and adds no new
endpoints. Authentication, scoping, and method are unchanged from feature 004.

## `GET /api/v1/nodes` (authenticated; caller's own nodes)

Returns the caller's nodes, each now carrying an `online` flag and an optional
`last_handshake`. The set of nodes and their identity fields are unchanged from 004.

### Response 200 — example

```json
{
  "nodes": [
    {
      "id": 12,
      "name": "laptop",
      "ip": "100.127.0.5",
      "created_at": "2026-06-05T09:00:00Z",
      "online": true,
      "last_handshake": "2026-06-05T09:42:10Z"
    },
    {
      "id": 7,
      "name": "old-desktop",
      "ip": "100.127.0.2",
      "created_at": "2026-06-01T12:00:00Z",
      "online": false,
      "last_handshake": "2026-06-05T09:30:00Z"
    },
    {
      "id": 20,
      "name": "never-connected",
      "ip": "100.127.0.9",
      "created_at": "2026-06-05T09:41:00Z",
      "online": false
    }
  ]
}
```

### Field rules

| Field | Type | Rule |
|-------|------|------|
| `online` | bool | **Always present.** `true` iff the node's last handshake is within 3 minutes of now; otherwise `false`. A never-connected node is `false` (FR-002, FR-003). |
| `last_handshake` | string (RFC 3339) | **Omitted** when the node has never handshaked. Otherwise the most recent handshake time, so the client can show "last seen" (FR-004). |

### Behavioral guarantees (from spec)

- **Freshness**: `online` reflects a connect within ≤ 30 s and a reconnect within
  ≤ 30 s; it flips to `false` within 3 min + 30 s of the last handshake (SC-001/002/003).
- **Robustness**: If the server cannot read the tunnel, the endpoint still returns 200
  with every node `online: false` (and `last_handshake` reflecting the last known
  snapshot, possibly omitted) — never a 5xx for this reason (FR-008, SC-007).
- **Restart**: Immediately after a restart, before the first poll, nodes report
  `online: false`; correct status appears within one poll interval (SC-005).
- **Scope/auth**: Unauthenticated → 401 (unchanged). Only the caller's own nodes are
  returned; no other user's status is exposed here (FR-005).
- **Performance**: The enrichment is an in-memory lookup per node; the endpoint stays
  within the ≤ 100 ms P50 read budget and does not scan the kernel per request
  (FR-010, SC-006).

## Unchanged contracts

- `POST /api/v1/nodes`, `DELETE /api/v1/nodes/{id}` — unchanged. (Registration's 201
  body MAY remain without `online`; a freshly registered node is offline until it
  connects, and the client reads status via `GET /nodes`.)
- `GET /api/v1/zones/{name}/members` (005) — **unchanged**; this feature does not add
  online status to the zone-members view.
