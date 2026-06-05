# Research: Node Online Status

**Feature**: 007-node-online-status | **Date**: 2026-06-05

All Technical Context items were resolvable from DESIGN.md §6.5, the constitution's
performance budgets, and the existing 003/004 code. No open NEEDS CLARIFICATION.

## Decision 1 — Source of the online signal: kernel last-handshake time

- **Decision**: Derive "online" from each WireGuard peer's `LastHandshakeTime`
  (exposed by `wgctrl` via `Device(name).Peers`). A node is online when
  `now - LastHandshakeTime < 3 minutes`; a zero time (peer never handshaked) is
  offline.
- **Rationale**: The kernel already records the last successful handshake per peer;
  it is the ground truth for "is this tunnel live". DESIGN §6.5 specifies exactly this
  (3-minute threshold). No application-level heartbeat is needed.
- **Alternatives considered**:
  - *Application heartbeat / WebSocket ping*: more moving parts, and DESIGN defers
    push to v1.1. Rejected for v1.
  - *Counting bytes / endpoint presence*: noisier and not a real liveness signal;
    handshake recency is the canonical WireGuard liveness indicator.

## Decision 2 — Poll-and-snapshot vs. read-on-request

- **Decision**: A background goroutine polls `Handshakes()` every **30 s** and stores
  an in-memory snapshot (`map[pubkey]time.Time`). `GET /nodes` reads the snapshot
  (O(1) per node), never scanning the device per request.
- **Rationale**: The constitution mandates "Online-status update lag ≤ 30 s", which
  *is* the poll interval — so 30 s is the correct, budget-aligned cadence. Polling
  also bounds device reads to once per interval regardless of request volume,
  protecting the ≤ 100 ms read-endpoint budget (FR-010, SC-006). A reconnect therefore
  surfaces within ≤ 30 s (SC-003) and the data is at most 30 s stale.
- **Alternatives considered**:
  - *Read the device on every `GET /nodes`*: simpler, and freshness would be instant,
    but it couples request latency to a netlink syscall and lets request volume drive
    device load — and it would beat, not meet, the 30 s budget at the cost of the read
    budget. The spec's acceptance ("within 30 s") is written against the poll model.
    Rejected.
  - *Event/netlink subscription*: WireGuard exposes no handshake-event stream; polling
    is the supported mechanism. Rejected (not available).

## Decision 3 — Ephemeral in-memory snapshot vs. a `nodes.last_handshake_at` column

- **Decision**: Keep the snapshot **in memory only**; do not add a DB column.
- **Rationale**: Handshake time is ephemeral kernel state. Persisting it would create
  a second, lag-prone copy that the constitution (Principle I: "derivative state …
  reconstructible from the database … no hidden runtime-only memory") actively
  discourages making authoritative. The snapshot is a read-through cache of the kernel,
  reconstructible at any instant by re-reading the device, so it is *not* authoritative
  hidden state. After a restart it repopulates within one poll (FR-007, SC-005) — no
  stale "online" can survive a restart precisely because nothing is persisted.
- **Alternatives considered**:
  - *`nodes.last_handshake_at` column updated by the poller* (the "可选缓存" option in
    ROADMAP): adds a migration and write traffic every 30 s, and risks a stale value
    being read as authoritative after a crash. Rejected as unnecessary complexity for a
    derived signal.

## Decision 4 — Decoupling: narrow `statusProvider` interface in the API package

- **Decision**: The handler depends on a two-method in-package interface
  (`Online(pubKey string) bool`, `LastHandshake(pubKey string) (time.Time, bool)`),
  satisfied by `*status.Tracker`. `app.Run` constructs the tracker with its source set
  to `wgServer.Handshakes` and injects it via `api.Options.Status`.
- **Rationale**: Keeps `api` independent of `status`/`wg` internals and makes the
  handler unit-testable with a tiny fake provider over a **real** SQLite store — the
  fake is our own abstraction, not a mocked system boundary, so it complies with
  Principle II (which forbids mocking SQLite/nftables/WireGuard, not our own seams).
- **Alternatives considered**:
  - *Handler calls `wg.Server` directly*: couples the API to the data plane and forces
    a privileged kernel device into every handler test. Rejected.

## Decision 5 — Testing the data-plane read without a second WireGuard endpoint

- **Decision**: Integration-test `wg.Server.Handshakes()` against a **real** kernel
  device (privileged, `unshare -rUn`, `RequireNetAdmin`): create the interface, add a
  peer, and assert `Handshakes()` returns that peer's public key with a **zero** time
  (never handshaked → tracker reports offline). The literal `online: true` path (a real
  handshake) is a **manual** quickstart scenario with an actual client, because it
  needs a second live WG endpoint — the same constraint accepted for reachability in
  003–006. The 3-minute/recent online computation is covered deterministically by the
  tracker unit test (injected clock + source).
- **Rationale**: Honors "WireGuard MUST NOT be mocked" for the reader while keeping the
  timing logic deterministic and CI-friendly. The privileged test proves we read real
  kernel handshake data and map it correctly; the unit test proves the threshold math.
- **Alternatives considered**:
  - *Stand up two `wireguard-go`/kernel endpoints and force a handshake in-test*:
    heavy, slow, and flaky for a derived boolean. Deferred to the manual quickstart.

## Resolved Constants (from DESIGN §6.5 + constitution Principle IV)

| Name | Value | Source |
|------|-------|--------|
| Online threshold | 3 minutes | DESIGN §6.5 |
| Poll interval | 30 seconds | DESIGN §6.5 = constitution "lag ≤ 30 s" |
| Client keepalive (client-side, out of scope here) | ≈25 s | DESIGN §6.5 |
