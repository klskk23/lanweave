# Implementation Plan: Cascade Deletes (Admin User Removal)

**Branch**: `008-cascade-deletes` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/008-cascade-deletes/spec.md`

## Summary

Add an admin-only `DELETE /api/v1/admin/users/{id}` that removes a user and cascades
through everything attributable to them. The database cascade is a single transaction
that first **gathers** the data-plane footprint (the user's nodes' public keys and
addresses, the surviving zones those nodes belonged to, and the zones the user owns),
then performs one `DELETE FROM users` — SQLite's foreign keys (already enabled in the
DSN) cascade to nodes, owned zones, and memberships, and SET NULL on invite references.
After commit, the handler best-effort syncs the live data plane (remove each former
node's tunnel peer, clear each former node's address from surviving zones' isolation
sets, destroy each owned zone's set+rule); the existing startup rebuild reconciles any
gap. Two safety guards: the last administrator cannot be deleted, and an admin cannot
delete themselves through this operation.

## Technical Context

**Language/Version**: Go 1.26 (module `lanweave`)

**Primary Dependencies**: existing only — `modernc.org/sqlite` (FK cascade via the DSN's
`_pragma=foreign_keys(ON)`), `net/http` (Go 1.22 mux), the existing `netfw.Manager`
(`RemoveMember`, `DeleteZone`) and `wg.Server` (`RemovePeer`). No new dependency.

**Storage**: SQLite, source of truth. **No schema migration** — the cascade relies on
the existing `ON DELETE CASCADE` (nodes, zones, zone_members) and `ON DELETE SET NULL`
(invites) already declared in migrations 0002–0004.

**Testing**: `go test`. Unit/integration over a **real** SQLite store for the cascade
(removal completeness, returned data-plane plan, guards, freed-address reuse, guard
rejections leave state unchanged). Privileged acceptance (real kernel WG + nftables via
`unshare -rUn`, `testutil.RequireNetAdmin`): delete a user with nodes in owned and
foreign zones and assert no peer, no owned-zone set/rule, surviving zone lost the
member, DB clean, address reusable; cross-user untouched; guards (self/last-admin/
non-admin/not-found) return the right status.

**Target Platform**: Linux server (root / `CAP_NET_ADMIN`).

**Project Type**: Single Go project (server), existing `internal/server/...` layout.

**Performance Goals**: A typical account (≤20 nodes across ≤10 zones) is fully removed
within 1 second (SC-007): one DB statement plus a bounded set of per-entity netlink/
nftables operations.

**Constraints**: Single instance; IPv4. Atomic DB cascade (one transaction). No secrets
in logs. The data plane is derivative and reconciled at startup.

**Scale/Scope**: Up to ~1000 nodes overall; a single user's footprint is the bound on
per-delete work.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**: One new store method (`UserRepo.DeleteCascade`) and one new admin
  handler (`deleteUser`), plus a route. SQLite stays the single source of truth; the
  cascade is one `DELETE FROM users` relying on declared FK actions (no hidden logic).
  The data-plane sync reuses existing `wg`/`netfw` primitives — no new abstraction. No
  premature generality. `gofmt`/`vet`/`staticcheck` clean; errors as values; typed
  errors (`ErrUserNotFound`, `ErrLastAdmin`). **PASS**
- **II. Testing Standards (NON-NEGOTIABLE)**: SQLite, nftables, WireGuard are **not**
  mocked — the cascade is tested on a real store and the data-plane effects on a real
  kernel (privileged). Each user story gets acceptance coverage: US1 full-cleanup, US2
  guards + rejection-leaves-unchanged + restart-reconcile, US3 cross-user integrity. A
  regression-style atomicity assertion: a rejected delete (last-admin / not-found)
  changes nothing. Target ≥70% on new code. **PASS**
- **III. User Experience Consistency**: Server-side only. It returns clear, typed
  outcomes (404 not found, 403 self-delete, 409 last-admin) so the client can render a
  human message and a specific confirmation (Principle III destructive-op confirmation
  is the client's job, 009–011). No secret in the response. **PASS**
- **IV. Performance Requirements**: The DB delete is a single statement; data-plane sync
  is bounded by the user's footprint. A typical account completes ≤1 s (SC-007). This is
  a deliberately bulkier write than the 300 ms single-write budget because a cascade is
  inherently multi-entity; each per-entity op stays within its own budget (nft set
  op ≤50 ms) and the aggregate for a typical account is bounded. A pathological account
  (hundreds of nodes) is acknowledged and acceptable for an admin maintenance action.
  Per the constitution §IV process, this budget deviation is **recorded as a documented
  exception** in Complexity Tracking below rather than treated as a silent pass — the
  300 ms budget governs steady-state user write traffic, not a one-shot admin maintenance
  cascade, whose budget is SC-007's ≤1 s. **PASS with documented exception.**
- **Security & Operational Discipline**: Admin-only (reuses `AdminRequired`). Logs only
  ids/public keys (non-secret). Validated path parameter. Single-instance assumption
  preserved (one DB, one WG device, one nft table). **PASS**

No violations → Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/008-cascade-deletes/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── delete-user.md   # DELETE /api/v1/admin/users/{id}
└── checklists/
    └── requirements.md  # (from /speckit-specify)
```

### Source Code (repository root)

```text
internal/server/
├── store/
│   ├── users.go             # + DeleteCascade(ctx, targetID) (*DeletionResult, error); ErrUserNotFound, ErrLastAdmin; DeletionResult type
│   └── users_delete_test.go # NEW unit/integration over real SQLite: cleanup, plan, guards, reuse, rejection-unchanged
├── api/
│   ├── router.go            # + DELETE /api/v1/admin/users/{id} under AuthRequired+AdminRequired
│   ├── admin_user_handlers.go      # NEW deleteUser handler: self-delete guard, calls store, best-effort data-plane sync
│   └── admin_user_handlers_test.go # NEW privileged acceptance (real WG+nft): full cleanup, cross-user, guards
└── (netfw, wg unchanged — reuse RemoveMember/DeleteZone/RemovePeer)

pkg/protocol/
└── (no change — 204 No Content on success; existing error envelope for failures)
```

**Structure Decision**: Single Go project, existing layout. New work is one store method
+ one admin handler + one route + tests. No migration, no protocol change.

### Cascade algorithm (reference for tasks)

1. **Handler** (`deleteUser`): require admin (middleware); parse `{id}`; if `id ==`
   caller's own id → 403 `cannot_delete_self`. Otherwise call `store.Users().DeleteCascade`.
2. **Store** (`DeleteCascade`, one transaction):
   - `SELECT is_admin FROM users WHERE id=?` → none ⇒ `ErrUserNotFound` (rollback).
   - If target is admin and `SELECT COUNT(*) FROM users WHERE is_admin=1` `== 1` ⇒ `ErrLastAdmin` (rollback).
   - Gather: owned zone ids (`zones WHERE owner_user_id=?`); the user's nodes' public keys (`nodes WHERE user_id=?`); surviving-zone memberships of those nodes (`zone_members ⋈ nodes ⋈ zones WHERE nodes.user_id=? AND zones.owner_user_id<>?` → `{ip, zone_id}`).
   - `DELETE FROM users WHERE id=?` (FK cascade clears nodes/zones/zone_members; SET NULL on invites).
   - Commit; return `DeletionResult{NodePubKeys, SurvivingMemberships:[{IP,ZoneID}], OwnedZoneIDs}`.
3. **Handler** (best-effort, each failure logged, never fails the 204 since the DB is authoritative):
   - For each pubkey → `wg.RemovePeer`.
   - For each surviving membership → `netfw.RemoveMember(zoneID, ip)`.
   - For each owned zone → `netfw.DeleteZone(zoneID)` (destroys its set+rule and all elements).
   - Respond `204 No Content`.
4. **Reconcile**: the existing startup `rebuildNodePeers` + `rebuildZoneRules` close any best-effort gap (FR-009).

## Complexity Tracking

> One documented exception (Principle IV budget), recorded per the constitution's own
> process. No principle is diluted; the deviation is bounded and justified.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Cascade delete may exceed the §IV 300 ms write-endpoint budget for large accounts (SC-007 allows ≤1 s for ≤20 nodes/≤10 zones) | A whole-user removal is inherently multi-entity (one DB statement + one kernel op per node/membership/owned-zone); it is an admin maintenance action, not steady-state user write traffic that the 300 ms budget targets | Splitting into async/background work would break the atomic, observable "delete and it's gone" guarantee and add a job/queue subsystem the single-instance design avoids; per-entity work already stays within its own sub-budget |
