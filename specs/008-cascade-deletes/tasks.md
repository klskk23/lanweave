# Tasks: Cascade Deletes (Admin User Removal)

**Feature**: 008-cascade-deletes | **Branch**: `008-cascade-deletes`
**Input**: Design documents in `/specs/008-cascade-deletes/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED per constitution Principle II (NON-NEGOTIABLE). SQLite, nftables, and
WireGuard are tested for real (never mocked): the cascade on a real store, the kernel
effects on a real device/table under `unshare -rUn`.

**Organization**: By user story. US1 (P1) is the MVP — the full cascade + cleanup.
US2 (P2) adds the safety guards and atomicity assertions. US3 (P3) proves cross-user
integrity (tests only, over US1's implementation).

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`
`[P]` = parallelizable (different file, no incomplete dependency).

---

## Phase 1: Setup (shared shapes)

- [X] T001 [P] Add the `DeletionResult` struct (`NodePubKeys []string`; `SurvivingMemberships []struct{IP netip.Addr; ZoneID int64}`; `OwnedZoneIDs []int64`) and sentinel errors `ErrUserNotFound` and `ErrLastAdmin` to `internal/server/store/users.go`

**Checkpoint**: shared types/errors exist for the cascade and its guards.

---

## Phase 3: User Story 1 — 管理员删除用户并清除其全部足迹 (P1) 🎯 MVP

**Goal**: an admin can delete a user; the user, their nodes (addresses freed, peers
removed, memberships cleared), and their owned zones (set+rule destroyed) are all gone.

**Independent test**: delete a user with two nodes (one in an owned zone, one in another
user's zone); verify records, peers, and isolation rules carry no trace and the freed
address is reused.

- [X] T002 [US1] Implement `DeleteCascade(ctx, targetID int64) (*DeletionResult, error)` happy path in `internal/server/store/users.go`: `BeginTx` (defer rollback); `SELECT is_admin FROM users WHERE id=?` → no row ⇒ `ErrUserNotFound`; gather owned zone ids (`SELECT id FROM zones WHERE owner_user_id=?`), node public keys (`SELECT wg_pubkey FROM nodes WHERE user_id=?`), and surviving-zone memberships (`SELECT n.ip, zm.zone_id FROM zone_members zm JOIN nodes n ON n.id=zm.node_id JOIN zones z ON z.id=zm.zone_id WHERE n.user_id=? AND z.owner_user_id<>?`, ip via `ipam.Uint32ToAddr`); `DELETE FROM users WHERE id=?`; `Commit`; return the gathered `DeletionResult`
- [X] T003 [US1] Add `deleteUser` handler in `internal/server/api/admin_user_handlers.go`: parse `{id}` (non-integer ⇒ 404 `not_found`); call `h.store.Users().DeleteCascade`; map `ErrUserNotFound` ⇒ 404; on success best-effort sync (each failure `h.log.Error`, never fails the response): `h.wg.RemovePeer(pub)` per pubkey, `h.netfw.RemoveMember(zoneID, ip)` per surviving membership, `h.netfw.DeleteZone(zoneID)` per owned zone; respond `204 No Content`
- [X] T004 [US1] Register `DELETE /api/v1/admin/users/{id}` wrapped `AuthRequired(opts.JWT)(AdminRequired()(http.HandlerFunc(h.deleteUser)))` in `internal/server/api/router.go` (alongside the other `/api/v1/admin/...` routes)
- [X] T005 [P] [US1] Store test in `internal/server/store/users_delete_test.go` (real SQLite): seed a user with two nodes, an owned zone (node 1 joined) and a foreign user's zone (node 2 joined); call `DeleteCascade`; assert the user/nodes/owned-zone/membership rows are gone, the returned `DeletionResult` has both pubkeys, the surviving membership `{node2 ip, foreign zone id}`, and the owned zone id; assert a freshly created node reuses node 1's freed address; assert `ErrUserNotFound` for a missing id; assert an invite the user created has its `created_by_user_id` cleared to NULL after the delete (audit row preserved, no dangling reference — FR-008)
- [X] T006 [US1] Privileged acceptance in `internal/server/api/admin_user_handlers_test.go` (real WG+nft, `testutil.RequireNetAdmin`): admin deletes a user whose node A is in the user's owned zone and node B is in another user's zone → `204`; assert the device has no peer for A or B, the owned zone's set+rule are gone, the foreign zone's set no longer contains B's address, the DB has no rows for the user, and a new registration reuses a freed address

**Checkpoint**: US1 delivers the full cascade end to end (MVP).

---

## Phase 4: User Story 2 — 删除是原子且安全的 (P2)

**Goal**: the cascade is atomic and cannot lock out admins; the last admin cannot be
deleted and an admin cannot delete themselves.

**Independent test**: deleting the sole admin is rejected and changes nothing; an admin
deleting their own account is rejected; a rejected delete leaves all rows intact.

- [X] T007 [US2] Add the last-admin guard to `DeleteCascade` in `internal/server/store/users.go`: when the target `is_admin` and `SELECT COUNT(*) FROM users WHERE is_admin=1` `== 1`, return `ErrLastAdmin` (transaction rolls back unchanged) before any gather/delete
- [X] T008 [US2] Add guards/mapping to `deleteUser` in `internal/server/api/admin_user_handlers.go`: if `{id}` equals the caller's own id (`IdentityFrom`) ⇒ 403 `cannot_delete_self` (before calling the store); map `ErrLastAdmin` ⇒ 409 `last_admin`
- [X] T009 [P] [US2] Store test in `internal/server/store/users_delete_test.go`: `DeleteCascade` on the only admin ⇒ `ErrLastAdmin`; with two admins, deleting one succeeds; after a rejected delete (last-admin and missing-id) assert user/node/zone row counts are unchanged (atomicity, SC-005)
- [X] T010 [P] [US2] Handler guard test in `internal/server/api/admin_user_handlers_test.go` (non-privileged; rejections never touch the data plane): self-delete (admin targets own id) ⇒ 403 `cannot_delete_self`; non-admin caller ⇒ 403; unauthenticated ⇒ 401; unknown id ⇒ 404

**Checkpoint**: guards and atomicity proven; US1's contract is unchanged for valid deletes.

---

## Phase 5: User Story 3 — 不波及其他用户 (P3)

**Goal**: deleting one user leaves everyone else intact except the intended membership
changes.

**Independent test**: two users share zones in both directions; delete one and confirm
the other's account/nodes/unrelated memberships are intact while exactly the shared
memberships tied to the deleted user are cleared.

- [X] T011 [US3] Privileged acceptance in `internal/server/api/admin_user_handlers_test.go` (real WG+nft, `testutil.RequireNetAdmin`): user A owns zone ZA (B's node joined); user B owns zone ZB (A's node joined); admin deletes A → B's account, B's node, and ZB survive; ZA is gone (B's node no longer in it, B's node still exists); ZB's set no longer contains A's deleted node; B's peer remains on the device, A's peers are gone

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T012 [P] Run `gofmt -w`, `go vet ./...`, `staticcheck ./internal/server/store/... ./internal/server/api/...`, and `go build ./...`; resolve findings
- [X] T013 [P] Run `go test ./internal/server/store/...` (unprivileged) and `unshare -rUn go test ./internal/server/api/...` (privileged); confirm new code (`DeleteCascade`, `deleteUser`) reaches ≥ 70% line coverage, noting any privileged-only uncovered paths

---

## Dependencies & Execution Order

- **Setup (T001)**: blocks everything.
- **US1 (T002–T006)**: T002 needs T001; T003 needs T002 (calls it); T004 needs T003 (routes to it); T005 needs T002; T006 needs T002+T003+T004. T002/T003 are sequential within their files; T005 can run alongside T003/T004 once T002 lands.
- **US2 (T007–T010)**: extends US1's files — T007 edits the same `DeleteCascade` (after T002), T008 edits the same `deleteUser` (after T003). T009/T010 are tests (parallel once their targets exist). US2 depends on US1.
- **US3 (T011)**: depends only on US1's implementation (T002–T004); independent of US2.
- **Polish (T012–T013)**: after all implementation/tests.

### File coordination (sequential within a file)

- `internal/server/store/users.go`: T001 → T002 → T007.
- `internal/server/api/admin_user_handlers.go`: T003 → T008.
- `internal/server/store/users_delete_test.go`: T005 → T009.
- `internal/server/api/admin_user_handlers_test.go`: T006 → T010 → T011.

## Parallel Execution Examples

- **Setup**: T001 alone.
- **US1 tests vs route**: T005 (`store/users_delete_test.go`) ∥ T004 (`router.go`) once T002 exists.
- **Polish**: T012 (lint/build) ∥ T013 (test/coverage).

## Implementation Strategy

**MVP** = Phase 1 + US1 (T001–T006): an admin can delete a user and everything they own
is removed from records and the live data plane, with freed addresses reusable. Then US2
hardens it with the admin-safety guards and atomicity assertions, and US3 proves
cross-user integrity. No schema migration; the DB cascade rides the existing foreign-key
actions.

### Decisions (resolved in /speckit-analyze)

- **F1 — keep both guards (option a)**: FR-011 (last-admin) and FR-012 (self-delete) are
  both retained. The self-delete 403 is the user-facing guard; the last-admin guard is a
  store-level invariant tested directly in T009. The handler's 409 `last_admin` mapping
  (T008) is intentionally defensive — it is only reachable at the store level (the sole
  admin is always the caller, so self-delete 403 fires first at the API), and that is
  accepted. Both guards stay.
- **D1 — documented exception (Principle IV)**: the cascade may exceed the 300 ms write
  budget for large accounts; this is recorded as a bounded, justified exception in the
  plan's Complexity Tracking (the 300 ms budget targets steady-state user writes, not a
  one-shot admin maintenance cascade; SC-007's ≤1 s governs here). No constitution
  amendment.
- **C1 — folded in**: T005 now also asserts invite references are nulled (FR-008).
