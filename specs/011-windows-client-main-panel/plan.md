# Implementation Plan: Windows Client Main Panel

**Branch**: `011-windows-client-main-panel` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/011-windows-client-main-panel/spec.md`

## Summary

Build the full management panel over the already-existing server operations. A
**Fyne-free panel controller** assembles the display data (this user's devices — marking
this machine — and the zones the device is in, with members) and performs every action
(create/join/leave a zone; on owned zones change password, kick, delete), using an
authenticated session. The session token is cached in the OS secure store and reused while
valid; an expired/absent session prompts a sign-in (no redoing device setup). The controller
is tested against a **real** server; the Fyne panel (tabs, top status with the 010 connect
switch, buttons, confirmations, polling refresh) replaces the placeholder home and is
validated manually on Windows.

## Technical Context

**Language/Version**: Go 1.26 (module `lanweave`, shared with server + client 009/010).

**Primary Dependencies**: existing only — `net/http` + `pkg/protocol` DTOs (reused), the 009
`apiclient` (extended with the zone methods) and `keyring` (session-token storage), the 010
`tunnel` (the connect switch), Fyne for the panel view. No new third-party dependency.

**Storage**: None added server-side. The session token is cached in the OS secure store
(`keyring`); no new local file. No server schema change — every endpoint already exists
(005/006/007). **One minimal additive server DTO change** (resolves analyze M1): the
zone-members response gains each member's `node_id` so the panel can kick a member by id (the
members DTO previously exposed only name/owner/ip).

**Testing**: `go test`. Unit (non-privileged): apiclient zone-method error mapping (httptest
canned responses); panel controller "mark this machine" / view assembly with a fake API.
Integration (privileged, `unshare -rUn`, `RequireNetAdmin`): the real apiclient + panel
controller drive a **real** server (`api.NewRouter` + real store + real `wg.Server` + real
`netfw` over `httptest.NewTLSServer`) — create/join/leave zones, owner change-password/kick/
delete, members transparency, and online status — asserting against the server. Acceptance:
the Fyne panel is validated manually on Windows (documented exception).

**Target Platform**: Windows desktop (the panel); the host-agnostic controller + integration
tests run on the Linux dev host.

**Project Type**: Extends the existing client (009/010) in the same Go module.

**Performance Goals**: UI input → server-reflected ≤ 1 s on a local network (constitution
§IV); online refresh ≤ 30 s (feature 007); sub-second progress feedback on every action.

**Constraints**: Owner controls only on owned zones; destructive actions confirmed; the
session token never written to a plain file or logged; the panel stays usable on any error.

**Scale/Scope**: One user's devices (≤ ~100) and their zones; a few API calls per refresh/
action.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**: Two small additions — `apiclient` gains the zone methods + token
  persistence; a new `internal/client/panel` controller (Fyne-free) assembles views and runs
  operations. The Fyne panel is a thin view over the controller. No premature abstraction;
  errors as values; typed client errors. `gofmt`/`vet` clean on the host-buildable packages.
  **PASS**
- **II. Testing Standards (NON-NEGOTIABLE)**: The server is **not** mocked — the integration
  tier drives a real `api.NewRouter` backed by real SQLite + WireGuard + nftables
  (privileged), exercising create/join/leave/owner/members/online end to end. Unit tests use
  the apiclient's own httptest seam and a fake API for the controller (our own seams, not the
  forbidden SQLite/nftables/WireGuard). Each user story gets coverage (US1 view assembly +
  this-machine marking + online; US2 create/join/leave; US3 owner ops + gating; US4
  session reuse/prompt + error surfacing). **The Fyne panel rendering is validated manually
  on Windows** — the documented GUI exception, recorded in Complexity Tracking; all
  behavior-bearing logic is automated. **PASS with documented exception.**
- **III. User Experience Consistency**: The spec's FRs encode the constitution's rules:
  owner-control gating (FR-008), destructive-action confirmation naming the entity (FR-009),
  immediate progress (FR-010), human-readable errors (FR-011), connection state always
  visible (FR-001), and uniform field rendering — address `100.127.x.y`, device-with-owner,
  local timestamps (FR-015). The controller carries the gating/validation; the view carries
  confirmations/progress. **PASS**
- **IV. Performance Requirements**: Management actions are single API calls; the panel shows
  the result within the ≤ 1 s local-network budget and refreshes online status on the ≤ 30 s
  cadence. No heavy computation. **PASS**
- **Security & Operational Discipline**: The session token is stored only in the OS secure
  store (DESIGN §8), reused while valid, never written to a file or logged. Owner-only
  operations are enforced server-side (006); the panel additionally hides the controls. No
  secret in the panel's displayed data. **PASS**

One documented exception (Fyne panel manual validation) recorded in Complexity Tracking; no
principle diluted.

## Project Structure

### Documentation (this feature)

```text
specs/011-windows-client-main-panel/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── client-zone-api.md   # the zone/node endpoints the panel consumes + DTOs
│   └── panel-controller.md  # the Fyne-free controller's operations + view model
└── checklists/
    └── requirements.md
```

### Source Code (repository root)

```text
internal/client/
├── apiclient/
│   ├── client.go            # + CreateZone/ListZones/JoinZone/LeaveZone/ZoneMembers/ChangeZonePassword/DeleteZone/KickMember/Me; SetToken; zone typed errors
│   └── client_test.go       # + zone error-mapping unit tests (non-priv)
├── keyring/
│   └── store.go             # + SessionTokenName constant (token cached in the vault)
├── panel/                   # NEW Fyne-free management controller
│   ├── panel.go             # Controller: Devices()/Zones()/Members(); CreateZone/JoinZone/LeaveZone/ChangePassword/KickMember/DeleteZone; "this machine" marking; session load/validate
│   ├── panel_test.go        # unit: view assembly + this-machine + session decisions (fake API), non-priv
│   └── panel_integration_test.go  # privileged: real apiclient + controller vs real server
└── ui/
    ├── panel.go             # //go:build gui — the Fyne panel (tabs, top status + connect switch, create/join, owner controls, confirmations, polling); replaces home.go
    └── ...                   # wizard.go unchanged

cmd/lanweave-client/main.go  # //go:build gui — show the panel (with session) instead of the simple home

pkg/protocol/zone.go         # + node_id on ZoneMemberResponse (additive, M1)
internal/server/store/zones.go      # + NodeID on ZoneMember; select n.id in MembersByZone (M1)
internal/server/api/zone_handlers.go # map node_id into the members response (M1)
```

**Structure Decision**: The panel controller + apiclient extension are Fyne-free and tested
on the host against a real server; the Fyne panel + the connect switch are behind the `gui`
tag (manual on Windows). The only server-side change is the additive `node_id` on the
zone-members DTO (M1); otherwise `pkg/protocol` is reused as-is.

### Session handling (reference for tasks)

1. On panel start, load the cached session token from the secure store; construct the
   apiclient with it; validate via a cheap authed call (`GET /me`).
2. If missing/expired (401) → show a sign-in prompt (username + password) → `Login` → cache
   the new token in the secure store → proceed.
3. All management calls use the apiclient's bearer token; a 401 mid-use re-triggers the
   sign-in prompt, then resumes.

## Complexity Tracking

> One documented exception (Fyne panel manual validation under Principle II), recorded per
> the constitution's process. No principle diluted.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| The Fyne panel rendering/interaction is validated manually on Windows rather than by automated tests | A desktop GUI has no headless equivalent on the Linux build host; the panel's management logic and server interactions are fully automated against a real server, leaving only pixels for manual checks | A Windows CI runner is unavailable for v1; driving the GUI in tests would prove little about the rendered panel and would not exercise the real management logic any better than the controller tests already do |
