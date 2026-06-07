---
description: "Task list for feature 025 — client-logout-hardening"
---

# Tasks: Client Logout Hardening

**Input**: Design documents from `/specs/025-client-logout-hardening/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/logout-controller.md, quickstart.md

**Tests**: REQUIRED per constitution Principle II. This is a client behavioral
change crossing the network boundary; each user story gets headless acceptance
tests against a real `apiclient.Client` (httptest) and/or a fake `api` boundary
with an injected `sleep` seam (no wall-clock sleeps). The GUI two-button dialog
is the single manually-verified surface (`//go:build gui`, quickstart Part B).

**Organization**: Tasks grouped by user story (US1/US2 = P1, US3 = P2).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on incomplete tasks)
- All client paths are under `internal/client/`.

**Test invocation** (loopback must be UP for httptest on 127.0.0.1):

```sh
unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/...'
```

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Confirm the baseline is green before changing logout behavior.

- [X] T001 Run the existing client suite green as a baseline: `unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/...'` and note current `panel` / `apiclient` test names that touch logout (`panel_test.go`, `panel_integration_test.go`, `apiclient/client_test.go`).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared scaffolding the state machine and every story's tests depend on.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T002 Define the `LogoutOutcome` type and constants `LogoutDone`, `LogoutBlocked`, `LogoutNeedSignIn` (iota) with doc comments in `internal/client/panel/panel.go` (per `data-model.md` outcome table).
- [X] T003 Add an injectable retry-delay seam to `Controller` in `internal/client/panel/panel.go`: unexported `sleep func(time.Duration)` defaulting to `time.Sleep` in `New`, plus an unexported test setter (e.g. `setSleep`) used by `panel_test.go`; import `time`. Production behavior unchanged (real 1 s sleeps).
- [X] T004 [P] Add the bilingual logout-hardening i18n keys to BOTH `internal/client/i18n/en.json` and `internal/client/i18n/zh-Hans.json`: `panel.logoutBlockedTitle`, `panel.logoutBlockedBody`, `panel.logoutCancel`, `panel.logoutForce`, `panel.logoutNeedSignIn` (identical key sets so `i18n_test.go` parity passes). en = natural English; zh-Hans = 简体中文.
- [X] T005 Confirm `internal/client/i18n/i18n_test.go` key-parity test passes with the new keys: `unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/i18n/...'`.

**Checkpoint**: Types, retry seam, and translations exist; stories can begin.

---

## Phase 3: User Story 1 - Block logout when the server is unreachable (Priority: P1) 🎯 MVP

**Goal**: Logout no longer wipes local state on a network-unreachable server. The
remote node removal is retried ≤3× at 1 s, and a pure-unreachable failure returns
`LogoutBlocked` with zero local mutation; otherwise it completes (`LogoutDone`).

**Independent Test**: With the `api` boundary returning `apiclient.ErrUnreachable`
three times, `Logout()` returns `LogoutBlocked`, keyring + state are unchanged,
`firewall.Clear` is never called, and the injected `sleep` was called twice with
`1 * time.Second`.

### Tests for User Story 1 (write first; MUST fail before impl) ⚠️

- [X] T006 [US1] In `internal/client/panel/panel_test.go`, extend the fake `api` to drive `DeleteNode`/`ListNodes` outcomes (return `apiclient.ErrUnreachable` a configurable number of times, then a chosen result) and record calls; add a `sleep` recorder via the T003 setter.
- [X] T007 [US1] Add `TestLogoutBlockedOnUnreachable` in `internal/client/panel/panel_test.go`: 3× `ErrUnreachable` → `LogoutBlocked`; assert keyring `SessionTokenName`/`RefreshTokenName`/`DeviceKeyName` and the state file are unchanged, `firewall.Clear` NOT called, and `sleep` called exactly twice with `1*time.Second` (INV-1, INV-4). Verify it FAILS first.
- [X] T008 [US1] Add `TestLogoutRetryBoundedThenSucceeds` in `internal/client/panel/panel_test.go`: `ErrUnreachable` twice then a successful delete → `LogoutDone`, `sleep` called twice, local material cleared. Verify it FAILS first.

### Implementation for User Story 1

- [X] T009 [US1] Rework `removeRemoteNode` in `internal/client/panel/panel.go` into a bounded-retry resolver: at most 3 attempts, `c.sleep(1*time.Second)` between attempts, retry ONLY on `errors.Is(err, apiclient.ErrUnreachable)`; return a small result distinguishing removed/absent (done) vs network-unreachable (block) vs auth-expired (need-sign-in) vs other-reachable-error (proceed-with-warn). Treat 404 (`ErrZoneNotFound`) and `errNotRegistered` as removed (INV-3).
- [X] T010 [US1] Rewrite `Controller.Logout()` in `internal/client/panel/panel.go` to the new signature `Logout() (LogoutOutcome, error)`: on network-unreachable-after-3 → return `(LogoutBlocked, nil)` doing NO teardown and NOT calling `firewall.Clear()`; on removed/absent → `firewall.Clear()` + clear keyring (session+RT+device key) + `state.Clear()` → return `(LogoutDone, joinedLocalErr)`. (RT-revoke + need-sign-in branches are added in US2.)
- [X] T011 [US1] Update the GUI `confirmLogout` in `internal/client/ui/panel.go` (`//go:build gui`) to consume `LogoutOutcome`: remove the pre-removal `p.tn.Disconnect()` (removal now happens with the tunnel up); on `LogoutDone` disconnect the tunnel then `p.restart()`; on `LogoutBlocked` hide the progress bar and show a TWO-button dialog (`panel.logoutBlockedTitle`/`Body`, buttons `panel.logoutCancel` default / `panel.logoutForce`) where Cancel is a no-op (stay signed in). Keep the existing infinite-progress indicator during removal. (Force button is wired in US3.)
- [X] T012 [US1] Run US1 tests green and confirm no regression in `internal/client/ui/panel_overflow_test.go`: `unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/panel/... ./internal/client/ui/...'`.

**Checkpoint**: Unreachable-server logout is blocked with no local change; reachable logout completes. MVP demoable.

---

## Phase 4: User Story 2 - Clean logout removes the remote node and revokes this device (Priority: P1)

**Goal**: On a reachable server, logout removes the node first, revokes this
device's refresh token (slice 024 endpoint), clears all local material, and
handles already-absent and expired-session cases.

**Independent Test**: Against a real httptest/server, `Logout()` deletes the node,
calls `POST /api/v1/logout` (the device's `refresh_tokens` row gets `revoked_at`
set), clears local material, and returns `LogoutDone`.

### Tests for User Story 2 (write first; MUST fail before impl) ⚠️

- [X] T013 [US2] Add `TestLogoutCleanRemovesAndRevokes` in `internal/client/panel/panel_integration_test.go` (real server harness): after `Logout()`, assert the node is gone from the server, keyring + state are cleared, and `LogoutDone`. Assert RT revocation **behaviorally** (not by DB introspection — the `realServer` harness returns only url/cert/mintInvite, not the store): capture the RT before logout, then after logout call `apiclient.Refresh()` with that RT on a fresh client and expect it to FAIL (`ErrRefreshFailed`/`ErrSessionExpired`), proving the server revoked it. Verify the test FAILS first.
- [X] T014 [US2] Add `TestLogoutAlreadyAbsentIsDone` in `internal/client/panel/panel_test.go`: node missing (DeleteNode → `ErrZoneNotFound`, or not in `ListNodes`) → `LogoutDone`, not blocked; local cleared (INV-3). Verify it FAILS first.
- [X] T015 [US2] Add `TestLogoutNeedSignInOnRefreshFail` in `internal/client/panel/panel_test.go`: fake `api` returns `apiclient.ErrSessionExpired` (refresh unavailable/failing) on `DeleteNode` → `LogoutNeedSignIn`, NO local mutation, `firewall.Clear` not called. Verify it FAILS first.

### Implementation for User Story 2

- [X] T016 [US2] In `Controller.Logout()` (`internal/client/panel/panel.go`), on the done path add best-effort `_ = c.api.Logout()` (revoke RT) BEFORE clearing the keyring; a revoke failure must not re-block or change the outcome (research D6, INV-2).
- [X] T017 [US2] In `Controller.Logout()` (`internal/client/panel/panel.go`), add the `LogoutNeedSignIn` branch: when the (post-lazy-refresh) delete still surfaces `apiclient.ErrSessionExpired`/`ErrRefreshFailed`, return `(LogoutNeedSignIn, nil)` with no local mutation (research D7).
- [X] T018 [US2] Wire the GUI `LogoutNeedSignIn` branch in `internal/client/ui/panel.go` (`//go:build gui`): reuse the existing `promptSignIn()` form dialog (`panel.go:556`), but parameterize its success continuation — instead of the default `refresh()`+`pollLoop()`, on a successful `SignIn` re-invoke `p.ctrl.Logout()` (retry the removal). Factor `promptSignIn` to take an `onSuccess func()` (default = refresh+poll) so both the `start()` caller and the logout-retry caller share one dialog. On cancel, abort the logout with no local change.
- [X] T019 [US2] Preserve the reachable-but-non-network warning path: a reachable non-auth error (5xx / changed cert) must NOT block — keep the existing "remote may still be registered" info message (`panel.logoutRemoteLinger`) on a `LogoutDone`-with-warning, per FR-010 / research D1. Adjust the `confirmLogout` branch in `internal/client/ui/panel.go` accordingly.
- [X] T020 [US2] Run US2 tests green: `unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/panel/...'`.

**Checkpoint**: Reachable logout is residue-free (no node, no live RT, no local secrets); already-absent and expired-session handled.

---

## Phase 5: User Story 3 - Force-logout escape hatch (Priority: P2)

**Goal**: From the blocked prompt, "Force log out anyway" performs the full local
teardown and returns to the wizard, accepting a server-side orphan.

**Independent Test**: `ForceLogout()` against an unreachable server clears all
local material (keyring + state), best-effort revokes the RT, and the GUI returns
to the wizard.

### Tests for User Story 3 (write first; MUST fail before impl) ⚠️

- [X] T021 [US3] Add `TestForceLogoutClearsLocal` in `internal/client/panel/panel_test.go`: with the `api` boundary unreachable, `ForceLogout()` clears all three keyring entries + state and returns nil (or joined local error only on a local-delete failure); `firewall.Clear` IS called (INV-5). Verify it FAILS first.

### Implementation for User Story 3

- [X] T022 [US3] Implement `Controller.ForceLogout() error` in `internal/client/panel/panel.go`: unconditional full teardown — best-effort `_ = c.api.Logout()`, `_ = c.fw.Clear()`, delete keyring session+RT+device key, `state.Clear()`, return the joined local error (this is the old 017 always-clear behavior, now reachable only via force).
- [X] T023 [US3] Wire the "Force log out anyway" button in the blocked dialog in `internal/client/ui/panel.go` (`//go:build gui`) to call `p.tn.Disconnect()` then `p.ctrl.ForceLogout()` then `p.restart()`.

**Checkpoint**: User is never trapped signed-in; forced logout returns to wizard (orphan accepted).

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T024 [P] Amend `DESIGN.md` §11 / onboarding logout description: from "logout works offline, only warns about possible residue" to "logout blocks on network-unreachable to avoid residue, with an explicit force-logout escape hatch that accepts a server-side orphan" (plan.md "DESIGN.md amendment"). Same PR; no constitution version bump.
- [X] T025 Run the full suite + vet + fmt clean: `gofmt -l internal/client`, `go vet ./...`, `unshare -rUn bash -c 'ip link set lo up && go test ./...'`.
- [X] T026 Execute quickstart Part A end-to-end and confirm every listed case passes (`specs/025-client-logout-hardening/quickstart.md`).
- [ ] T027 Manually verify quickstart Part B (GUI two-button blocked prompt + Force path, zh-Hans + en) on the Mesa-OpenGL Windows VM; record the result in the PR description.
- [ ] T028 Check off slice 025 in `docs/ROADMAP.md` (done in the merge commit per constitution Development Workflow).

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup; **blocks all stories** (types, `sleep` seam, i18n keys).
- **US1 (Phase 3)**: depends on Foundational. MVP. Establishes the retry loop + block branch + basic done path that US2 extends.
- **US2 (Phase 4)**: depends on Foundational; builds on the US1 `Logout()` body (RT-revoke + need-sign-in branches). Independently testable.
- **US3 (Phase 5)**: depends on Foundational; `ForceLogout` is independent of US1/US2 logic but its GUI button lives in the US1 blocked dialog.
- **Polish (Phase 6)**: after the desired stories are complete.

### Story-completion order

P1 first: US1 → US2 (both P1), then US3 (P2). US1 before US2 because they edit the
same `Logout()` body and US2 extends US1's done path.

### Within each story

- Tests written and failing before implementation (Principle II).
- Controller (`panel.go`) before GUI (`ui/panel.go`).

### Parallel Opportunities

- T004 (i18n JSON) is `[P]` vs the `panel.go` work — different files.
- T024 (DESIGN.md) is `[P]` vs code.
- Most `panel.go` / `panel_test.go` / `ui/panel.go` tasks are **sequential** (same files), so few `[P]` within a story by design.

---

## Implementation Strategy

### MVP First (US1 only)

1. Phase 1 Setup → Phase 2 Foundational → Phase 3 US1.
2. **STOP and VALIDATE**: unreachable logout blocks with zero local change; reachable logout completes. Demoable MVP.

### Incremental Delivery

US1 (block + bounded retry) → US2 (clean removal + RT revoke + edge cases) → US3
(force escape hatch + GUI) → Polish (DESIGN.md, ROADMAP, quickstart, GUI manual).

---

## Notes

- The slice is client-only: `DELETE /api/v1/nodes/{id}` (017) and `POST /api/v1/logout` (024) already exist — no server/schema tasks.
- Blocking trigger is EXACTLY `errors.Is(err, apiclient.ErrUnreachable)`; 4xx/5xx/cert errors never block (research D1).
- No wall-clock sleeps in tests — drive timing via the injected `sleep` seam (research D3, Constitution II).
- Commit per logical group; stage explicit files only (never `git add .` — repo-root `TODO.md` stays uncommitted).
