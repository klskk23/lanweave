---

description: "Task list for Zone Create Auto-Join (015)"
---

# Tasks: Zone Create Auto-Join

**Input**: Design documents from `/specs/015-zone-create-auto-join/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/zones-create.md, quickstart.md

**Tests**: MANDATORY (Constitution Principle II). The server change crosses the
SQLite + nftables boundary, so it ships with real-instance integration tests (no mocks).
Each user story has an acceptance test.

**Organization**: Tasks are grouped by user story. US1 (auto-join on create) is the MVP;
US2 (join others' zone unchanged) is a regression guard with no new production code.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- File paths are repository-relative.

## Path Conventions

Client/server Go monorepo (per plan.md): server under `internal/server/`, client under
`internal/client/`, shared wire types under `pkg/protocol/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Confirm the privileged test path works before changing behavior.

- [X] T001 Sanity-check the real-nftables zone harness is green on the current tree: `unshare -rUn bash -c 'ip link set lo up && go test ./internal/server/api/ -run TestCreateZone -count=1'` (establishes the baseline the new cases extend; if it skips, you lack CAP_NET_ADMIN — re-run under `unshare -rUn`).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The shared wire field both server and client depend on.

**⚠️ CRITICAL**: Blocks every US1 task (server + client) — they reference this field.

- [X] T002 Add optional `NodeID int64` with tag `json:"node_id,omitempty"` to `CreateZoneRequest` in `pkg/protocol/zone.go` (document: `0`/omitted = create-only legacy behavior).

**Checkpoint**: Wire contract updated; user-story work can begin.

---

## Phase 3: User Story 1 - Creating a zone makes me a member immediately (Priority: P1) 🎯 MVP

**Goal**: A `POST /api/v1/zones` carrying the caller's owned `node_id` creates the zone
AND admits that device (membership row + nft set element) atomically; the desktop client
always sends the current device id so the creator appears in the member list with no
second action.

**Independent Test**: Create a zone with an owned `node_id` against the real-nft harness
and assert the device IP is in `zone_<id>` (`setHas`) and listed by `GET .../members`;
on the client, `Controller.CreateZone(name,password)` calls `api.CreateZone` with this
machine's node id.

### Tests for User Story 1 (write FIRST; they fail until implementation) ⚠️

- [X] T003 [P] [US1] Integration (happy path): in `internal/server/api/zone_handlers_test.go`, add `TestCreateZoneAutoJoin` — seed a user + owned node, `POST /api/v1/zones` with `node_id`, expect `201`/`is_owner:true`, assert `h.setHas(zone.ID, node.IP)` is true and `GET /api/v1/zones/{name}/members` includes that node.
- [X] T004 [P] [US1] Integration (security): in the same file, assert that `POST /api/v1/zones` with a `node_id` the caller does NOT own (a bogus id and/or another user's node) returns `404` AND creates nothing — the name is still free (a subsequent owner-less create with that name succeeds, or `GET /api/v1/zones` does not list it) and `h.setExists(newID)` is false.
- [X] T005 [P] [US1] Integration (backward-compat): assert `POST /api/v1/zones` with no `node_id` (and with `node_id:0`) returns `201` and an EMPTY member list — extend/keep `TestCreateZone` green.
- [X] T006 [P] [US1] apiclient test: in `internal/client/apiclient/client_test.go`, drive `CreateZone(name, nodeID, password)` against an `httptest` server and assert the captured request body JSON contains `"node_id":<nodeID>`.
- [X] T007 [P] [US1] panel test: in `internal/client/panel/panel_test.go`, with a fake `api` that records args, assert `Controller.CreateZone(name, password)` calls `api.CreateZone` with `nodeID == this machine's node id` (resolved from the device list via the setup record).

### Implementation for User Story 1

- [X] T008 [US1] Implement auto-join in `createZone` (`internal/server/api/zone_handlers.go`): after validating name/password, if `req.NodeID != 0` call `h.store.Nodes().GetOwned(ctx, id.UserID, req.NodeID)` BEFORE creating the zone (`ErrNodeNotFound` → `404 not_found`); after `Zones().Create` + `netfw.AddZone` succeed, run `Zones().Join(zone.ID, node.ID)` then `netfw.AddMember(zone.ID, node.IP)`; on either failure roll back via `Zones().Delete(zone.ID)` (DB cascade) + best-effort `netfw.DeleteZone(zone.ID)`, then `serverError`. Keep the existing `AddZone`-failure compensation. Add a one-line WHY comment on the rollback only if non-obvious.
- [X] T009 [P] [US1] Change `apiclient.Client.CreateZone` to `CreateZone(name string, nodeID int64, password string)` in `internal/client/apiclient/client.go`, setting `protocol.CreateZoneRequest{Name: name, Password: password, NodeID: nodeID}`.
- [X] T010 [P] [US1] In `internal/client/panel/panel.go`: update the `api` interface method to `CreateZone(name string, nodeID int64, password string)` and make `Controller.CreateZone(name, password)` resolve `c.thisMachineNodeID()` (same helper `JoinZone`/`LeaveZone` use) and pass it to `api.CreateZone`. Keep the `Controller.CreateZone(name, password)` signature so the UI is untouched.
- [X] T011 [US1] Verify `onCreateZone` in `internal/client/ui/panel.go` refreshes the panel after a successful create so the new zone and the creator's membership become visible (FR-007); only adjust if it does not already reload zones/members. No signature change.

**Checkpoint**: US1 fully functional — creating a zone yields immediate membership, proven by real-nft integration tests and the client seam tests.

---

## Phase 4: User Story 2 - Joining someone else's zone still works (Priority: P2)

**Goal**: The existing join-others flow (name + password, foreign node → 404, no
enumeration) is unaffected by the create-auto-join change.

**Independent Test**: A user joins a zone owned by another user via
`POST /api/v1/zones/{name}/join` and becomes a member; wrong password / unknown zone
yield identical `403`.

### Tests for User Story 2 (regression guard) ⚠️

- [X] T012 [US2] Confirm `TestJoinZone` in `internal/server/api/zone_handlers_test.go` remains green unchanged (no production code touches `joinZone`); run `unshare -rUn bash -c 'ip link set lo up && go test ./internal/server/api/ -run 'TestJoinZone|TestLeaveZone|TestListAndMembers' -count=1'`. If any case now relies on create-only semantics that T008 altered, fix the test setup (not the join behavior).

**Checkpoint**: US1 + US2 both pass independently.

---

## Phase 5: Polish & Cross-Cutting Concerns

- [X] T013 [P] If `docs/GUIDE.en.md` / `docs/GUIDE.zh.md` describe creating a zone as a two-step "create then join" flow, update both to state the creator is auto-joined on create (keep en/zh in sync).
- [X] T014 Full validation per quickstart Definition of Done: `gofmt -l .` empty, `go vet ./...`, `staticcheck ./...`, and `unshare -rUn bash -c 'ip link set lo up && go test ./...'` all green.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**: no dependencies.
- **Foundational (T002)**: BLOCKS all of Phase 3 (every US1 task references `NodeID`).
- **US1 (Phase 3)**: depends on T002. Tests T003–T007 are written first and fail
  (T006/T007 fail to compile until T009/T010; T003/T004 fail on behavior until T008;
  T005 should pass before and after).
- **US2 (Phase 4)**: depends on T008 existing (it is the only change that could regress
  join); otherwise independent.
- **Polish (Phase 5)**: after US1 + US2 are green.

### Within User Story 1

- Server: T003/T004/T005 (tests) → T008 (impl).
- Client apiclient: T006 (test) → T009 (impl).
- Client panel: T007 (test) → T010 (impl).
- T011 is a UI verification, after T010.
- T009 and T010 touch different files and can proceed in parallel once T002 lands;
  T008 is independent of both.

### Parallel Opportunities

- Test-writing T003, T004, T005, T006, T007 are all `[P]` (distinct files / distinct
  concerns) and can be authored together.
- Implementation T009 and T010 are `[P]` (different files); T008 is server-side and also
  independent of them.
- T013 is `[P]` (docs only).

---

## Parallel Example: User Story 1

```bash
# Author the failing tests together:
Task: "T003 integration happy-path in internal/server/api/zone_handlers_test.go"
Task: "T004 integration foreign-node-404 in internal/server/api/zone_handlers_test.go"
Task: "T005 integration backward-compat in internal/server/api/zone_handlers_test.go"
Task: "T006 apiclient node_id body in internal/client/apiclient/client_test.go"
Task: "T007 panel injects node id in internal/client/panel/panel_test.go"

# Then implement in parallel where files differ:
Task: "T008 createZone auto-join in internal/server/api/zone_handlers.go"
Task: "T009 apiclient.CreateZone += nodeID in internal/client/apiclient/client.go"
Task: "T010 panel api + Controller.CreateZone inject in internal/client/panel/panel.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. T001 baseline → T002 wire field.
2. Write US1 tests (T003–T007), watch them fail.
3. Implement T008 (server), T009 (apiclient), T010 (panel), verify T011 (UI refresh).
4. **STOP and VALIDATE**: US1 integration + seam tests green; manual Windows create shows
   self in member list.

### Incremental

5. US2 regression guard (T012) green.
6. Polish: docs (T013) + full suite + lint (T014).
7. Final ritual (separate commit): mark 015 ✅ in `docs/ROADMAP.md`.

---

## Notes

- `[P]` = different files, no ordering dependency.
- Real SQLite + real nftables only; never mock them (Constitution II). Client seam tests
  use the fake `api` interface (the HTTP boundary), which is permitted.
- Run all server zone tests under `unshare -rUn bash -c 'ip link set lo up && …'` — a
  fresh netns starts with `lo` DOWN.
- Commit after each logical group; keep `Controller.CreateZone(name, password)` signature
  stable so `internal/client/ui/panel.go` stays untouched.
