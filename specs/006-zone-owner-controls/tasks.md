---
description: "Task list for 006-zone-owner-controls"
---

# Tasks: Zone Owner Controls

**Input**: Design documents from `/specs/006-zone-owner-controls/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present). Builds on features 001–005.

**Tests**: REQUIRED per constitution Principle II. **Tiers**: password update + ownership authz + membership removal + delete-cascade run on real temp SQLite (unprivileged, no mocks); real nftables effects (kick removes the set element, delete destroys the set+rule, restart rebuild) are asserted against the real kernel under root / `unshare -rUn`, skipping with a clear message otherwise. Literal two-client packet reachability is a manual quickstart step. Test tasks are written FIRST and must FAIL before implementation. CI MUST run the privileged tier.

**Organization**: Tasks grouped by user story. US1–US3 are P1 (the three owner operations); US4 (P2) is authz + restart consistency.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: different files, no dependency on an incomplete task
- **(privileged)**: needs root / `unshare -rUn`; skips otherwise

## Path Conventions

Single Go project `lanweave`. No new tables/files except tests. Extends `store/zones.go`,
`store/nodes.go`, `netfw/nftables.go`, `api/zone_handlers.go`, `api/router.go`,
`pkg/protocol/zone.go`.

---

## Phase 1: Setup

- [X] T001 Add `ChangeZonePasswordRequest{ Password string }` to `pkg/protocol/zone.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the store/netfw methods and the ownership-gate helper every owner handler needs. (No new tables, no new dependency, no app/startup change — feature 005's rebuild already reconciles all three operations.)

**⚠️ CRITICAL**: No user story work begins until this phase is complete.

- [X] T002 [P] Integration tests (real temp DB): `ZoneRepo.UpdatePassword` changes only the hash (membership unchanged) in `internal/server/store/zones_test.go`; `NodeRepo.GetByID` returns any node's address / `ErrNodeNotFound` in `internal/server/store/nodes_test.go` — MUST FAIL
- [X] T003 Implement `ZoneRepo.UpdatePassword(ctx, zoneID, passwordHash)` in `internal/server/store/zones.go` and `NodeRepo.GetByID(ctx, nodeID) (*Node, error)` (unscoped) in `internal/server/store/nodes.go`
- [X] T004 [P] (privileged) Test in `internal/server/netfw/zones_test.go`: after `AddZone`+`AddMember`, `DeleteZone` removes BOTH the set and its accept rule (the chain no longer references `zone_<id>`, the set is gone) — MUST FAIL
- [X] T005 Implement `Manager.DeleteZone(zoneID)` in `internal/server/netfw/nftables.go`: in the forward chain, `DelRule` the rule(s) whose exprs reference `zone_<id>`, then `DelSet` the set (rule before set; one flush, or two if the kernel rejects the batch order) per research.md R4
- [X] T006 Add the `ownedZone(w, r, identity) (*store.Zone, bool)` helper in `internal/server/api/zone_handlers.go`: `GetByName` → nil ⇒ 404 `not_found`; owner mismatch ⇒ 403 `forbidden`; else return the zone (research.md R1)

**Checkpoint**: `go build ./...` green; bare `go test ./...` green (privileged tests skip).

---

## Phase 3: User Story 1 — owner 改密码（不踢老人） (Priority: P1) 🎯 MVP

**Goal**: The owner changes the password; members keep membership; old password stops joining, new one starts.

**Independent Test**: Change password → 200; an existing member is still listed; a fresh join with the old password → refused; with the new password → admitted.

### Tests for User Story 1 (REQUIRED) ⚠️

- [X] T007 [P] [US1] Acceptance test in `internal/server/api/zone_handlers_test.go`: PATCH (owner) → 200; existing members unchanged; join with OLD password → 403, with NEW password → 200; weak new password → 400; non-owner → 403; missing zone → 404 — MUST FAIL

### Implementation for User Story 1

- [X] T008 [US1] Implement `changeZonePassword` handler in `internal/server/api/zone_handlers.go`: `ownedZone`, validate password (≥8), `HashPassword`, `UpdatePassword`; map → 200/400/403/404 (never log the password)
- [X] T009 [US1] Register route `PATCH /api/v1/zones/{name}` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: password rotation works without ejecting members.

---

## Phase 4: User Story 2 — owner 踢出成员 (Priority: P1) 🎯 MVP

**Goal**: The owner kicks a member node (incl. another user's); it loses reachability within the zone; the node and its other zones are untouched.

**Independent Test**: Kick a member → 204 and (privileged) its address is gone from the zone set; the node still exists; re-kick → 404; non-owner → 403; a two-zone node kicked from one keeps the other.

### Tests for User Story 2 (REQUIRED) ⚠️

- [X] T010 [P] [US2] Acceptance test in `internal/server/api/zone_handlers_test.go`: owner kicks a member (including a node owned by a different user) → 204 and (privileged, `RequireNetAdmin`) the set no longer contains that address; the node still exists; kicking a non-member / nonexistent node → 404; non-owner → 403; a node in two zones kicked from one retains the other set — MUST FAIL

### Implementation for User Story 2

- [X] T011 [US2] Implement `kickMember` handler in `internal/server/api/zone_handlers.go`: `ownedZone`, parse `node_id`, `GetByID` (→ 404 if absent), `Leave` (→ 404 `ErrNotMember`), `NetFW.RemoveMember(zoneID, ip)` best-effort; 204
- [X] T012 [US2] Register route `DELETE /api/v1/zones/{name}/members/{node_id}` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: owners can revoke a specific member's access.

---

## Phase 5: User Story 3 — owner 删除整个 zone (Priority: P1) 🎯 MVP

**Goal**: The owner deletes a zone; memberships removed, set+rule destroyed, name released; member nodes survive.

**Independent Test**: Delete a zone with members → 204; the name can be re-created; (privileged) the set+rule are gone; member nodes still exist; a multi-zone node keeps its other zone; non-owner → 403.

### Tests for User Story 3 (REQUIRED) ⚠️

- [X] T013 [P] [US3] Acceptance test in `internal/server/api/zone_handlers_test.go`: owner deletes a zone with members → 204; the name is re-creatable (201); (privileged) the `zone_<id>` set and its accept rule are gone; member nodes still exist; a node also in another zone keeps that zone's set; non-owner delete → 403; missing zone → 404 — MUST FAIL

### Implementation for User Story 3

- [X] T014 [US3] Implement `deleteZone` handler in `internal/server/api/zone_handlers.go`: `ownedZone`, `Zones().Delete(zoneID)` (cascades memberships), `NetFW.DeleteZone(zoneID)` best-effort; 204
- [X] T015 [US3] Register route `DELETE /api/v1/zones/{name}` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: owners can dismantle a zone and free its name.

---

## Phase 6: User Story 4 — 权限与一致性 (Priority: P2)

**Goal**: Non-owners (incl. mere members) are refused on all three ops; member-view still works; a restart reflects the changes.

**Independent Test**: A member-but-not-owner is refused (403) on each op; member-view (005) still returns members; after change/kick/delete + restart, the rules match the DB.

### Tests for User Story 4 (REQUIRED) ⚠️

- [X] T016 [P] [US4] (privileged) Test in `internal/server/app/zones_dataplane_test.go`: seed a zone+members, perform a kick and a zone-delete via the repos/netfw, then `rebuildZoneRules` → the kicked member is absent and the deleted zone has no set/rule (matches the DB, FR-013/SC-005) — MUST FAIL
- [X] T017 [P] [US4] Acceptance test in `internal/server/api/zone_handlers_test.go`: a user who is a MEMBER (not owner) of the zone is refused (403) on PATCH/kick/delete, yet can still GET members (membership ≠ ownership, FR-012/FR-015) — MUST FAIL

### Implementation for User Story 4

- [X] T018 [US4] Confirm the authorization gate and restart consistency hold (no new code expected beyond US1–US3 + the existing 005 rebuild); add any missing assertion so T016/T017 pass

**Checkpoint**: owner controls are authorized and durable across restart.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T019 Run `make lint` (gofmt + `go vet` + staticcheck) clean; confirm ≥70% coverage on the new code (`store`/`netfw`/`api` additions); kernel-path coverage measured under the privileged run
- [X] T020 Execute `quickstart.md` under `unshare -rUn`: change password (old fails / new joins), kick (element gone, node survives), delete (set+rule gone, name re-creatable), non-owner 403 matrix, and restart reflects all of it

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: the DTO.
- **Foundational (Phase 2)**: depends on Setup — provides `UpdatePassword`/`GetByID`/`DeleteZone`/`ownedZone`. T003 needs T002(test); T005 needs T004(test).
- **US1 / US2 / US3**: each depends on Foundational; they extend `zone_handlers.go` + `router.go` sequentially (shared files). Independent of each other in behavior.
- **US4 (Phase 6)**: depends on US1–US3 existing (it tests their authz + restart effects).
- **Polish (Phase 7)**: after the targeted stories.

### Critical Path

```
Setup → Foundational → US1 → US2 → US3 → US4 → Polish
```

### Within Each User Story

- Test tasks (⚠️) written first and MUST FAIL before implementation.
- Handler before route.

---

## Parallel Opportunities

- **Foundational**: T002 [P] (store test) and T004 [P] (netfw test) are distinct files; T003/T005/T006 then sequence.
- **Each story**: its single acceptance test is [P] vs other stories' tests (distinct concerns), but the implementation tasks share `zone_handlers.go`/`router.go` → sequential.
- **US4**: T016 [P] (app test) and T017 [P] (api test) in parallel.

### Parallel Example: Foundational tests

```bash
Task T002: store UpdatePassword + GetByID tests
Task T004: netfw DeleteZone privileged test
```

---

## Implementation Strategy

### MVP (US1 + US2 + US3)

1. Setup + Foundational (DTO, store methods, DeleteZone, ownedZone helper).
2. US1 → change password (members kept).
3. US2 → kick a member (element removed from the real set).
4. US3 → delete a zone (set+rule destroyed, name released).
5. **STOP & VALIDATE** (privileged): rotate a password (old fails / new joins), kick a member (gone from set), delete a zone (set+rule gone, name re-creatable).

### Incremental Delivery

- Add US4 → prove the authz matrix (member ≠ owner) and restart consistency.

---

## Implementation outcomes (analyze findings)

- **I1 (DeleteZone)**: `DeleteZone` deletes the exact `*Rule` objects returned by
  `GetRules` (their handles), then the set — verified on the real kernel by
  `TestDeleteZone` (both the rule AND the set are gone).
- **U1 (kick precedence)**: `kickMember` runs `ownedZone` (403/404) → `GetByID` (404)
  → `Leave` (404), so a non-owner never probes node/membership existence (asserted in
  `TestKickMember`/`TestOwnerOpsRequireOwnership`).
- **Verified for real** via `unshare -rUn`: DeleteZone (rule+set destroyed), kick
  (element removed, node survives), change-password (members kept, old-pw 403/new-pw
  200), owner-ops rebuild from DB, and a binary smoke (the full owner-ops flow +
  non-owner 403 matrix + restart).
- **No new tables/dependency/app change** — feature 005's `rebuildZoneRules` already
  reconciles change-password / kick / delete on restart.

## Notes

- [P] = different files, no dependency on an incomplete task.
- **No mocking** of SQLite or nftables: password/authz/kick/delete on a real DB; set/rule
  effects on the real kernel (privileged) or skipped with a clear message (constitution
  Principle II; research.md R3/R4). CI MUST run the privileged tier.
- **No new tables, no new dependency, no app/startup change** — feature 005's
  `rebuildZoneRules` already reconciles a changed password, a kicked member, and a
  deleted zone on restart.
- Reuses 001–005: auth/JWT middleware, argon2id (`HashPassword`), config + logging +
  error envelope + rate limiter, `netfw.Manager`, zones/memberships (005). Zone password
  hashed, never logged (FR-014).
- **Cross-user note**: kick uses the **unscoped** `NodeRepo.GetByID` (owner authority is
  over the zone, not the node), distinct from `GetOwned` used in 004/005.
- Out of scope (later features): ownership transfer (v1.1), admin override of zone control,
  user-deletion cascade (008), client UI (009–011).
