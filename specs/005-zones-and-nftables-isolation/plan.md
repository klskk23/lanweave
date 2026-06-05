# Implementation Plan: Zones and nftables Isolation

**Branch**: `005-zones-and-nftables-isolation` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/005-zones-and-nftables-isolation/spec.md`

## Summary

A zone is a password-protected group whose members are node addresses held in an
nftables set; the relay's forward chain admits same-set traffic and denies the rest.
Any user creates a zone (becomes owner), joins one of their nodes by name+password,
leaves, lists their zones, and views a zone's members (full transparency). A node may
join multiple zones. Zone create/join are atomic across DB + nftables (compensate on
failure); leave and node-deletion are DB-authoritative with best-effort element
removal; the whole ruleset is rebuilt from the database at startup. Deleting a node
(feature 004) now also removes it from every zone and set, so a recycled address
never inherits a deleted node's membership.

## Technical Context

**Language/Version**: Go 1.23+ (existing module `lanweave`)

**Primary Dependencies**: All inherited — `modernc.org/sqlite`, `goose`, `github.com/google/nftables` (003), `wgctrl` (003/004), `golang-jwt/v5` (002), `golang.org/x/crypto/argon2` (001), stdlib. **No new dependency.**

**Storage**: SQLite. New `zones` and `zone_members` tables. nftables sets/rules are derived from them and rebuilt at startup.

**Testing**: Three tiers. **Unit/SQLite (unprivileged)**: zone CRUD, membership (join/leave idempotency), `ListForUser`, member visibility + participant authz, no-enumeration, node-delete cleanup logic. **Privileged (real kernel nftables)**: the relay's ruleset state — per-zone set with the right member elements + the same-zone accept rule — exactly matches memberships after create/join/leave/node-delete/restart (asserted against the real kernel via `google/nftables`, under root / `unshare -rUn`). **Manual (quickstart)**: literal two-client ping for same-zone reachable / cross-zone denied.

**Target Platform**: Linux, kernel WireGuard + nftables, x86-64.

**Project Type**: Single Go project — extends server packages and 004's node-delete path.

**Performance Goals** (constitution IV): join/leave ≤ 300 ms (one membership write + a few netlink ops); list/members ≤ 100 ms; startup rebuild scales with zones×members (bounded, single flush).

**Constraints**:
- Same-zone admit + cross-zone deny + multi-zone membership (FR-009/010); reachability per shared zone, not transitive.
- DB is the source of truth; ruleset rebuilt at startup to exactly match memberships (FR-017).
- Node deletion clears memberships + set elements so a recycled address inherits nothing (FR-018).
- Zone passwords hashed (argon2id), never logged; join is no-enumeration (FR-006/019).

**Scale/Scope**: Single instance; tens of zones, hundreds of nodes. This feature: 2 new tables, ~5 endpoints, netfw set/rule/element methods, a touch to 004's deleteNode, 1 new config-free DTO file. No new dependency.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Applies? | How this plan honors it |
|-----------|----------|-------------------------|
| **I. Code Quality** | Yes | Zones in `store`, set/rule/element ops in `netfw`, HTTP in `api`. DB is the single source of truth; the nftables ruleset is derived and rebuilt from it. Create/join are atomic (insert + nft, compensate on failure); leave/node-delete are DB-authoritative + best-effort + startup reconcile — one consistent pattern reused from 004. Typed errors (`ErrZoneNameTaken`, `ErrZoneOrPassword`, `ErrNotMember`). No premature abstraction. |
| **II. Testing Standards (NON-NEGOTIABLE)** | Yes (with documented reachability approach) | SQLite is **not** mocked (zone/membership integration on a real temp DB). nftables is **not** mocked: the **real kernel ruleset** (sets, elements, accept rules) is asserted to match memberships after every operation and a restart, under root / `unshare -rUn`, skipping with a clear signal otherwise. Literal node-to-node **packet** reachability (SC-001) needs a multi-client WG topology that is impractical in unit CI; it is verified manually in `quickstart.md`, while automated tests assert the **real** kernel state that *is* the enforcement mechanism. Each user story has tests; FR-018 (delete cleanup) and FR-017 (restart rebuild) are explicit. CI runs the privileged tier. |
| **III. UX Consistency** | **N/A** | No end-user UI (Windows client is 009–011). Machine-facing surface — zone JSON + stable codes (`validation_error`, `zone_name_taken`, `invalid_zone_or_password`, `not_found`, `unauthorized`) — stays uniform. |
| **IV. Performance Requirements** | Yes | Join/leave is one DB write + a single `add/delete element` netlink op; list/members are scoped reads. Startup rebuild is one batched flush. Budgets smoke-checked in quickstart. |
| **Security & Operational Discipline** | Yes | Zone passwords argon2id-hashed (reused), never logged. Join is no-enumeration (generic error + dummy verify, mirroring login). Member visibility gated to participants (non-members → 404). Node ownership enforced for join/leave (→ 404). Auth + global rate limit reused. |

**Result**: PASS. The reachability-testing approach (assert real kernel ruleset state; manual packet test in quickstart) is recorded in Complexity Tracking — it uses real nftables, not a mock.

## Project Structure

### Documentation (this feature)

```text
specs/005-zones-and-nftables-isolation/
├── plan.md
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1 (incl. real two-client reachability test)
├── contracts/
│   ├── zones.md         # create / join / leave / list
│   └── members.md       # GET /zones/{name}/members
├── checklists/requirements.md
└── tasks.md             # /speckit-tasks (later)
```

### Source Code (repository root) — new and changed

```text
internal/server/
├── store/
│   ├── migrations/0004_zones.sql   # NEW: zones + zone_members (FKs ON DELETE CASCADE)
│   ├── zones.go          # NEW: ZoneRepo — Create, GetByName, Join(idempotent), Leave, MembersByZone,
│   │                     #      ListForUser(is_owner), IsParticipant, AllForRebuild, ZonesForNode
│   ├── nodes.go          # CHANGED: add GetOwned(userID,nodeID) -> node (ip,pubkey) for join/leave/delete
│   └── zones_test.go     # NEW (real temp DB): CRUD, join/leave idempotency, list/members, scoping
├── netfw/
│   ├── nftables.go       # CHANGED: ZoneState; Rebuild([]ZoneState) full; AddZone(id); AddMember(id,ip); RemoveMember(id,ip)
│   └── nftables_test.go  # CHANGED/NEW (privileged): set per zone + elements + same-zone accept rule; add/remove element; rebuild matches
├── api/
│   ├── zone_handlers.go  # NEW: createZone, joinZone, leaveZone, listZones, zoneMembers
│   ├── node_handlers.go  # CHANGED: deleteNode also removes node from all zones + set elements (FR-018)
│   ├── router.go         # CHANGED: Options +NetFW *netfw.Manager; zone routes (AuthRequired)
│   └── zone_handlers_test.go # NEW: acceptance (create/join/leave/list/members, authz, no-enum) + privileged ruleset checks
├── app/
│   ├── app.go            # CHANGED: pass NetFW into router; rebuild zone rules from DB at startup
│   └── dataplane.go      # CHANGED: setupDataPlane returns (*wg.Server, *netfw.Manager) (no nft rebuild); add rebuildZoneRules
pkg/protocol/
└── zone.go               # NEW: CreateZoneRequest, ZoneResponse(+is_owner), JoinZoneRequest, LeaveZoneRequest, ZoneMemberResponse
```

**Structure Decision**: Continue the single-project layout. Zone persistence lives in
`store`; all nftables set/rule/element work lives in `netfw` (it owns the table). The
startup nft rebuild moves out of `setupDataPlane` into `app.Run` (where the store is
available) so it can populate sets from the database — `setupDataPlane` now returns
the long-lived `netfw.Manager` alongside `wg.Server`, both passed to the router so
handlers can mutate sets on join/leave. Feature 004's `deleteNode` is extended to clear
zone memberships and set elements (FR-018), reusing the same DB-authoritative +
best-effort + startup-reconcile pattern. No new dependency.

## Complexity Tracking

> Two documented items, neither a constitution violation.

| Item | Why | Mitigation / Note |
|------|-----|-------------------|
| Reachability verified via real **ruleset state**, not simulated packets, in automated tests | A literal node-to-node ping needs ≥2 WireGuard client topologies + the relay forwarding — impractical/flaky in unit CI. | Tests assert the **real kernel nftables** sets/elements/accept-rules match memberships (not a mock); that ruleset *is* the enforcement. A literal two-client ping is in `quickstart.md` for manual/heavy-CI verification. |
| Privileged tests skip when unprivileged | Real nftables set/element/rule ops need `CAP_NET_ADMIN`. | Real, not mocked; run under root / `unshare -rUn` (verified usable in 003/004). SQLite/zone logic fully covered unprivileged. CI must run the privileged tier. |
