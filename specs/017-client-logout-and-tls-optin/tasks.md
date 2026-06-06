---
description: "Task list for feature 017 — client logout and interactive insecure-TLS opt-in"
---

# Tasks: Client Logout and TLS Opt-In

**Input**: Design documents from `/specs/017-client-logout-and-tls-optin/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/client-operations.md, quickstart.md

**Tests**: Mandatory per constitution Principle II (NON-NEGOTIABLE). US1 crosses the server's real
SQLite + nftables + WireGuard, so it gets an integration acceptance test against a real server (no
mocks of those systems). US2's security-relevant round trip (untrusted cert → `ErrUntrustedCert`;
rebuilt insecure client succeeds) gets an automated `apiclient`-layer acceptance test against a real
self-signed `httptest` TLS server (T013) — so US2 is not GUI-only at the acceptance level.
Orchestration/decision logic is unit-tested through the existing fake `api` seam. Only the Fyne
dialogs/indicator are verified manually on Windows; that GUI manual-verify exception is registered in
`DESIGN.md §11` by T012 so the waiver has documented authority.

**Organization**: Tasks are grouped by user story so each can be implemented and tested
independently. No server code changes — this feature reuses the existing `DELETE /api/v1/nodes/{id}`
endpoint and edits only the client + a DESIGN.md governance amendment.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1 (logout) or US2 (insecure opt-in); Setup/Foundational/Polish carry no story label
- All paths are repository-root-relative

## Path Conventions

Existing Go monorepo. Client code under `internal/client/...` and `cmd/lanweave-client/`. Tests live
beside the code they cover and run under `unshare -rUn bash -c 'ip link set lo up && go test ./...'`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish a known-green baseline so later failures are attributable to this feature.

- [X] T001 Establish baseline: run `unshare -rUn bash -c 'ip link set lo up && go test ./...'` and
  `CGO_ENABLED=0 go build ./cmd/lanweave-client`; confirm both pass before any edits.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: One structural change to the headless `panel` package shape that BOTH stories build on.
Doing it once avoids re-touching the same signature and call sites twice.

**⚠️ CRITICAL**: No user-story work can begin until this phase compiles.

- [X] T002 In `internal/client/panel/panel.go`: add `DeleteNode(nodeID int64) error` to the `api`
  interface; change the constructor to
  `New(a api, record state.Record, keys keyring.Store, statePath string, insecure bool) *Controller`
  storing `statePath` and `insecure` on `Controller`; add an `Insecure() bool` getter. Add the
  compile-time assertion `var _ api = (*apiclient.Client)(nil)` (after T008/T015 land it will hold).
- [X] T003 [P] In `internal/client/panel/panel_test.go`: extend `fakeAPI` with a `DeleteNode` method
  (record the id + a programmable error) and update the `newController` helper to pass `statePath`
  and `insecure` to the new `panel.New` signature.
- [X] T004 [P] Update the two `panel.New` call sites to the new signature (compile fix only, no
  behavior change yet): `cmd/lanweave-client/main.go` (Home branch) and
  `internal/client/ui/wizard.go` (`showHome`) — pass the existing state path and the CLI insecure
  value.

**Checkpoint**: Code compiles with the new `panel` shape and the headless gate still passes.

---

## Phase 3: User Story 1 - Log out and switch server or account (Priority: P1) 🎯 MVP

**Goal**: A confirmed Log out action that disconnects, removes this device's own node on the server,
clears the local session token + device key + `state.json`, and returns to the server-URL setup step.
Remote removal is best-effort; local logout always completes (FR-008).

**Independent Test**: Set up + connect against a real server; Log out; verify the tunnel drops, the
node is gone server-side, local credentials/state are cleared, and the app shows the server-URL step
from which a fresh setup connects.

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

> Write these FIRST and ensure they FAIL before implementing T008–T009.

- [X] T005 [P] [US1] `apiclient.DeleteNode` httptest contract test in
  `internal/client/apiclient/client_test.go`: a `204` returns `nil` and the wire request is
  `DELETE /api/v1/nodes/{id}` with `Authorization: Bearer <token>`; a non-2xx status maps to a
  non-nil error via the shared `mapError`.
- [X] T006 [P] [US1] `Controller.Logout` unit test in `internal/client/panel/panel_test.go` (fake
  `api`): this machine's node present + `DeleteNode` ok → `remoteRemoved=true`; `DeleteNode` failing
  → `remoteRemoved=false`; in BOTH cases the keyring session token, the device key, and `state.json`
  are cleared.
- [X] T007 [US1] Logout integration acceptance test in
  `internal/client/panel/panel_integration_test.go` against a **real** server (real SQLite +
  nftables + `wireguard-go` in the netns): onboard a node, `Logout()`, then assert the node is gone
  from `ListNodes`, its WireGuard peer is removed, its address is gone from any zone nft set, and the
  local device key + `state.json` are cleared. (US1 end-to-end acceptance.)

### Implementation for User Story 1

- [X] T008 [P] [US1] Implement `func (c *Client) DeleteNode(nodeID int64) error` in
  `internal/client/apiclient/client.go`: issue `DELETE /api/v1/nodes/{id}` through the shared
  `do`/`mapError` path with the bearer token; expect `204`. Makes T005 pass.
- [X] T009 [P] [US1] Implement `Controller.Logout() (remoteRemoved bool, err error)` in
  `internal/client/panel/panel.go`: resolve this machine's node id via the existing
  `thisMachineNodeID()`/`ListNodes`; call `DeleteNode` best-effort (network fail → `false`; node
  absent → `true`; delete ok → `true`, delete fail → `false`); ALWAYS delete the keyring session
  token + device key and call `state.Clear(c.statePath)`, joining local errors into `err`. Does NOT
  touch the tunnel or navigate. Makes T006 pass and supports T007.
- [X] T010 [US1] (gui) In `internal/client/ui/panel.go`: add a `restart func()` parameter to
  `NewPanel`; add a "Log out" control placed away from the connect/zone primary controls (FR-001);
  on click show a confirm dialog that NAMES this device + server and states it will disconnect,
  remove this device's node, and require re-entering the server URL (FR-002, Principle III); on
  confirm run `tn.Disconnect()` then `ctrl.Logout()`, show an informational notice if
  `remoteRemoved == false` that the node may still be registered (FR-008), then invoke `restart()`.
- [X] T011 [US1] (gui) Supply the `restart` closure at both `NewPanel` call sites —
  `cmd/lanweave-client/main.go` (Home branch) and `internal/client/ui/wizard.go` (`showHome`) —
  as `func(){ NewWizard(win, statePath, keys, cliInsecure).Start() }`, returning the user to the
  server-URL step (FR-006).

**Checkpoint**: US1 is fully functional and independently testable (MVP). Logout works end-to-end.

---

## Phase 4: User Story 2 - Proceed past an unverifiable certificate, deliberately (Priority: P2)

**Goal**: On a certificate-verification failure (`apiclient.ErrUntrustedCert`), show an explicit
opt-in prompt instead of an opaque failure; accepting rebuilds that session's API client with
verification disabled and shows a persistent "certificate not verified" indicator. Per-session,
never persisted; the `--insecure` CLI flag is retained.

**Independent Test**: Point setup at a self-signed server; confirm the opt-in prompt appears;
accepting connects + shows the persistent indicator; declining refuses; a restart verifies again by
default; `--insecure` connects without the prompt but still shows the indicator.

### Governance gate (constitution — DESIGN authority)

- [X] T012 [US2] Amend `DESIGN.md` §275 and §360 (§11 accepted-risks register) **in this same PR** to
  permit the reactive UI insecure opt-in (only on a real verification failure, explicit
  confirmation, per-session/never persisted, persistent warning indicator, `--insecure` CLI flag
  retained) while preserving the "no mindless toggle" intent. In the same §11 edit, register the
  standing exception that GUI-only surfaces (Fyne dialogs/indicators) are verified by the manual
  `quickstart.md` matrix rather than an automated acceptance test — giving constitution Principle
  II's per-story acceptance requirement a documented waiver for the GUI layer. Required before the UI
  changes below are legitimate.

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

> Write these FIRST and ensure they FAIL before implementing T015–T016.

- [X] T013 [P] [US2] `apiclient` insecure acceptance test in
  `internal/client/apiclient/client_test.go`, two parts: (a) `Insecure()` getter — a client built
  with `WithInsecure()` reports `true`, a default client reports `false`; (b) **automated end-to-end
  acceptance for US2** — stand up an `httptest.NewTLSServer` (self-signed cert); assert a default
  `apiclient.New(...)` call returns `ErrUntrustedCert`, and that an `apiclient.New(..., WithInsecure())`
  call to the same server succeeds. This is the headless acceptance test for the cert-failure →
  continue-insecurely path (SC-005), independent of the Fyne layer.
- [X] T014 [P] [US2] `Controller.UseInsecureClient` / `Insecure()` unit test in
  `internal/client/panel/panel_test.go` (fake `api`): after `UseInsecureClient(fake2)`, subsequent
  calls route to `fake2`, `Insecure()` is `true`, and the cached session token was re-applied to
  `fake2`.

### Implementation for User Story 2

- [X] T015 [P] [US2] Implement `func (c *Client) Insecure() bool` in
  `internal/client/apiclient/client.go` returning whether the client was built with verification
  disabled; update the `WithInsecure` doc comment. Makes T013 pass.
- [X] T016 [US2] Implement `Controller.UseInsecureClient(a api)` in `internal/client/panel/panel.go`:
  swap `c.api` to the supplied (insecure) client, set `c.insecure = true`, and re-apply the cached
  session token read from the keyring so the session survives the swap. Makes T014 pass.
- [X] T017 [US2] (gui) In `internal/client/ui/wizard.go`: on an `apiclient.ErrUntrustedCert` from
  `runProvision`, show the opt-in dialog (FR-009/010); on accept set `z.insecure = true` and re-run
  `runProvision` (rebuilds the client insecure), on decline make no connection (FR-011); render a
  persistent "⚠ certificate not verified" line while `z.insecure`; retain the ORIGINAL `--insecure`
  CLI value in a separate immutable field set in `NewWizard` and pass THAT (not the flipped live
  value) into the logout `restart` closure (research Decision 5; FR-012).
- [X] T018 [US2] (gui) In `internal/client/ui/panel.go`: when a running-client operation returns
  `apiclient.ErrUntrustedCert`, show the same opt-in dialog; on accept build a bare insecure client
  (`apiclient.New(rec.ServerURL, apiclient.WithInsecure())`), call `ctrl.UseInsecureClient(...)`, and
  retry the operation; render a persistent "⚠ certificate not verified" banner whenever
  `ctrl.Insecure()` is true — covering the `--insecure` CLI-flag entry point as well (FR-013/014).

**Checkpoint**: US1 and US2 both work independently. Certificate failures now have a deliberate path.

---

## Phase 5: Polish & Cross-Cutting Concerns

- [X] T019 [P] Confirm the non-graphical stub still builds: `CGO_ENABLED=0 go build ./cmd/lanweave-client`.
- [X] T020 Run the full gate green: `unshare -rUn bash -c 'ip link set lo up && go test ./...'`
  (includes the US1 integration test and the US1/US2 unit tests).
- [ ] T021 Execute the `quickstart.md` manual verify matrix (12 rows) on a Windows desktop against a
  test server (self-signed for the insecure rows) — the GUI acceptance gate for US1 + US2 surfaces.
- [X] T022 Mark the `docs/ROADMAP.md` 017 row done (✅) in the merge commit.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**: none — run first to confirm a green baseline.
- **Foundational (T002–T004)**: depends on Setup; T002 must land before T003/T004 (T003, T004 are
  parallel to each other). BLOCKS both user stories.
- **US1 (T005–T011)**: depends on Foundational. The MVP — can ship alone.
- **US2 (T012–T018)**: depends on Foundational. Independent of US1; can be built in parallel by a
  second person once Foundational is done. T012 (DESIGN amendment) gates T017/T018.
- **Polish (T019–T022)**: depends on the stories being delivered.

### Within US1

- Tests T005, T006, T007 before implementation; T005/T006 are [P] (different files), T007 is the
  real-server integration test.
- T008 and T009 are [P] (different files: `apiclient/client.go` vs `panel/panel.go`); the controller
  reaches `DeleteNode` through the `api` interface, so it doesn't need T008 to compile, but T007
  needs T008 at runtime.
- T010 depends on T009 (calls `ctrl.Logout`); T011 depends on T010 (`NewPanel` restart param).

### Within US2

- T012 first (governance gate). Tests T013, T014 [P] before impl.
- T013(b) (the self-signed round trip) needs only the existing `apiclient` (`New` + `WithInsecure`)
  and can be written/passing immediately; T015 makes T013(a) (the `Insecure()` getter assertion)
  pass. T016 makes T014 pass (uses the `insecure` field + getter from T002).
- T017 depends on T012 + the existing `WithInsecure`; T018 depends on T012 + T016 (+ T015 for the
  indicator source). T017 and T018 edit different gui files and can be [P] once their deps are met.

### Parallel Opportunities

- T003 ∥ T004 (foundational, different files).
- US1 tests T005 ∥ T006; US1 impl T008 ∥ T009.
- US2 tests T013 ∥ T014; US2 gui T017 ∥ T018 (after T012/T015/T016).
- Whole stories: with two people, one takes US1 (T005–T011) and one takes US2 (T012–T018) after
  Foundational completes.

---

## Parallel Example: User Story 1

```bash
# Tests first (different files → parallel):
Task: "T005 apiclient.DeleteNode httptest test in internal/client/apiclient/client_test.go"
Task: "T006 Controller.Logout unit test in internal/client/panel/panel_test.go"

# Implementation (different files → parallel):
Task: "T008 apiclient.DeleteNode in internal/client/apiclient/client.go"
Task: "T009 Controller.Logout in internal/client/panel/panel.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. T001 Setup → green baseline.
2. T002–T004 Foundational → new `panel` shape compiles.
3. T005–T011 US1 → logout works end-to-end.
4. **STOP and VALIDATE**: run T007 integration + manual logout rows of quickstart. Ship.

### Incremental Delivery

1. Setup + Foundational → ready.
2. US1 → integration-tested → deliver (MVP).
3. US2 → unit-tested + DESIGN amended → manual GUI rows → deliver.
4. Polish (T019–T022) → close out, check off ROADMAP.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- No server code changes; logout reuses the existing owner-enforced `DELETE /api/v1/nodes/{id}`.
- No schema/migration changes; `state.Record.SchemaVersion` stays 1; the per-session insecure flag is
  in-memory only (FR-012).
- The `CLAUDE.md` SPECKIT marker was already pointed at this plan during `/speckit-plan`.
- Commit after each task or logical group; verify tests fail before implementing.
