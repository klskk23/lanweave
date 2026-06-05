# Tasks: Node Online Status

**Feature**: 007-node-online-status | **Branch**: `007-node-online-status`
**Input**: Design documents in `/specs/007-node-online-status/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED per constitution Principle II (NON-NEGOTIABLE). WireGuard is tested
against a real kernel device (never mocked); the tracker's pure logic uses a
function-typed source + injectable clock (our own seams, not system mocks); the handler
is tested against a real SQLite store with a fake in-package status provider.

**Organization**: Tasks are grouped by user story. US1 (P1) is the MVP — the visible
online flag. US2 (P2) hardens freshness/robustness.

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`
`[P]` = parallelizable (different file, no incomplete dependency).

---

## Phase 1: Setup (shared data shapes)

- [X] T001 [P] Add `Online bool` (`json:"online"`) and `LastHandshake string` (`json:"last_handshake,omitempty"`) fields to `NodeResponse` in `pkg/protocol/node.go`
- [X] T002 [P] Add `func (s *Server) Handshakes() (map[string]time.Time, error)` to `internal/server/wg/iface.go` reading `s.wgc.Device(s.name).Peers` and mapping each peer's `PublicKey().String()` → `LastHandshakeTime` (add the `time` import)

**Checkpoint**: protocol carries the new fields; the data plane can report per-peer handshake times.

---

## Phase 2: Foundational (blocking prerequisites for all stories)

- [X] T003 Create `internal/server/status/tracker.go`: package `status` with constants `Threshold = 3 * time.Minute` and `DefaultInterval = 30 * time.Second`; a `Tracker` struct holding `snapshot map[string]time.Time` guarded by `sync.RWMutex`, a `source func() (map[string]time.Time, error)`, `interval time.Duration`, `threshold time.Duration`, `now func() time.Time` (clock seam), and a `*slog.Logger`; constructor `New(source func() (map[string]time.Time, error), interval time.Duration, log *slog.Logger) *Tracker` (defaults `now=time.Now`, `threshold=Threshold`); an unexported `refresh() error` that calls `source` and replaces the snapshot under the write lock (on error returns it without clearing the snapshot); `Online(pubKey string) bool` = present && non-zero && `now-hs < threshold`; `LastHandshake(pubKey string) (time.Time, bool)` (ok=false when absent or zero); and `Run(ctx context.Context)` that calls `refresh()` immediately, then on a `time.Ticker(interval)` calls `refresh()` each tick logging WARN on error (keeping the prior snapshot), and returns on `ctx.Done()`
- [X] T004 Define the `statusProvider` interface (`Online(pubKey string) bool`; `LastHandshake(pubKey string) (time.Time, bool)`) in the api package and add a `status statusProvider` field to the `handlers` struct in `internal/server/api/auth_handlers.go`; add `Status statusProvider` to `Options` and set `status: opts.Status` in `NewRouter` in `internal/server/api/router.go`
- [X] T005 In `app.Run` (`internal/server/app/app.go`) construct the tracker with `status.New(wgServer.Handshakes, status.DefaultInterval, log)`, start it with `go tracker.Run(ctx)` (stops on ctx cancel at shutdown), and pass it via `api.Options{... Status: tracker}`

**Checkpoint**: the tracker polls the real device every 30 s and is reachable from handlers; no endpoint behavior has changed yet.

---

## Phase 3: User Story 1 — 用户查看自己节点的在线状态 (P1) 🎯 MVP

**Goal**: `GET /api/v1/nodes` reports a definite `online` per node (true when last
handshake < 3 min; never-connected = false) plus `last_handshake` for "last seen".

**Independent test**: A node with a recent handshake lists as `online:true` with a
`last_handshake`; a never-connected node lists as `online:false` with no
`last_handshake`; unauthenticated requests are refused.

- [X] T006 [US1] Enrich `listNodes` in `internal/server/api/node_handlers.go` to set, per node, `Online: h.status.Online(n.PubKey)` and, when `ts, ok := h.status.LastHandshake(n.PubKey); ok`, `LastHandshake: ts.Format(time.RFC3339)` (leave it empty/omitted otherwise)
- [X] T007 [P] [US1] Unit test in `internal/server/status/tracker_test.go`: with an injected `now` and a fixed `source`, call `refresh()` then assert `Online`/`LastHandshake` for a recent handshake (online, ok), a handshake older than `Threshold` (offline), a zero-time peer (offline, ok=false), and an absent pubkey (offline, ok=false)
- [X] T008 [US1] Handler test in `internal/server/api/node_handlers_test.go`: build the router over a **real** SQLite store with a **fake** in-package `statusProvider`; seed two nodes (one whose pubkey the fake reports online with a recent handshake, one never-connected) and assert the `GET /nodes` JSON has `online:true` + a `last_handshake` for the first and `online:false` + **absent** `last_handshake` for the second; assert an unauthenticated request → 401
- [X] T009 [P] [US1] Integration test (privileged) in `internal/server/wg/peers_test.go` gated by `testutil.RequireNetAdmin`: bring up a real WG device, `AddPeer` a generated key, and assert `Handshakes()` returns that pubkey with a **zero** time (so `status` would treat it offline) — proving real kernel handshake data is read and mapped

**Checkpoint**: US1 is independently demonstrable — the online flag and last-seen render from real data; never-connected is unambiguously offline.

---

## Phase 4: User Story 2 — 状态及时且健壮 (P2)

**Goal**: status tracks reality within one poll, repopulates after restart, and never
destabilizes the server when the tunnel can't be read.

**Independent test**: across ticks the snapshot updates; a source error keeps the prior
snapshot and does not crash; a fresh tracker reports offline until its first poll; ctx
cancel stops the loop.

- [X] T010 [P] [US2] Unit test in `internal/server/status/run_test.go`: run `Run(ctx)` with a small interval and a `source` whose returned map changes between calls; assert the snapshot reflects the new value after a tick (freshness), and that cancelling `ctx` makes `Run` return promptly
- [X] T011 [P] [US2] Unit test in `internal/server/status/run_test.go`: a fresh tracker (no poll yet) reports every pubkey offline (restart/repopulate baseline); a `source` that returns an error on a later poll leaves the previously-seen snapshot intact and does not panic (FR-008); after a subsequent successful poll the snapshot updates
- [X] T012 [US2] Integration smoke (privileged) in `internal/server/app/status_test.go` gated by `testutil.RequireNetAdmin`: with a real WG device (no client) build a `status.New(srv.Handshakes, ...)`, call `refresh()` (or run one poll), and assert it returns no error and reports a registered node's pubkey offline — the real source path works end-to-end without a live handshake

**Checkpoint**: freshness/robustness guarantees are proven; US2 layers on US1 without changing its contract.

---

## Phase 5: Polish & Cross-Cutting Concerns

- [X] T013 [P] Run `gofmt -w`, `go vet ./...`, and `staticcheck ./...` on the changed packages and `go build ./...`; resolve any findings
- [X] T014 [P] Run `go test ./internal/server/status/... ./internal/server/api/... ./pkg/protocol/...` and the privileged set `unshare -rUn go test ./internal/server/wg/... ./internal/server/app/...`; confirm new code (status package, listNodes enrichment) reaches ≥ 70% line coverage, noting any privileged-only uncovered paths
- [X] T015 Validate quickstart Scenario A (never-connected node → `online:false`, no `last_handshake`) against a built binary under `unshare -rUn`; record B–E as manual real-client scenarios

---

## Dependencies & Execution Order

- **Setup (T001, T002)**: independent of each other → parallel. Block everything else.
- **Foundational (T003, T004, T005)**: T003 and T004 are independent (different files);
  T005 depends on T002 (Handshakes), T003 (Tracker), and T004 (Options.Status). Foundational blocks all stories.
- **US1 (T006–T009)**: T006 depends on T004 (handler field). T008 depends on T006 (tests its behavior). T007 depends on T003. T009 depends on T002. → T006 then T008; T007 and T009 parallel with each other and with T006.
- **US2 (T010–T012)**: T010, T011 depend on T003 (and share `run_test.go` → sequential to each other). T012 depends on T002/T003/T005. US2 is independent of US1 (no shared files except none).
- **Polish (T013–T015)**: after all implementation/tests.

### Story independence

- US1 is the MVP and is fully testable on its own (T006–T009) once Setup+Foundational land.
- US2 adds only tests + a privileged smoke against the already-built tracker; it does not modify US1's files or contract.

## Parallel Execution Examples

- **Setup**: T001 (`pkg/protocol/node.go`) ∥ T002 (`internal/server/wg/iface.go`).
- **US1 tests**: T007 (`status/tracker_test.go`) ∥ T009 (`wg/peers_test.go`) — different files, no shared state; both ∥ T006 (`api/node_handlers.go`).
- **Polish**: T013 (lint/build) ∥ T014 (test/coverage).

## Implementation Strategy

**MVP** = Phase 1 + Phase 2 + Phase 3 (US1): the online flag and last-seen rendered
from real handshake data, with a definite offline for never-connected nodes. Ship/verify
US1, then layer US2's freshness/robustness coverage. No schema migration; status is
derived, ephemeral, and repopulates after restart.
