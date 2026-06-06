# Implementation Plan: Zone Create Auto-Join

**Branch**: `015-zone-create-auto-join` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/015-zone-create-auto-join/spec.md`

## Summary

When a user creates a zone from the desktop panel, the creator's own device must be
joined to that zone in the same operation — no separate manual join. The create
request gains an **optional** device identifier; when present the server validates the
device belongs to the caller, creates the zone, then admits the device to the zone's
nftables isolation set, all with the existing compensation-on-failure pattern so the
result is atomic ({zone + creator member + nft consistent} or {nothing}). When the
identifier is omitted the old create-only behavior is preserved (backward compatible).
The Fyne client always passes the current device id; the create dialog and the separate
"join others' zone" flow are unchanged.

## Technical Context

**Language/Version**: Go 1.26.2 (module `lanweave`)

**Primary Dependencies**: net/http (Go 1.22 mux), modernc.org/sqlite, google/nftables,
golang-jwt/jwt v5; client: Fyne v2.7.4 (behind `//go:build gui`)

**Storage**: SQLite (`zones`, `zone_members`, `nodes` tables — all already exist; no
migration). nftables `inet <table>` zone sets are derivative state, reconstructible
from SQLite at startup.

**Testing**: `go test` — unit (pure Go) + integration against **real** SQLite and
**real** nftables via `unshare -rUn` (rootless user+net namespace gives CAP_NET_ADMIN;
`testutil.RequireNetAdmin(t)` skips when unprivileged). Client seams use a fake `api`
interface (the HTTP client boundary — NOT SQLite/nftables/WireGuard, which are never
mocked).

**Target Platform**: Linux server (root, CAP_NET_ADMIN) + Windows 10/11 client

**Project Type**: Client/server (Go server + Fyne desktop client) — single repo

**Performance Goals**: API write endpoint (incl. nft side-effects) P50 ≤ 300 ms;
nft set element add ≤ 50 ms; client UI input → server-reflected state ≤ 1 s
(Constitution IV). Create now does one extra `Join` (DB) + one `AddMember` (nft) — well
within budget.

**Constraints**: No new SQL transaction wrapper; reuse the existing
compensation-delete pattern. Ownership check must not enable node enumeration
(`GetOwned` returns the same `ErrNodeNotFound` for "absent" and "not yours").

**Scale/Scope**: Small change — 1 protocol field, 1 server handler, 1 store call reuse,
3 client touch-points (apiclient, panel interface, controller), plus tests.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS. Small, reversible: adds one optional field and an
  in-handler branch that reuses `Nodes().GetOwned`, `Zones().Join`, `netfw.AddMember`,
  and the existing delete-compensation. No new abstraction, no new package. SQLite stays
  the single source of truth; nft remains derivative. Config untouched.
- **II. Testing Standards (NON-NEGOTIABLE)** — PASS. Server change is exercised by
  **real-nftables** integration tests (extend `zoneHarness`, asserting `setHas`). Each
  user story gets an acceptance test: US1 = create-with-device → member row + nft set
  element; US2 = join-others still works (existing `TestJoinZone` stays green).
  Backward-compat (no device id) and security (foreign device id → no zone) are tested.
  Client wiring uses the existing fake-`api` seam (permitted; not a SQLite/nft/WG mock).
- **III. User Experience Consistency** — PASS. Creating a zone now yields membership in
  one action; the panel refreshes so the creator appears in the member list within ≤ 1 s
  (SC-001/SC-003). No new destructive op; no silent wait introduced. Create dialog
  unchanged.
- **IV. Performance Requirements** — PASS. One extra DB insert + one nft set element add
  on the create path; both inside the ≤ 300 ms write budget and ≤ 50 ms nft budget.
- **Security & Operational Discipline** — PASS. Device ownership validated at the
  boundary before any state change; no enumeration leak; no secrets logged. The zone
  password the user just set is reused for their own membership without re-verification
  (they authored it this request) — documented in Assumptions.

No violations. One documented exception (consistent with 009–014): the **Fyne create
button → auto-join** behavior is validated manually (no automated GUI test); the
headless `Controller` logic it calls IS unit-tested. Logged in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/015-zone-create-auto-join/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── zones-create.md  # POST /api/v1/zones (auto-join) contract
└── tasks.md             # Phase 2 output (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
pkg/protocol/
└── zone.go                         # + CreateZoneRequest.NodeID (optional)

internal/server/api/
├── zone_handlers.go                # createZone: validate-owned → create → AddZone → Join + AddMember (+ compensate)
└── zone_handlers_test.go           # + auto-join happy/foreign/backward-compat cases (real nft)

internal/server/store/
└── zones.go                        # unchanged (reuse Create/Join/Delete)

internal/client/apiclient/
├── client.go                       # CreateZone(name, nodeID, password)
└── client_test.go                  # asserts request body carries node_id

internal/client/panel/
├── panel.go                        # api.CreateZone signature += nodeID; Controller.CreateZone injects thisMachineNodeID()
└── panel_test.go                   # fake api asserts Controller passes current node id

internal/client/ui/
└── panel.go                        # unchanged (Controller.CreateZone(name, password) signature kept)
```

**Structure Decision**: Existing client/server layout. The server change is confined to
`createZone` in `internal/server/api/zone_handlers.go`, reusing store + netfw methods
already used by `joinZone`. The client change keeps `Controller.CreateZone(name,
password)` (so `internal/client/ui/panel.go` is untouched) and resolves the device id
internally exactly as `Controller.JoinZone` already does via `thisMachineNodeID()`.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Fyne create-button auto-join UX validated **manually**, not by an automated GUI test | Driving the real Fyne dialog + UAC-free desktop interaction in CI is out of scope (same constraint as 009–014); the value is the headless `Controller` behavior, which IS tested | A headless Fyne harness would test the framework, not our logic; the `Controller`/`apiclient` seam tests already cover the node-id injection and request shape end-to-end against a real server |
