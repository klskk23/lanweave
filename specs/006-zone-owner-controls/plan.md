# Implementation Plan: Zone Owner Controls

**Branch**: `006-zone-owner-controls` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/006-zone-owner-controls/spec.md`

## Summary

Add three owner-only operations on top of feature 005's zones: change the zone
password (members keep their membership; only future joins are affected), kick a
specific member node (including another user's), and delete the entire zone
(releasing the name and destroying its isolation rules). Every operation is gated to
the zone's owner (non-owner → 403, missing zone → 404). The database stays the source
of truth; the nftables side is updated immediately (best-effort) and reconciled by
the existing startup rebuild. No new tables, endpoints aside, and no new dependency.

## Technical Context

**Language/Version**: Go 1.23+ (existing module `lanweave`)

**Primary Dependencies**: All inherited (modernc sqlite, goose, google/nftables, wgctrl, golang-jwt, argon2). **No new dependency.**

**Storage**: SQLite — **no new tables**. Mutates `zones` (password, delete) and `zone_members` (kick = remove one; delete = cascade all). nftables sets/rules updated/destroyed accordingly.

**Testing**: Two tiers. **Unprivileged (real SQLite)**: password update, owner-vs-non-owner authorization, membership removal (kick), zone delete cascade, `GetByID`. **Privileged (real kernel nftables, root / `unshare -rUn`)**: kick removes the element from the zone set; delete destroys the set + accept rule; a restart rebuilds the ruleset to match the DB (changed password / kicked member / deleted zone reflected). Manual two-client ping in quickstart for literal reachability.

**Target Platform**: Linux, kernel WireGuard + nftables, x86-64.

**Project Type**: Single Go project — extends `store`, `netfw`, `api` (zone surface).

**Performance Goals** (constitution IV): each owner op is one DB write + a few netlink ops → well within the ≤300 ms write budget.

**Constraints**:
- Password change must NOT eject members (FR-002); only the stored hash changes.
- Kick may target ANY member node, incl. another user's (FR-005) — first cross-user mutation.
- Delete removes memberships + isolation rules but NOT member nodes; releases the name (FR-009/010).
- Owner-only (403 for non-owner, 404 for missing zone, FR-011); membership ≠ ownership (FR-012).
- Password hashed, never logged (FR-014).

**Scale/Scope**: Single instance. This feature: 0 new tables, 3 new endpoints, 2 small store methods, 1 netfw method (`DeleteZone`), no app/startup change (the 005 rebuild already covers it).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Applies? | How this plan honors it |
|-----------|----------|-------------------------|
| **I. Code Quality** | Yes | Reuses the existing pattern: password/delete in `store` (`UpdatePassword`, the existing `Delete`), node lookup `GetByID` in `store`, set/rule destruction `DeleteZone` in `netfw`, handlers in `api`. A small `ownedZone` helper centralizes the GetByName + owner check (no duplicated 404/403 across three handlers). DB stays the source of truth; nftables is derived and reconciled at startup. No premature abstraction; typed errors reused (`ErrNotMember`, `ErrNodeNotFound`). |
| **II. Testing Standards (NON-NEGOTIABLE)** | Yes (reachability via ruleset, as in 005) | SQLite is **not** mocked (password update, authz, kick, delete-cascade on a real temp DB). nftables is **not** mocked: kick/delete/restart assert the **real kernel** ruleset (set element gone, set+rule destroyed, rebuild matches) under root / `unshare -rUn`, skipping with a clear signal otherwise. Each US has tests; the authz matrix (SC-004) and restart (SC-005) are explicit. Literal packet reachability stays a manual quickstart step (carried over from 005). CI runs the privileged tier. |
| **III. UX Consistency** | **N/A** | No end-user UI (Windows client is 009–011). Machine surface — stable codes (`validation_error`, `forbidden`, `not_found`, `unauthorized`) — stays uniform. |
| **IV. Performance Requirements** | Yes | One DB write + a couple of netlink ops per operation; trivially within budget. |
| **Security & Operational Discipline** | Yes | New password argon2id-hashed, never logged. Owner-only authorization on all three (403); a member is not an owner (FR-012). No admin override of zone control (scope guard). Auth + global rate limit reused. |

**Result**: PASS. The reachability-via-ruleset testing approach and the privileged-test skip are the same documented items as feature 005 (real nftables, not mocks) — recorded in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/006-zone-owner-controls/
├── plan.md
├── research.md          # Phase 0
├── data-model.md        # Phase 1 (mutations on 005 entities; no new tables)
├── quickstart.md        # Phase 1
├── contracts/
│   └── owner-ops.md      # PATCH /zones/{name}; DELETE /zones/{name}/members/{node_id}; DELETE /zones/{name}
├── checklists/requirements.md
└── tasks.md             # /speckit-tasks (later)
```

### Source Code (repository root) — changed (no new files except tests)

```text
internal/server/
├── store/
│   ├── zones.go          # CHANGED: ZoneRepo.UpdatePassword(zoneID, hash); (Delete already exists from 005)
│   ├── nodes.go          # CHANGED: NodeRepo.GetByID(nodeID) -> node (ip) / ErrNodeNotFound (unscoped, for kick)
│   ├── zones_test.go      # CHANGED: UpdatePassword + ownership-irrelevant Leave (kick) integration
│   └── nodes_test.go      # CHANGED: GetByID test
├── netfw/
│   ├── nftables.go       # CHANGED: Manager.DeleteZone(zoneID) — delete the set's accept rule(s) then the set
│   └── zones_test.go      # CHANGED (privileged): DeleteZone removes set + rule; kick (RemoveMember) already covered
├── api/
│   ├── zone_handlers.go  # CHANGED: changeZonePassword, kickMember, deleteZone + ownedZone(w,r,id) helper
│   ├── router.go         # CHANGED: 3 routes (PATCH zones/{name}; DELETE zones/{name}/members/{node_id}; DELETE zones/{name})
│   └── zone_handlers_test.go # CHANGED: acceptance (each op + authz 403/404) + privileged ruleset checks
└── pkg/protocol/zone.go  # CHANGED: ChangeZonePasswordRequest{ Password }
```

**Structure Decision**: Pure extension of the feature-005 surface — no new packages,
tables, or dependencies, and **no app/startup change** (the 005 `rebuildZoneRules`
already reconciles a changed password, a kicked member, or a deleted zone on restart).
The three handlers share an `ownedZone` helper for the GetByName + owner-or-403 +
missing-or-404 gate. `netfw.DeleteZone` is the only genuinely new mechanism — it must
delete the accept rule before the set it references (kernel ordering).

## Complexity Tracking

> Two documented items, identical to feature 005; neither a constitution violation.

| Item | Why | Mitigation / Note |
|------|-----|-------------------|
| Reachability asserted via real **ruleset state**, not simulated packets | A literal multi-client ping is impractical in unit CI. | Tests assert the **real kernel** nftables state (element removed on kick, set+rule destroyed on delete, rebuild matches DB) — not a mock; that ruleset is the enforcement. Manual two-client ping in quickstart. |
| Privileged tests skip when unprivileged | Real nftables ops need `CAP_NET_ADMIN`. | Real, not mocked; run under root / `unshare -rUn`. SQLite/authz logic fully covered unprivileged. CI must run the privileged tier. |
