# Implementation Plan: Node Online Status

**Branch**: `007-node-online-status` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/007-node-online-status/spec.md`

## Summary

The server periodically (every 30 s) reads each WireGuard peer's last-handshake time
from the live kernel device and keeps an in-memory snapshot keyed by public key. The
existing `GET /api/v1/nodes` is enriched so each node reports `online` (true when its
last handshake is within 3 minutes) and `last_handshake` (RFC 3339, or omitted when
the node has never connected). The status is derived, ephemeral data — never written
to SQLite — and is repopulated within one poll after a restart. No new endpoint, no
schema migration; the work is a small status tracker plus a handler enrichment.

## Technical Context

**Language/Version**: Go 1.26 (module `lanweave`)

**Primary Dependencies**: `golang.zx2c4.com/wireguard/wgctrl` (+ `wgtypes`) to read
per-peer `LastHandshakeTime`; standard library (`sync`, `time`, `context`,
`log/slog`, `net/http`). No new third-party dependency.

**Storage**: None added. Online status is derived from the live WireGuard device; it
is **not** persisted (no migration; `nodes` table unchanged). SQLite remains the
source of truth for node identity (003/004).

**Testing**: `go test`. Unit (table-driven) for the tracker's online computation and
snapshot refresh (a function-typed source + injectable clock — no system mock) and
for the handler enrichment over a **real** SQLite store with a fake in-package status
provider. Integration (privileged, real kernel via `unshare -rUn`,
`testutil.RequireNetAdmin`) for `wg.Server.Handshakes()` reading real peer handshake
data. Acceptance/smoke: quickstart with a real handshaking client (manual for the
literal `online=true`, consistent with 003–006).

**Target Platform**: Linux server (root / `CAP_NET_ADMIN`); reads the kernel WG device.

**Project Type**: Single Go project (server). Reuses the existing
`internal/server/...` layout.

**Performance Goals**: Online-status update lag ≤ 30 s (Principle IV) — the poll
interval. `GET /nodes` stays an in-memory map lookup per node (no per-request kernel
scan), keeping the read endpoint within the ≤ 100 ms P50 budget.

**Constraints**: Single instance; IPv4 only. Graceful shutdown ≤ 10 s — the poll
goroutine stops on context cancellation. No secrets in logs (handshake times and
public keys are not secret).

**Scale/Scope**: Up to ~1000 nodes; one `Handshakes()` read returns all peers in a
single netlink round-trip; snapshot is a `map[string]time.Time`.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**: One new small package `internal/server/status` (single
  responsibility: track per-peer online state) plus one new method on `wg.Server`
  (`Handshakes`) and an enrichment of `listNodes`. The snapshot is a read-through
  cache of kernel state, fully reconstructible by re-reading the device — it holds no
  authoritative state and adds no "hidden runtime-only memory" (the kernel device is
  the source, SQLite still owns node identity). No premature abstraction: the handler
  depends on a narrow in-package `statusProvider` interface (two methods). `gofmt` /
  `go vet` / `staticcheck` clean; errors as values; config-free (threshold/interval
  are package constants from DESIGN §6.5, not scattered env reads). **PASS**
- **II. Testing Standards (NON-NEGOTIABLE)**: WireGuard is **not** mocked — the
  `Handshakes()` reader is tested against a real kernel device (privileged). The
  tracker's pure logic is tested via a function-typed source (our own seam, not a
  system mock) with an injectable clock; the handler is tested against a **real**
  SQLite store with a fake in-package status provider (our type, not a system
  boundary). Each user story gets acceptance coverage (US1 online/offline/never via
  handler + tracker tests; US2 freshness/robustness via refresh, restart-repopulate,
  and source-error tests). Target ≥ 70% on new code. **PASS**
- **III. User Experience Consistency**: This feature is server-side only; it supplies
  the data the client renders. It exposes `last_handshake` precisely so the client can
  show "last seen" uniformly (Principle III field uniformity). A never-connected node
  reports a definite `online: false` (not "unknown"), so the UI is never ambiguous.
  **PASS** (client rendering lands in 009–011).
- **IV. Performance Requirements**: Directly implements the constitution's
  "Online-status update lag ≤ 30 s" budget via a 30 s poll. `GET /nodes` adds only an
  O(1) map lookup per node — no per-request kernel scan (FR-010) — staying within the
  ≤ 100 ms read budget. Poll goroutine exits on `ctx.Done()` → shutdown ≤ 10 s. **PASS**
- **Security & Operational Discipline**: Logs only public keys and handshake
  timestamps (non-secret). No new network input (read-only enrichment of an existing
  authenticated, caller-scoped endpoint). Single-instance assumption preserved (reads
  the one local WG device). **PASS**

No violations → Complexity Tracking left empty.

## Project Structure

### Documentation (this feature)

```text
specs/007-node-online-status/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (node list response shape)
│   └── nodes-online.md
└── checklists/
    └── requirements.md  # (from /speckit-specify)
```

### Source Code (repository root)

```text
internal/server/
├── wg/
│   ├── iface.go             # + (s *Server) Handshakes() (map[string]time.Time, error)
│   └── peers_test.go        # (existing) + privileged Handshakes() integration test
├── status/                  # NEW package: online-status tracker
│   ├── tracker.go           # Tracker: snapshot, Run(ctx) poll loop, Online/LastHandshake
│   └── tracker_test.go      # unit: computation, refresh, restart-repopulate, source error
├── api/
│   ├── router.go            # Options gains Status; wired into handlers
│   ├── auth_handlers.go     # handlers struct gains `status statusProvider`
│   ├── node_handlers.go     # listNodes enriches each NodeResponse with online/last_handshake
│   └── node_handlers_test.go# + enrichment tests with a fake provider over real SQLite
└── app/
    ├── app.go               # construct tracker (source = wgServer.Handshakes), go tracker.Run(ctx), pass into router
    └── dataplane_test.go    # (existing) — no change

pkg/protocol/
└── node.go                  # NodeResponse += Online bool, LastHandshake string (omitempty)
```

**Structure Decision**: Single Go project, existing layout. The only new unit is
`internal/server/status` (the tracker); everything else is a minimal extension of
existing files. No migration directory entry (status is not persisted).

## Complexity Tracking

> No constitution violations; table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| (none)    | —          | —                                    |
