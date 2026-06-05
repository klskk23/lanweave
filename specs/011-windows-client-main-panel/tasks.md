# Tasks: Windows Client Main Panel

**Feature**: 011-windows-client-main-panel | **Branch**: `011-windows-client-main-panel`
**Input**: Design documents in `/specs/011-windows-client-main-panel/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED per constitution Principle II. The server is **not** mocked — the
integration tier drives a real `api.NewRouter` (real SQLite + WireGuard + nftables,
privileged) for create/join/leave/owner/members/online. Only our own seams (apiclient over
`httptest`, a fake API for the controller) are faked in unit tests. The Fyne panel rendering
is validated manually on Windows (documented exception).

**One minimal additive server change** (resolves analyze M1): the zone-members response must
include each member's `node_id` so the panel can remove (kick) a member — the members DTO
currently exposes only name/owner/ip. This is the only server-side change in 011.

**Build isolation** (as in 009/010): the apiclient + `panel` controller are Fyne-free and
build/test headless; the Fyne panel (`ui/panel.go`) is `//go:build gui`. The default headless
`go build ./...` / `go test ./...` stay green.

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`

---

## Phase 1: Setup

- [X] T001 Add `SessionTokenName` constant (e.g. `"lanweave-session-token"`) to `internal/client/keyring/store.go` for caching the session token in the OS secure store

---

## Phase 2: Foundational (block all stories)

- [X] T002 [Server: members carry node_id] Add `NodeID int64` (`json:"node_id"`) to `protocol.ZoneMemberResponse` in `pkg/protocol/zone.go`; add `NodeID int64` to `store.ZoneMember` and select `n.id` in `MembersByZone` (`internal/server/store/zones.go`); map it in the `zoneMembers` handler (`internal/server/api/zone_handlers.go`); update the affected server tests (the members-transparency store test in `internal/server/store/zones_test.go` and any zone-members handler test) to assert the node_id is present. This is the minimal additive change that lets the panel kick a member by id
- [X] T003 [P] Extend `internal/client/apiclient/client.go`: add `Me() (protocol.MeResponse, error)`, `CreateZone(name, password) (protocol.ZoneResponse, error)`, `ListZones() (protocol.ZoneListResponse, error)`, `JoinZone(name string, nodeID int64, password string) error`, `LeaveZone(name string, nodeID int64) error`, `ZoneMembers(name) (protocol.ZoneMembersResponse, error)`, `ChangeZonePassword(name, password) error`, `DeleteZone(name) error`, `KickMember(name string, nodeID int64) error`, and `SetToken(string)`; map server codes to typed errors `ErrSessionExpired` (401 on authed calls), `ErrZoneNameTaken` (409 `zone_name_taken`), `ErrZoneOrPassword` (403 `invalid_zone_or_password`), `ErrNotOwner` (403 `forbidden`), `ErrNotMember`/`ErrZoneNotFound` (404 `not_found`)
- [X] T004 Create `internal/client/panel/panel.go`: the Fyne-free `Controller` with `New(api, record state.Record, keys keyring.Store)`, and the session flow `LoadSession() (needSignIn bool, err error)` (read cached token → `SetToken` → validate via `Me`; missing/expired → needSignIn) and `SignIn(username, password string) error` (`Login` → cache token in the vault). Define an in-package `api` interface (the subset of apiclient methods) so unit tests can supply a fake; `*apiclient.Client` satisfies it

**Checkpoint**: members carry a node_id; the client can authenticate and is ready to call zone/node endpoints.

---

## Phase 3: User Story 1 — 一眼看清我的网络 (P1) 🎯 MVP

**Goal**: the controller assembles this user's devices (marking this machine, with online +
last-seen), the zones the device is in, and each zone's members (each with its node_id).

**Independent test**: against a real server, `Devices()`/`Zones()`/`Members()` match the
server; exactly one device is this machine; online state is reflected.

- [X] T005 [US1] Add read/view assembly to `internal/client/panel/panel.go`: `Devices() ([]DeviceView, error)` (from `ListNodes`; mark `IsThisMachine` by matching `record.NodeName`/`record.IP`; carry IP, online, last-seen), `Zones() ([]ZoneView, error)` (from `ListZones`, carrying `IsOwner`), `Members(zoneName string) ([]MemberView, error)` (from `ZoneMembers`, mapping node_id/name/owner/ip)
- [X] T006 [P] [US1] Unit test `internal/client/panel/panel_test.go` (non-privileged): with a fake API, `Devices()` marks exactly the matching device as this machine and carries online/last-seen; `Zones()` carries `IsOwner`; `Members()` returns every member's node_id/name/owner/ip
- [X] T007 [US1] Privileged integration test `internal/client/panel/panel_integration_test.go` (`testutil.RequireNetAdmin`, `unshare -rUn`): stand up a real server (`api.NewRouter` + real store/wg/nft over `httptest.NewTLSServer`, trusting the cert via `RootCAs`); create a user, register two devices, sign in; assert `Devices()` lists both with this machine marked and online state, `Zones()` and `Members()` match the server

**Checkpoint**: US1 is demonstrable — the panel's view matches the server (MVP).

---

## Phase 4: User Story 2 — 创建与加入隔离区 (P1)

**Goal**: create a zone, join another's zone, leave a zone — reflected in the views.

**Independent test**: create → appears as owner; another user joins → member appears; leave →
gone; wrong password / duplicate name → typed errors.

- [X] T008 [US2] Add operations to `internal/client/panel/panel.go`: `CreateZone(name, password) error`, `JoinZone(name, password) error`, `LeaveZone(name) error`. Join/leave use **this machine's** device id, resolved by matching `record.NodeName` against `ListNodes()` (the setup record has no node id); each returns only after the server confirms
- [X] T009 [P] [US2] Extend `internal/client/apiclient/client_test.go` (non-privileged): zone error mapping vs `httptest` canned responses — create duplicate → `ErrZoneNameTaken`, join wrong password (`403 invalid_zone_or_password`) → `ErrZoneOrPassword`, authed `401` → `ErrSessionExpired`
- [X] T010 [US2] Extend `internal/client/panel/panel_integration_test.go`: user A `CreateZone` → appears in `Zones()` as owner; user B `JoinZone` by name+password → B's device shows in A's `Members()` (transparency, with node_id); `LeaveZone` → membership gone; wrong password → `ErrZoneOrPassword`; duplicate name → `ErrZoneNameTaken`

**Checkpoint**: zone membership works end to end through the controller.

---

## Phase 5: User Story 3 — 隔离区拥有者管理 (P2)

**Goal**: owner change-password / kick / delete, gated to owners.

**Independent test**: owner changes password (old password no longer admits a new device),
kicks a member by node_id, deletes the zone; a non-owner attempt → `ErrNotOwner`.

- [X] T011 [US3] Add owner operations to `internal/client/panel/panel.go`: `ChangePassword(name, password) error`, `KickMember(name string, nodeID int64) error` (the node_id comes from a `MemberView`, now that members carry it — T002), `DeleteZone(name) error` (all surface `ErrNotOwner` when the server refuses)
- [X] T012 [US3] Extend `internal/client/panel/panel_integration_test.go`: as owner, `ChangePassword` (a new device can no longer join with the old password, can with the new), `KickMember` by the member's node_id (the member leaves `Members()`), `DeleteZone` (the zone disappears); a non-owner calling an owner op → `ErrNotOwner`

**Checkpoint**: owner controls work and are enforced; non-owners are refused.

---

## Phase 6: User Story 4 — 会话与一致的体验 (P2)

**Goal**: reuse a valid cached session, prompt sign-in only when needed; the Fyne panel
renders everything with confirmations, progress, and polling.

**Independent test**: valid cached token → no sign-in; absent/expired → sign-in needed, then
`SignIn` caches a token; a 401 mid-use surfaces `ErrSessionExpired`.

- [X] T013 [P] [US4] Unit test `internal/client/panel/panel_test.go`: `LoadSession` returns needSignIn=false for a valid cached token (fake `Me` ok) and true when the token is absent or `Me` returns `ErrSessionExpired`; `SignIn` caches the token in the fake keyring; an authed call after expiry surfaces `ErrSessionExpired`
- [X] T014 [US4] Build the Fyne panel `internal/client/ui/panel.go` (`//go:build gui`): top status (IP + the 010 connect/disconnect switch + last-seen), a "My nodes" tab (devices, this machine marked, online), a "My zones" tab (zones with members on expand), Create/Join buttons, owner-only change-password/kick/delete shown only when `IsOwner` (kick uses the member's node_id), destructive-action confirmations naming the entity, progress feedback, a periodic refresh (~30 s) and refresh-after-action, and a sign-in dialog when `LoadSession` reports needSignIn; wire it in `cmd/lanweave-client/main.go` (`//go:build gui`) replacing the 010 `home.go` view

**Checkpoint**: the session is reused/prompted correctly and the panel exposes every operation.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T015 [P] Run `gofmt -w`, `go vet`, and `staticcheck` on the changed packages (client + the touched server files); confirm `go build ./...` (headless) succeeds and `go build -tags gui ./cmd/lanweave-client/` builds where the toolchain is available
- [X] T016 [P] Run `go test ./internal/client/apiclient/... ./internal/client/panel/... ./internal/server/store/... ./internal/server/api/...` (non-privileged where possible) and `unshare -rUn go test ./internal/client/panel/... -run Integration ./internal/server/...` (privileged); confirm the new code (apiclient zone methods + `panel` controller + the members-node_id change) reaches ≥ 70% coverage, noting the Fyne panel as the manual-on-Windows exception
- [X] T017 Validate quickstart Scenarios A–D automatically (A/B/C privileged, D non-privileged); record Scenario E (the Windows panel: tabs, create/join/leave, owner ops with confirmations, transparency) as the manual target-OS check

---

## Dependencies & Execution Order

- **Setup (T001)** blocks the session work.
- **Foundational (T002–T004)**: T002 (server members node_id) and T003 (apiclient) are
  independent; T004 (controller skeleton + session) needs T003 + T001. The members-node_id
  change (T002) is needed before the panel's `Members`/kick (T005/T011). Block all stories.
- **US1 (T005–T007)**: T005 (read methods) after T002 (node_id) + T004; T006 after T005; T007
  after T005 + session (T004).
- **US2 (T008–T010)**: T008 after T005; T009 after T003 ([P]); T010 after T008 + T007.
- **US3 (T011–T012)**: T011 after T008 (+ T002 node_id for kick); T012 after T011 + T010.
- **US4 (T013–T014)**: T013 after T004; T014 after T011 (needs all controller ops).
- **Polish (T015–T017)**: after all implementation/tests.

### File coordination (sequential within a file)

- `internal/client/panel/panel.go`: T004 → T005 → T008 → T011.
- `internal/client/panel/panel_test.go`: T006 → T013.
- `internal/client/panel/panel_integration_test.go`: T007 → T010 → T012.
- `internal/client/apiclient/client.go`: T003. `client_test.go`: T009.
- `internal/client/ui/panel.go` + `cmd/lanweave-client/main.go`: T014.
- Server (T002): `pkg/protocol/zone.go`, `internal/server/store/zones.go`, `internal/server/api/zone_handlers.go`, + their tests.

## Parallel Execution Examples

- **Foundational**: T002 (server members node_id) ∥ T003 (`apiclient/client.go`).
- **Tests**: T006 (`panel_test.go`) ∥ T009 (`apiclient/client_test.go`).
- **Polish**: T015 (lint/build) ∥ T016 (test/coverage).

## Implementation Strategy

**MVP** = Setup + Foundational + US1 (T001–T007): the members DTO carries node_id, the panel
controller authenticates and shows the user's devices (this machine marked) and zones/members
from a real server, proven end to end. US2 adds create/join/leave, US3 the owner controls
(kick by node_id), US4 the session reuse/prompt and the Fyne panel. The controller + apiclient
+ the server change are fully automated; the Fyne panel is validated manually on Windows.
