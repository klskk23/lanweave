# Data Model: Node Online Status

**Feature**: 007-node-online-status | **Date**: 2026-06-05

This feature adds **no persisted schema**. The `nodes` table (migration 0003) is
unchanged; there is no migration 0004. The only new "data" is an in-memory snapshot
derived from the live WireGuard device, plus two new fields on the node API response.

## Derived / runtime entities

### Handshake snapshot (in-memory, `internal/server/status`)

A read-through cache of the kernel's per-peer handshake times. Authoritative source is
the WireGuard device; reconstructed every poll.

| Field | Type | Notes |
|-------|------|-------|
| (key) public key | `string` | WireGuard peer public key (matches `nodes.wg_pubkey`) |
| last handshake | `time.Time` | From `wgtypes.Peer.LastHandshakeTime`; zero value = never handshaked |

- **Lifecycle**: empty at process start → populated by the first poll (immediately on
  `Run`) → refreshed every poll interval → discarded on shutdown. Never written to
  SQLite (FR-007).
- **Derivation rule (online)**: `online(pubkey) = present && lastHandshake != zero &&
  (now - lastHandshake) < Threshold`. Absent key or zero time → offline (FR-003).
- **Constants**: `Threshold = 3 * time.Minute`, `DefaultInterval = 30 * time.Second`
  (DESIGN §6.5; the interval equals the constitution's ≤ 30 s lag budget).

### Tracker (the holder of the snapshot)

| Member | Type | Purpose |
|--------|------|---------|
| snapshot | `map[string]time.Time` guarded by `sync.RWMutex` | current per-peer handshake times |
| source | `func() (map[string]time.Time, error)` | reads the device (bound to `wg.Server.Handshakes`) |
| interval | `time.Duration` | poll cadence (default 30 s; small value in tests) |
| threshold | `time.Duration` | online cutoff (3 min) |
| now | `func() time.Time` | clock seam for deterministic tests (defaults to `time.Now`) |

- **Behavior**: `Run(ctx)` polls once immediately, then on each tick replaces the
  snapshot with the source's result. A source error leaves the previous snapshot in
  place and is logged at WARN; it never panics and never blocks serving (FR-008). On
  `ctx.Done()` the loop returns (FR-009, shutdown ≤ 10 s).
- **Reads**: `Online(pubkey) bool` and `LastHandshake(pubkey) (time.Time, bool)` take
  the read lock; `ok=false` when the key is absent or its time is zero.

## Modified API contract entity

### NodeResponse (`pkg/protocol/node.go`) — two new fields

| Field | JSON | Type | Notes |
|-------|------|------|-------|
| (existing) ID | `id` | int64 | |
| (existing) Name | `name` | string | |
| (existing) IP | `ip` | string | dotted `100.127.x.y` |
| (existing) CreatedAt | `created_at` | string (omitempty) | RFC 3339 |
| **Online** | `online` | bool | **always present**; true iff last handshake within threshold |
| **LastHandshake** | `last_handshake` | string (omitempty) | RFC 3339; **omitted** when the node has never handshaked |

- `online` is always serialized (a definite true/false, never "unknown") so the client
  is unambiguous (FR-002, Principle III).
- `last_handshake` is omitted (not empty-string, not zero) for never-connected nodes so
  the client renders "never"/"—" cleanly (FR-004).

## Relationships & invariants

- Node identity (id, name, ip, pubkey) remains owned by SQLite (003/004). Status is
  matched to a node by **public key** (`nodes.wg_pubkey` ↔ snapshot key). `ListByUser`
  already returns `wg_pubkey`, so no store change is required.
- A peer present on the device with **no** matching node row is ignored for status
  (it simply isn't looked up). A node whose peer is momentarily absent reads as offline
  until the peer/handshake reappears (edge cases in spec).
- Status is **per caller's own nodes** only — the enrichment happens inside the
  existing caller-scoped `GET /nodes`; the zone-members view (005) is untouched (FR-005).
