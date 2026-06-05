---
description: "Task list for 005-zones-and-nftables-isolation"
---

# Tasks: Zones and nftables Isolation

**Input**: Design documents from `/specs/005-zones-and-nftables-isolation/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present). Builds on features 001–004.

**Tests**: REQUIRED per constitution Principle II. **Tiers**: zone CRUD + membership run on real temp SQLite (unprivileged, no mocks); real nftables set/element/rule state is asserted against the real kernel under root / `unshare -rUn`, skipping with a clear message otherwise (never mocked). Literal two-client packet reachability (SC-001) is a manual quickstart step. Test tasks are written FIRST and must FAIL before implementation. CI MUST run the privileged tier.

**Organization**: Tasks grouped by user story. US1–US4 are P1; US5 (P2) is consistency/rebuild/cleanup.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: different files, no dependency on an incomplete task
- **(privileged)**: needs root / `unshare -rUn`; skips otherwise

## Path Conventions

Single Go project `lanweave`. New: `store/zones.go`, `api/zone_handlers.go`,
`pkg/protocol/zone.go`; extends `netfw/nftables.go`, `store/nodes.go`,
`api/node_handlers.go`, `api/router.go`, `app/dataplane.go`, `app/app.go`.

---

## Phase 1: Setup

- [X] T001 Create migration `internal/server/store/migrations/0004_zones.sql` (zones + zone_members per data-model.md: `zones.name UNIQUE`, `owner_user_id` FK CASCADE; `zone_members` PK `(zone_id,node_id)`, both FKs `ON DELETE CASCADE`)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: DTOs, repo/netfw method surfaces, the node-ownership lookup, and the
dataplane refactor (nft rebuild from the DB) that every story needs.

**⚠️ CRITICAL**: No user story work begins until this phase is complete.

- [X] T002 [P] Define DTOs in `pkg/protocol/zone.go`: `CreateZoneRequest`, `ZoneResponse` (incl. `is_owner`), `ZoneListResponse`, `JoinZoneRequest`, `LeaveZoneRequest`, `ZoneMemberResponse`, `ZoneMembersResponse`
- [X] T003 [P] Create `internal/server/store/zones.go` skeleton: `Zone`, `ZoneMember`, `ZoneWithOwnership`, `ZoneState` types; `ErrZoneNameTaken`/`ErrZoneOrPassword`/`ErrNotMember`; `Store.Zones()` accessor (query methods added per story)
- [X] T004 Add `NodeRepo.GetOwned(ctx, userID, nodeID) (*Node, error)` (returns ip+pubkey or `ErrNodeNotFound`) in `internal/server/store/nodes.go`, with an integration test in `internal/server/store/nodes_test.go`
- [X] T005 [P] (privileged) Test `internal/server/netfw/nftables_test.go` (extend): after `AddZone`+`AddMember`, the `zone_<id>` set holds the element and the same-zone accept rule exists; `RemoveMember` deletes the element; `Rebuild([]ZoneState)` reproduces exactly the given sets/elements/rules — MUST FAIL
- [X] T006 Extend `internal/server/netfw/nftables.go`: add `ZoneState{ID int64; MemberIPs []netip.Addr}`; change `Rebuild` to `Rebuild(zones []ZoneState, log)` (table + forward chain + per-zone set + elements + accept rule, one flush); add `AddZone(id)`, `AddMember(id, ip)`, `RemoveMember(id, ip)` (google/nftables expr per research.md R1); update the existing 003 `Rebuild` call sites and `nftables_test.go` skeleton-case to `Rebuild(nil, …)`
- [X] T007 Add `ZoneRepo.AllForRebuild(ctx) ([]ZoneState, error)` in `internal/server/store/zones.go`; refactor `internal/server/app/dataplane.go` so `setupDataPlane` returns `(*wg.Server, *netfw.Manager, error)` (no nft rebuild inside) and add `rebuildZoneRules(ctx, zoneRepo, mgr, log)`; in `internal/server/app/app.go` call `mgr.Rebuild` via `rebuildZoneRules` from the DB after `rebuildNodePeers`
- [X] T008 Expand `api.Options` with `NetFW *netfw.Manager` and add a `netfw` field to the `handlers` struct in `internal/server/api/router.go`; pass `NetFW` from `internal/server/app/app.go`

**Checkpoint**: `go build ./...` green; bare `go test ./...` green (privileged tests skip); empty-DB startup still builds the table.

---

## Phase 3: User Story 1 — 创建 zone (Priority: P1) 🎯 MVP

**Goal**: A user creates a uniquely named, password-protected zone and becomes owner; the zone's empty set + accept rule are installed.

**Independent Test**: Create with a fresh name → 201 (is_owner); same name again → 409.

### Tests for User Story 1 (REQUIRED) ⚠️

- [X] T009 [P] [US1] Integration test in `internal/server/store/zones_test.go`: `Create` stores the zone with owner; a second `Create` with the same name → `ErrZoneNameTaken`; `GetByName` returns it / nil — MUST FAIL
- [X] T010 [P] [US1] Acceptance test in `internal/server/api/zone_handlers_test.go`: create → 201 `{id,name,is_owner:true}`; duplicate name → 409 `zone_name_taken`; empty name / short password → 400; unauth → 401 — MUST FAIL

### Implementation for User Story 1

- [X] T011 [US1] Implement `ZoneRepo.Create` + `GetByName` in `internal/server/store/zones.go` (`UNIQUE(name)` → `ErrZoneNameTaken`)
- [X] T012 [US1] Implement `createZone` handler in `internal/server/api/zone_handlers.go`: validate name (≤64) + password (≥8); `HashPassword`; insert zone then `NetFW.AddZone(id)`; on AddZone failure delete the zone row (compensate); map → 201/400/409
- [X] T013 [US1] Register route `POST /api/v1/zones` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: zones can be created; each has a set + accept rule.

---

## Phase 4: User Story 2 — 加入 zone 并获得同区可达 (Priority: P1) 🎯 MVP

**Goal**: A user joins one of their nodes by name+password; the node's address enters the zone's set (same-zone admitted). No enumeration; node ownership enforced.

**Independent Test**: Join with correct name+password → 200 and (privileged) the set contains the node's address; wrong password and unknown zone return the identical 403; foreign node → 404; re-join → 200.

### Tests for User Story 2 (REQUIRED) ⚠️

- [X] T014 [P] [US2] Integration test in `internal/server/store/zones_test.go`: `Join` adds membership and is idempotent (second call no error, no duplicate) — MUST FAIL
- [X] T015 [P] [US2] Acceptance test in `internal/server/api/zone_handlers_test.go`: join → 200; re-join → 200; wrong password AND unknown zone → identical 403 `invalid_zone_or_password`; node not owned → 404; (privileged, `RequireNetAdmin`) the `zone_<id>` set contains the node address and the accept rule is present — MUST FAIL

### Implementation for User Story 2

- [X] T016 [US2] Implement `ZoneRepo.Join` (idempotent, `ON CONFLICT DO NOTHING`) in `internal/server/store/zones.go`
- [X] T017 [US2] Implement `joinZone` handler in `internal/server/api/zone_handlers.go`: `GetByName` (dummy-verify on miss), `VerifyPassword`, `GetOwned` (node ownership), `Join`, `NetFW.AddMember` (compensate by leaving on failure); map → 200/400/403/404
- [X] T018 [US2] Register route `POST /api/v1/zones/{name}/join` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: same-zone membership is reflected in the real ruleset.

---

## Phase 5: User Story 3 — 离开 zone (Priority: P1)

**Goal**: A user removes a node from a zone; its address leaves that set only.

**Independent Test**: Leave → 204 and (privileged) the element is gone; a node in two zones that leaves one keeps the other; non-member/foreign/unknown → 404.

### Tests for User Story 3 (REQUIRED) ⚠️

- [X] T019 [P] [US3] Integration test in `internal/server/store/zones_test.go`: `Leave` removes membership; leaving when not a member → `ErrNotMember` — MUST FAIL
- [X] T020 [P] [US3] Acceptance test in `internal/server/api/zone_handlers_test.go`: leave own member → 204 and (privileged) the set element is removed; not a member / foreign node / unknown zone → 404; a two-zone node that leaves one retains the other set — MUST FAIL

### Implementation for User Story 3

- [X] T021 [US3] Implement `ZoneRepo.Leave` (rows-affected → `ErrNotMember`) in `internal/server/store/zones.go`
- [X] T022 [US3] Implement `leaveZone` handler in `internal/server/api/zone_handlers.go`: `GetByName`, `GetOwned`, `Leave`, `NetFW.RemoveMember` best-effort; map → 204/404
- [X] T023 [US3] Register route `POST /api/v1/zones/{name}/leave` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: members can withdraw; only the target zone is affected.

---

## Phase 6: User Story 4 — 查看 zone 与成员 (Priority: P1)

**Goal**: A user lists their zones and views a zone's members (full transparency); non-participants cannot view.

**Independent Test**: After two users join one zone, each lists the zone and sees both members (name/ip/owner); a non-participant gets 404.

### Tests for User Story 4 (REQUIRED) ⚠️

- [X] T024 [P] [US4] Integration test in `internal/server/store/zones_test.go`: `ListForUser` returns owned + participated zones with `is_owner`; `MembersByZone` returns name/ip/owner across users; `IsParticipant` true for owner/member, false otherwise — MUST FAIL
- [X] T025 [P] [US4] Acceptance test in `internal/server/api/zone_handlers_test.go`: list returns the caller's zones; a member views all members (incl. other users' nodes); a non-participant → 404 on members — MUST FAIL

### Implementation for User Story 4

- [X] T026 [US4] Implement `ZoneRepo.ListForUser`, `MembersByZone`, `IsParticipant` in `internal/server/store/zones.go` (joins to `nodes`/`users`)
- [X] T027 [US4] Implement `listZones` + `zoneMembers` handlers in `internal/server/api/zone_handlers.go` (members gated by `IsParticipant` → 404 otherwise)
- [X] T028 [US4] Register routes `GET /api/v1/zones` and `GET /api/v1/zones/{name}/members` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: members can discover each other; the full create→join→view→leave loop works.

---

## Phase 7: User Story 5 — 多区、重启重建、节点删除清理 (Priority: P2)

**Goal**: Multi-zone membership works; restart rebuilds the ruleset from the DB; deleting a node clears it from all zones and sets (no recycled-address inheritance).

**Independent Test**: A node joins two zones → in both sets. Restart → ruleset matches memberships. Delete a member node → gone from all sets; a new node reusing its address is in none of them.

### Tests for User Story 5 (REQUIRED) ⚠️

- [X] T029 [P] [US5] Integration test in `internal/server/store/zones_test.go`: `ZonesForNode` returns all zones a node is in; `AllForRebuild` returns every zone id + member ips — MUST FAIL
- [X] T030 [P] [US5] (privileged) Test in `internal/server/app/dataplane_test.go`: with zones+members seeded, `rebuildZoneRules` reproduces the exact sets/elements/accept-rules; and after deleting a member node its address is absent from the zone sets (FR-018) — MUST FAIL

### Implementation for User Story 5

- [X] T031 [US5] Implement `ZoneRepo.ZonesForNode(ctx, nodeID)` in `internal/server/store/zones.go`
- [X] T032 [US5] Modify `deleteNode` in `internal/server/api/node_handlers.go`: `GetOwned` → `ZonesForNode` → `DeleteOwned` (cascades memberships) → `RemovePeer` → `NetFW.RemoveMember(zoneID, node.IP)` for each former zone (FR-018); preserves the existing 404/204 behavior
- [X] T033 [US5] Confirm multi-zone correctness: a node joined to two zones appears in both sets (extend T015/T030 assertions); reachability is per shared zone (set membership only, no cross-set rule)

**Checkpoint**: isolation is consistent across multi-zone, restart, and node deletion.

---

## Phase 8: Polish & Cross-Cutting Concerns

- [X] T034 Run `make lint` (gofmt + `go vet` + staticcheck) clean; confirm ≥70% coverage on new code (`store` zone methods, `api` zone handlers, `netfw` additions); kernel-path coverage measured under the privileged run
- [X] T035 Execute `quickstart.md` under `unshare -rUn` (create→join→list/members→leave→node-delete→restart, asserting `nft`/ruleset state); document the manual two-client ping for SC-001

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: schema — precondition for everything.
- **Foundational (Phase 2)**: depends on Setup — blocks all stories. T006 (netfw) needs T005(test); T007 needs T006 + AllForRebuild; T008 threads deps.
- **US1 (Phase 3)**: depends on Foundational. Create + AddZone.
- **US2–US4**: depend on Foundational + US1 (a zone must exist to join/list); they extend `zones.go`, `zone_handlers.go`, `router.go` sequentially.
- **US5 (Phase 7)**: depends on US2/US3 (membership) and modifies 004's `deleteNode`.
- **Polish (Phase 8)**: after the targeted stories.

### Critical Path

```
Setup → Foundational → US1 → US2 → US3 → US4 → US5 → Polish
```

### Within Each User Story

- Test tasks (⚠️) written first and MUST FAIL before implementation.
- Repo method → handler → route.

---

## Parallel Opportunities

- **Foundational**: T002 [P] (DTOs), T003 [P] (zones skeleton), T005 [P] (netfw test) are distinct files; T004/T006/T007/T008 sequence (shared files / dependencies).
- **Each story**: the store test [P] and the api/app test [P] are distinct files → parallel.
- Note: `store/zones.go`, `api/zone_handlers.go`, `api/router.go` are touched across US1–US5, so the implementation tasks on those files are sequential between stories.

### Parallel Example: Foundational

```bash
Task T002: zone DTOs (pkg/protocol/zone.go)
Task T003: ZoneRepo skeleton (store/zones.go)
Task T005: netfw set/rule privileged test (netfw/nftables_test.go)
```

---

## Implementation Strategy

### MVP (US1 + US2 + US4)

1. Setup + Foundational (schema, DTOs, netfw set/rule ops, dataplane rebuild wiring).
2. US1 → create a zone.
3. US2 → join a node (same-zone admitted in the real ruleset).
4. US4 → list/members (members discover each other).
5. **STOP & VALIDATE** (privileged): create → join two nodes → the set holds both + accept rule; members list shows both.

### Incremental Delivery

- Add US3 (leave) → withdraw a node.
- Add US5 (consistency) → multi-zone, restart rebuild, node-delete cleanup (the FR-018 recycled-address guard).

---

## Implementation outcomes (analyze findings)

- **U1/I2 (strengthen the real test)**: the privileged nftables test asserts the
  **exact rule shape** — the same-zone accept rule references the set on BOTH source
  and destination (2 lookups), so a one-direction (silently-broken-isolation) rule is
  caught. Verified on the real kernel.
- **C1 (non-transitive)**: a multi-zone node's address is asserted to be in each
  zone's set independently (no cross-set rule), and `TestLeaveZoneMultiZone` confirms
  leaving one zone doesn't affect the other.
- **I1 (join 403)**: kept `403 invalid_zone_or_password` (deliberate: caller is
  authenticated, just lacks zone access) — documented in the handler comment.
- **FR-018 verified at both levels**: the live `deleteNode → RemoveMember` path
  (`TestDeleteNodeClearsZoneMembership`) and the startup-rebuild backstop
  (`TestRebuildZoneRules`).
- **Verified for real** via `unshare -rUn`: per-zone set + both-direction accept
  rule, add/remove/replace elements, restart rebuild matches memberships, node-delete
  clears elements, and a binary smoke (create→join→members→leave→delete→restart).
- **No new dependency** (uses google/nftables expr + `golang.org/x/sys/unix`, already present).

## Notes

- [P] = different files, no dependency on an incomplete task.
- **No mocking** of SQLite or nftables: zone/membership tests use a real DB; set/rule
  state is asserted on the real kernel (privileged) or skipped with a clear message
  (constitution Principle II; research.md R7). Literal cross-client packet reachability
  is the manual quickstart step. CI MUST run the privileged tier.
- Reuses 001–004: auth/JWT middleware, argon2id (`HashPassword`/`VerifyPassword`,
  `DummyVerify`), config + logging + error envelope + rate limiter, `netfw.Manager`
  (003), node IPs (004). Zone passwords hashed, never logged (FR-019).
- **FR-018 is the key consistency task (T032)**: node delete must clear set elements,
  or feature-004 IP recycling lets a new node inherit a deleted node's zone reachability.
- Out of scope (later features): owner controls — change password/kick/delete zone (006);
  full user-deletion cascade (008); client UI (009–011).
