# Implementation Plan: Windows Client Skeleton & First-Run Wizard

**Branch**: `009-windows-client-skeleton` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/009-windows-client-skeleton/spec.md`

## Summary

Stand up the first end-user app: a desktop client whose first run walks the user through
server address → sign in / create account → name this device, then generates the device
key locally, registers the device with the server (uploading only the public key), stores
the private key in the OS secure store, and writes a non-secret local record so later
launches skip setup. The design splits a **UI-framework-free onboarding core** (REST
client, onboarding controller, state file, secret store, key generation) — fully tested
headlessly against a **real** server — from a thin Fyne UI shell (wizard screens +
placeholder home) validated on the target OS. The tunnel (010) and full panel (011) are
out of scope.

## Technical Context

**Language/Version**: Go 1.26 (module `lanweave`, shared with the server).

**Primary Dependencies**:
- UI: `fyne.io/fyne/v2` (DESIGN §9.1) — confined to the UI shell + `cmd/lanweave-client`.
- HTTP: standard `net/http` + `encoding/json`, reusing `pkg/protocol` DTOs.
- Key generation: existing `golang.zx2c4.com/wireguard/wgctrl/wgtypes` (device key pair).
- Secure store: a `keyring.Store` interface with a Windows Credential Manager backend
  (DPAPI-backed) and a non-Windows dev backend; tests use an in-memory fake.

**Storage**: A local JSON state record (no secret) at a per-user path
(`%LOCALAPPDATA%\lanweave\state.json` on Windows; `os.UserConfigDir()` elsewhere for
dev). The private key lives only in the OS secure store. No server schema change.

**Testing**: `go test`. Unit (non-privileged): apiclient typed-error mapping against an
`httptest` server with canned responses; onboarding-controller flow/validation/cancel/
partial-failure with a fake apiclient + fake keyring + temp state dir; state-file
read/write. Integration (privileged, `unshare -rUn`, `RequireNetAdmin`): the real
apiclient + real onboarding controller drive a **real** server (`api.NewRouter` with real
store + real `wg.Server` + real `netfw` over `httptest.NewTLSServer`) end to end for both
the register and sign-in paths. Acceptance/smoke: the built client against `quickstart.md`
— GUI rendering and the Windows DPAPI store are validated manually on Windows.

**Target Platform**: Windows desktop (the only end-user surface in v1). The onboarding
core is OS-neutral so it builds and tests on the Linux dev host; the Fyne shell + DPAPI
backend build on the Windows toolchain (or cross-compile target).

**Project Type**: Adds a client to the existing single Go module (server + shared
protocol already present).

**Performance Goals**: Sub-second visible feedback on every network action (FR-011);
first-run completion under 2 minutes (SC-001); local UI→server-reflected ≤ 1 s
(constitution §IV) — onboarding is a few sequential API calls.

**Constraints**: One machine = one device. Private key never leaves the machine. No UI
control to weaken TLS verification (advanced CLI flag only). Cancelled/interrupted setup
leaves no secret and no record.

**Scale/Scope**: Single user per machine; a handful of API calls per onboarding.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**: Small, single-responsibility client packages (`apiclient`,
  `onboard`, `state`, `keyring`, `wgkey`, `ui`). The onboarding logic is UI-free, so it
  is obvious and testable in isolation; the Fyne shell is thin. No premature abstraction
  beyond the `keyring.Store` and apiclient seams needed for cross-OS + testing. Errors as
  values; typed client errors. `gofmt`/`vet` clean on the headless-buildable packages.
  **PASS**
- **II. Testing Standards (NON-NEGOTIABLE)**: The client crosses a **process** boundary
  (the server), so three tiers apply. The server is **not** mocked — the integration tier
  drives a real `api.NewRouter` backed by real SQLite + real WireGuard + real nftables
  (privileged), so the client's onboarding is exercised against the genuine boundary. The
  apiclient/keyring fakes used in unit tests are our own seams, not the forbidden
  SQLite/nftables/WireGuard. Each user story has acceptance coverage (US1 end-to-end
  register+login; US2 skip-on-relaunch via persisted state; US3 failure/cancel/partial
  recovery). **GUI rendering and the Windows DPAPI store are validated manually on the
  target OS** — a documented, unavoidable exception for a desktop GUI (consistent with
  prior "needs a real client/handshake" manual scenarios); all behavior-bearing logic is
  automated. **PASS with documented GUI-test exception** (recorded in Complexity Tracking).
- **III. User Experience Consistency**: This feature *is* the UX surface. The spec's
  FR-010..FR-015 encode the constitution's first-run wizard rules directly: back/cancel at
  every step, immediate progress on long actions, human-readable actionable errors,
  keyboard navigation (Enter/Escape), no UI certificate-skip, uniform field rendering
  (the address `100.127.x.y`, the device name). The plan keeps these in the controller +
  view contract. **PASS**
- **IV. Performance Requirements**: Onboarding is a few sequential API calls; sub-second
  feedback is a UI requirement (progress shown before each call), and completion is well
  under the 2-minute target. No heavy computation. **PASS**
- **Security & Operational Discipline**: The private key is generated locally and stored
  only in the OS secure store — never in a file or log. The session token is held in
  memory (and/or the secure store), never logged. TLS verification is on by default
  (system trust store); the skip-verify override is CLI-only and never in the UI
  (DESIGN §11 accepted risk). No secret appears in the state file or logs. **PASS**

One documented exception (GUI/secure-store manual validation) recorded in Complexity
Tracking; no principle is diluted.

## Project Structure

### Documentation (this feature)

```text
specs/009-windows-client-skeleton/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   ├── client-api-usage.md   # which server endpoints the client calls, and how
│   └── state-file.md         # the local state.json schema (no secret)
└── checklists/
    └── requirements.md  # (from /speckit-specify)
```

### Source Code (repository root)

```text
cmd/
└── lanweave-client/         # NEW: Fyne app entry; --insecure flag; wires core + UI
    └── main.go

internal/client/             # NEW client packages
├── apiclient/               # REST client over HTTPS; JWT; typed errors; reuses pkg/protocol
│   ├── client.go
│   └── client_test.go       # unit: error mapping vs httptest canned responses (non-priv)
├── wgkey/                   # device key-pair generation (wraps wgtypes)
│   └── wgkey.go
├── keyring/                 # secure secret store
│   ├── store.go             # Store interface + selection
│   ├── store_windows.go     # Windows Credential Manager (DPAPI) backend (build-tagged)
│   ├── store_other.go       # non-Windows dev backend (build-tagged)
│   └── fake.go              # in-memory test double (our own seam)
├── state/                   # local non-secret record (state.json)
│   ├── state.go             # Record type, DefaultPath, atomic Load/Save/Clear
│   └── state_test.go        # unit: round-trip, path, atomic write, absence (non-priv)
├── onboard/                 # the UI-free onboarding controller (the testable core)
│   ├── onboard.go           # step model, validation, Register/Login → RegisterDevice → persist; cancel cleanup; partial-failure recovery
│   ├── onboard_test.go      # unit: flow/validation/cancel/partial-failure (fake apiclient+keyring, temp state)
│   └── onboard_integration_test.go  # privileged: real server (api.NewRouter+real wg/nft) over httptest TLS
└── ui/                      # NEW Fyne shell (imports fyne; built on target toolchain)
    ├── wizard.go            # wizard screens bound to the onboard controller
    └── home_placeholder.go  # placeholder home area (full panel is 011)

pkg/protocol/                # unchanged — shared DTOs reused by apiclient
```

**Structure Decision**: A clean split — `internal/client/{apiclient,wgkey,keyring,state,
onboard}` are **Fyne-free** and build/test on the headless host against a real server;
`internal/client/ui` + `cmd/lanweave-client` carry the Fyne dependency and are built on
the desktop toolchain. No server code changes; `pkg/protocol` is reused as-is.

### Onboarding sequence (reference for tasks)

1. Collect server URL → build an apiclient (TLS verify on; `--insecure` only via CLI).
2. Authenticate: sign in (`POST /login`) or create account (`POST /register`) → session token.
3. Name the device → generate a key pair → **store the private key in the secure store first**.
4. `POST /nodes` with the public key:
   - `201` → assigned address.
   - `409 pubkey_taken` (our own prior attempt this session) → recover the address by
     matching the chosen name in `GET /nodes` (idempotent retry, no duplicate — FR-008).
   - `409 node_name_taken` (different device) → ask for a new name (FR-004).
5. `GET /server` → server public key, endpoint, network.
6. Write the local state record (atomic) → setup complete → home placeholder.
7. On cancel/failure: delete the stored private key and any partial record (FR-010, SC-007).

## Complexity Tracking

> One documented exception (GUI/secure-store manual validation under Principle II);
> recorded per the constitution's process. No principle diluted.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| GUI rendering and the Windows DPAPI secure-store binding are validated manually on the target OS rather than by automated tests | A desktop GUI and an OS-specific secret vault cannot be exercised headlessly on the Linux build host; the behavior-bearing onboarding logic is fully automated against a real server, leaving only pixels and the OS vault for manual checks | Forcing automated GUI/DPAPI tests would require a Windows CI runner not available for v1; mocking the secure store would violate the spirit of testing real boundaries and prove nothing about DPAPI |
