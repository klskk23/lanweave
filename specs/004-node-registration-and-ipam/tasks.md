---
description: "Task list for 004-node-registration-and-ipam"
---

# Tasks: Node Registration and IPAM

**Input**: Design documents from `/specs/004-node-registration-and-ipam/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present). Builds on features 001–003 (auth, store, wg.Server, app lifecycle).

**Tests**: REQUIRED per constitution Principle II. **Tiers**: IPAM math + node CRUD/allocation run on real temp SQLite (unprivileged, no mocks); real WireGuard peer add/remove + startup rebuild run under root / `unshare -rUn`, skipping with a clear message otherwise (never mocked). Test tasks are written FIRST and must FAIL before implementation. CI MUST run the privileged tier.

**Organization**: Tasks grouped by user story. US1+US2+US3 are P1 (register/list/delete — the MVP); US4 (P2) hardens IPAM correctness + restart peer rebuild.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: different files, no dependency on an incomplete task
- **(privileged)**: needs root / `unshare -rUn`; skips otherwise

## Path Conventions

Single Go project `lanweave`. New: `internal/server/ipam`, `store/nodes.go`,
`api/node_handlers.go` + `api/server_handler.go`, `pkg/protocol/node.go`.

---

## Phase 1: Setup

- [X] T001 Add `wireguard.endpoint` to config in `internal/server/config/config.go` (field on `WireGuardConfig`; validate non-empty `host:port`); add it to `config.toml.example`; update the `validConfig` helper in `internal/server/config/config_test.go` and the `writeConfig` helper in `internal/server/app/app_test.go` so existing suites still pass

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: IPAM math, schema, DTOs, peer methods, repo/router/app scaffolding shared by all stories.

**⚠️ CRITICAL**: No user story work begins until this phase is complete.

- [X] T002 [P] Unit test `internal/server/ipam/ipam_test.go`: `PoolRange` for /16,/24,/30 (first client = base+2, last = broadcast−1), a too-small range (/31,/32) errors, and `Uint32ToAddr`/`AddrToUint32` round-trip — MUST FAIL before T003
- [X] T003 [P] Implement `internal/server/ipam/ipam.go`: `PoolRange(cidr) (first, last uint32, err error)`, `Uint32ToAddr`, `AddrToUint32` (net/netip; IPv4 only)
- [X] T004 [P] Create migration `internal/server/store/migrations/0003_nodes.sql` (nodes table per data-model.md: `ip INTEGER UNIQUE`, `wg_pubkey UNIQUE`, `UNIQUE(user_id,name)`, `user_id` FK `ON DELETE CASCADE`)
- [X] T005 [P] Define DTOs in `pkg/protocol/node.go`: `RegisterNodeRequest`, `NodeResponse`, `NodeListResponse`, `ServerInfoResponse`
- [X] T006 [P] (privileged) Test `internal/server/wg/peers_test.go`: `AddPeer` makes a peer with the given pubkey + `ip/32` allowed-ip; `RemovePeer` removes it; `ReplacePeers` sets exactly the given set — MUST FAIL
- [X] T007 Add peer methods to `internal/server/wg/iface.go`: `PeerConfig{PublicKey string; IP netip.Addr}`, `Server.AddPeer(pub, ip)`, `Server.RemovePeer(pub)`, `Server.ReplacePeers([]PeerConfig)` (wgctrl `ConfigureDevice`; allowed-ip = ip/32)
- [X] T008 Create `internal/server/store/nodes.go` skeleton: `Node` struct, `ErrNodeNameTaken`/`ErrPubKeyTaken`/`ErrPoolExhausted`/`ErrNodeNotFound`, `Store.Nodes()` accessor, and a constraint-name detector (distinguish `nodes.ip` vs `nodes.wg_pubkey` vs `nodes.user_id, nodes.name` from the unique-violation message)
- [X] T009 Expand `api.Options` in `internal/server/api/router.go` with `WG *wg.Server` and `WGConfig config.WireGuardConfig`; pass them into the `handlers` struct (routes added per story)
- [X] T010 Thread `WG` + `WGConfig` into `api.NewRouter` from `internal/server/app/app.go` (the data-plane `wg.Server` from `setupDataPlane`); leave a placeholder for the US4 peer rebuild

**Checkpoint**: `go build ./...` green; bare `go test ./...` green (privileged tests skip).

---

## Phase 3: User Story 1 — 注册节点并取得隧道配置 (Priority: P1) 🎯 MVP

**Goal**: A user registers a node → gets the lowest free address, a tunnel peer is added, and server-info is available.

**Independent Test**: Register a node → 201 with id+name+ip; GET /server → pubkey/endpoint/network/mtu; (privileged) a peer with the pubkey + ip/32 exists.

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

- [X] T011 [P] [US1] Integration test `internal/server/store/nodes_test.go` (real temp DB): `Create` assigns the first client address, a second assigns the next (ascending); duplicate name → `ErrNodeNameTaken`; duplicate pubkey → `ErrPubKeyTaken` — MUST FAIL
- [X] T012 [P] [US1] Acceptance test `internal/server/api/node_handlers_test.go`: register → 201 `{id,name,ip}`; `GET /server` → 200 with all fields; invalid pubkey → 400; unauth → 401; (privileged) `RequireNetAdmin` block asserts the peer is present after register — MUST FAIL

### Implementation for User Story 1

- [X] T013 [US1] Implement `NodeRepo.Create(ctx, userID, name, pubKey, first, last uint32)` in `internal/server/store/nodes.go`: lowest-free query (research.md R2) + INSERT, retry on `nodes.ip` conflict (bounded), map name/pubkey conflicts to typed errors, no candidate → `ErrPoolExhausted`
- [X] T014 [US1] Implement `registerNode` in `internal/server/api/node_handlers.go`: validate name (≤64) + `wgtypes.ParseKey`; `ipam.PoolRange` from `WGConfig.Network`; `Create`; `WG.AddPeer`; on peer-add failure delete the node and return 500; map errors → 201/400/409/503
- [X] T015 [US1] Implement `GET /server` in `internal/server/api/server_handler.go`: return `ServerInfoResponse{public_key: WG.PublicKey(), endpoint, network, mtu}` from `WGConfig` + `wireguard.endpoint`
- [X] T016 [US1] Register routes `POST /api/v1/nodes` and `GET /api/v1/server` (both `AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: US1 works; a user can register and get tunnel config.

---

## Phase 4: User Story 2 — 查看自己的节点 (Priority: P1) 🎯 MVP

**Goal**: A user lists only their own nodes.

**Independent Test**: Register two nodes, list → both appear; another user's list excludes them; no nodes → empty list.

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

- [X] T017 [P] [US2] Integration test in `internal/server/store/nodes_test.go`: `ListByUser` returns only the owner's nodes, newest first; a different user gets none — MUST FAIL
- [X] T018 [P] [US2] Acceptance test in `internal/server/api/node_handlers_test.go`: list returns the caller's nodes with id/name/ip/created_at; a second user does not see them; empty list for a user with no nodes — MUST FAIL

### Implementation for User Story 2

- [X] T019 [US2] Implement `NodeRepo.ListByUser(ctx, userID)` in `internal/server/store/nodes.go` (newest first; ip → dotted in the mapped `Node`)
- [X] T020 [US2] Implement `listNodes` handler in `internal/server/api/node_handlers.go` (scope to caller identity)
- [X] T021 [US2] Register route `GET /api/v1/nodes` (`AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: US1 + US2 → register and review nodes.

---

## Phase 5: User Story 3 — 删除自己的节点 (Priority: P1) 🎯 MVP

**Goal**: A user deletes an owned node → address freed, peer removed; others' nodes untouchable.

**Independent Test**: Register then delete → 204, gone from list, (privileged) peer removed, address reusable; delete other's/nonexistent → 404.

### Tests for User Story 3 (REQUIRED per constitution Principle II) ⚠️

- [X] T022 [P] [US3] Integration test in `internal/server/store/nodes_test.go`: `DeleteOwned` removes the node and returns its pubkey; the address is then free; not-owned or nonexistent → `ErrNodeNotFound` and no change — MUST FAIL
- [X] T023 [P] [US3] Acceptance test in `internal/server/api/node_handlers_test.go`: delete own → 204 (and, privileged, peer gone); delete another user's node → 404; nonexistent → 404 — MUST FAIL

### Implementation for User Story 3

- [X] T024 [US3] Implement `NodeRepo.DeleteOwned(ctx, userID, nodeID) (pubKey string, err error)` in `internal/server/store/nodes.go` (`DELETE ... WHERE id=? AND user_id=?`; RowsAffected 0 → `ErrNodeNotFound`)
- [X] T025 [US3] Implement `deleteNode` handler in `internal/server/api/node_handlers.go` (`DeleteOwned`; then `WG.RemovePeer` best-effort, log on failure; 204 / 404)
- [X] T026 [US3] Register route `DELETE /api/v1/nodes/{id}` (`AuthRequired`, `r.PathValue("id")`) in `internal/server/api/router.go`

**Checkpoint**: Full node CRUD; deletion frees the address (sets up US4 recycle).

---

## Phase 6: User Story 4 — 地址分配正确性 + 重启重建 (Priority: P2)

**Goal**: Lowest-free recycle, concurrency-distinct addresses, clear pool exhaustion, and node peers rebuilt from the DB at startup.

**Independent Test**: Delete a middle node, register → freed address reused; 50 concurrent registrations → all distinct; exhaust a tiny pool → clear error; restart → all peers restored.

### Tests for User Story 4 (REQUIRED per constitution Principle II) ⚠️

- [X] T027 [P] [US4] Integration test in `internal/server/store/nodes_test.go` (real DB, `-race`): after deleting a middle node the next `Create` reuses the freed lowest address; 50 concurrent `Create` calls yield 50 distinct addresses (no collision); a 1-slot pool exhausts with `ErrPoolExhausted` and creates no node — MUST FAIL
- [X] T028 [P] [US4] (privileged) Test `internal/server/app/dataplane_test.go`: register several nodes, then `rebuildNodePeers` restores exactly those peers (pubkey + ip/32) on the interface (SC-007) — MUST FAIL

### Implementation for User Story 4

- [X] T029 [US4] Implement `NodeRepo.AllForPeers(ctx) ([]Node, error)` in `internal/server/store/nodes.go` (every node's pubkey + ip)
- [X] T030 [US4] Implement `rebuildNodePeers(ctx, repo, srv, log)` in `internal/server/app/dataplane.go` (load `AllForPeers` → `srv.ReplacePeers`); wire it into `app.Run` right after `setupDataPlane` (FR-018)
- [X] T031 [US4] Verify allocation under `-race` and confirm the exhaustion path: bounded ip-conflict retry, `ErrPoolExhausted` surfaced as 503; no node created on exhaustion

**Checkpoint**: IPAM correct under reuse/concurrency; nodes survive restart.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T032 Run `make lint` (gofmt + `go vet` + staticcheck) clean; confirm ≥70% coverage on new code (`ipam`, `store` node methods, `api` handlers); kernel-path coverage measured under the privileged run
- [X] T033 Execute `quickstart.md` under `unshare -rUn` (or root): register→list→delete→recycle, server-info, 409/503 conflicts, and restart peer rebuild (SC-007); spot-check register latency within budget

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: config endpoint — precondition for server-info and for boot.
- **Foundational (Phase 2)**: depends on Setup — blocks all stories. T003 needs T002; T007 needs T006(test); T009/T010 thread deps.
- **US1 (Phase 3)**: depends on Foundational. Create + register + server-info.
- **US2 (Phase 4)** / **US3 (Phase 5)**: depend on Foundational + US1's repo/handlers existing (they extend `nodes.go`, `node_handlers.go`, `router.go`).
- **US4 (Phase 6)**: depends on US1 (Create) and US3 (DeleteOwned) for recycle; adds AllForPeers + startup rebuild.
- **Polish (Phase 7)**: after the targeted stories.

### Critical Path

```
Setup → Foundational → US1 → US2 → US3 → US4 → Polish
```

### Within Each User Story

- Test tasks (⚠️) written first and MUST FAIL before implementation.
- Repo method → handler → route.

---

## Parallel Opportunities

- **Foundational**: T002/T004/T005/T006 [P] (distinct files); T003 after T002; T007 after T006; T008/T009/T010 sequence.
- **US1**: T011 [P] (store test) + T012 [P] (api test).
- **US2/US3/US4**: each story's store test [P] + api/app test [P] in parallel.
- Note: `store/nodes.go`, `api/node_handlers.go`, `api/router.go` are touched by US1–US4, so the implementation tasks on those files are sequential across stories.

### Parallel Example: Foundational

```bash
Task T002: ipam unit test (ipam/ipam_test.go)
Task T004: 0003_nodes.sql migration
Task T005: node DTOs (pkg/protocol/node.go)
Task T006: wg peers privileged test (wg/peers_test.go)
```

---

## Implementation Strategy

### MVP (US1 + US2 + US3)

1. Setup + Foundational (ipam, schema, peers, DTOs, scaffolding).
2. US1 → register + server-info (a user can obtain a tunnel config).
3. US2 → list own nodes.
4. US3 → delete own node (frees address + peer).
5. **STOP & VALIDATE** (privileged): register a node, see the peer, list, delete, peer gone.

### Incremental Delivery

- Add US4 → prove recycle/concurrency/exhaustion and restart peer rebuild.

---

## Implementation outcomes (analyze findings)

- **I1 (peer-add-failure code)**: the compensating-rollback 500 reuses the
  `internal_error` envelope (via `serverError`) so every response carries a stable code.
- **U1 (broadcast reserved)**: `ipam.PoolRange` reserves both `.0` (network) and the
  broadcast address (`lastClient = broadcast−1`); covered by `ipam_test`.
- **A1 (pool exhaustion = 503)**: kept `503 pool_exhausted` (transient/retryable
  semantics — an address frees up when a node is deleted).
- **Concurrency without `_txlock`**: allocation safety rests on `UNIQUE(ip)` +
  bounded retry; verified under `-race` (T027) and a 50-way HTTP/store burst.
- **Verified for real** via `unshare -rUn`: peer add/remove/replace, register→peer
  present, delete→peer gone, startup peer rebuild, and a binary smoke (register→
  list→delete→recycle .2→restart rebuilds peers).
- **New config field** `wireguard.endpoint` added; example + config/app test helpers updated.

## Notes

- [P] = different files, no dependency on an incomplete task.
- **No mocking** of SQLite or WireGuard: allocation runs on a real DB; peer ops run on the real kernel (privileged) or skip with a clear message (constitution Principle II; research.md R2/R5). CI MUST run the privileged tier.
- Reuses 001–003: auth + JWT middleware, config + logging + error envelope + rate limiter, `wg.Server`, `app.Run` lifecycle. Concurrency safety rests on `UNIQUE(ip)` + retry (no `_txlock` dependency — addresses feature-003 analyze finding I1).
- The server never handles a client private key (FR-019).
- Out of scope (later features): zones/reachability (005), user-deletion cascade peer/address cleanup (008), online status (007), client UI (009–011).
