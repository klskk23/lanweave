# Tasks: Windows Client Skeleton & First-Run Wizard

**Feature**: 009-windows-client-skeleton | **Branch**: `009-windows-client-skeleton`
**Input**: Design documents in `/specs/009-windows-client-skeleton/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED per constitution Principle II. The server boundary is **not** mocked —
the integration tier drives a real `api.NewRouter` (real SQLite + WireGuard + nftables,
privileged). Only our own seams (apiclient/keyring) are faked in unit tests. GUI rendering
and the Windows DPAPI vault are validated manually on Windows (documented exception).

**Build isolation**: every Fyne-importing file carries `//go:build gui`, and each such
package (`internal/client/ui`, `cmd/lanweave-client`) gets an untagged `doc.go` stub so
the default headless `go build ./...` / `go test ./...` stays green (the server build is
never broken by the GUI toolchain). The GUI is built/validated with `-tags gui` on the
Windows/desktop toolchain.

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`

---

## Phase 1: Setup

- [X] T001 Add client dependencies to `go.mod` (`fyne.io/fyne/v2` for the UI shell; a Windows Credential Manager backend lib if used) and run `go mod tidy`. Confirm the Fyne-free core still builds headless. (UI packages compile only with `-tags gui` on the GL toolchain.)

**Checkpoint**: dependencies resolved; module still builds headless.

---

## Phase 2: Foundational (Fyne-free building blocks — block all stories)

- [X] T002 [P] Create `internal/client/wgkey/wgkey.go`: `GenerateKeyPair() (priv, pub string, err error)` using `wgtypes.GeneratePrivateKey()` (base64 strings; private returned for the vault, public for the server)
- [X] T003 [P] Create `internal/client/state/state.go`: `Record` struct (fields per data-model: `schema_version, server_url, node_name, ip, server_public_key, endpoint, network`), `DefaultPath()` (`%LOCALAPPDATA%\lanweave\state.json` on Windows else `os.UserConfigDir()/lanweave/state.json`), and atomic `Save(path, Record)`, `Load(path) (Record, error)`, `Exists(path) bool`, `Clear(path) error` (temp-file + rename; user-only dir perms; never stores a secret)
- [X] T004 [P] Create `internal/client/keyring/`: `store.go` (`Store` interface `Set(name string, secret []byte) error` / `Get(name) ([]byte, error)` / `Delete(name) error`, plus `Open() (Store, error)` selecting a backend), `fake.go` (in-memory `Store` test double), `store_windows.go` (`//go:build windows` Credential Manager / DPAPI backend), `store_other.go` (`//go:build !windows` dev backend under the user config dir with user-only perms)
- [X] T005 [P] Create `internal/client/apiclient/client.go`: `Client` over HTTPS (base URL, `insecure bool`, an **optional trust root** `RootCAs *x509.CertPool` so a specific server certificate can be trusted without disabling verification — this is what lets the integration test exercise the verify-on path against a test cert, M1; in-memory bearer token) with `Register(invite, user, pass)`, `Login(user, pass) (token)`, `RegisterNode(name, pubKey) (protocol.NodeResponse, error)`, `ListNodes() (protocol.NodeListResponse, error)`, `ServerInfo() (protocol.ServerInfoResponse, error)`; map status + `protocol.ErrorResponse` to typed errors `ErrUnreachable, ErrUntrustedCert, ErrAuthFailed, ErrInviteInvalid, ErrUsernameTaken, ErrNodeNameTaken, ErrPubKeyTaken, ErrPoolExhausted, ErrServer` (reuse `pkg/protocol` DTOs). The `RootCAs` and `insecure` are mutually exclusive inputs; verification stays ON unless `insecure` is set

**Checkpoint**: the building blocks compile and are unit-testable in isolation.

---

## Phase 3: User Story 1 — 新用户在新机器上完成首次设置 (P1) 🎯 MVP

**Goal**: a fresh run completes onboarding (server → account/sign-in → device name →
device registered, key vaulted, state recorded) and reaches the home placeholder.

**Independent test**: run the onboarding controller end to end against a real server and
confirm the device is registered with an address, the state record holds the server info,
and the vault holds the key — for both the create-account and sign-in paths.

- [X] T006 [US1] Create `internal/client/onboard/onboard.go`: the UI-free controller with the step model (`Server→Auth→DeviceName→Provision→Done`) and `Provision()` happy path composing the blocks — authenticate (Login or Register+Login), `wgkey.GenerateKeyPair`, store the private key via `keyring.Store` **before** registration, `apiclient.RegisterNode`, `apiclient.ServerInfo`, then `state.Save`; expose the resulting `state.Record`
- [X] T007 [US1] Create the Fyne shell (all Fyne files `//go:build gui`, plus untagged `doc.go` stubs): `internal/client/ui/wizard.go` (server/auth/device-name/provision screens bound to the controller), `internal/client/ui/home_placeholder.go` (placeholder home), `cmd/lanweave-client/main.go` (app entry, `--insecure` flag, wires `keyring.Open` + `apiclient` + `onboard` + UI), and `doc.go` stubs in both packages
- [X] T008 [P] [US1] apiclient unit test `internal/client/apiclient/client_test.go` (non-privileged): against `httptest` servers returning canned status/JSON — happy parsing of Login/Register/RegisterNode/ListNodes/ServerInfo and typed-error mapping (401→`ErrAuthFailed`, 409 `node_name_taken`→`ErrNodeNameTaken`, 409 `pubkey_taken`→`ErrPubKeyTaken`, invite/username conflicts, 503→`ErrPoolExhausted`, 5xx→`ErrServer`, transport failure→`ErrUnreachable`)
- [X] T009 [P] [US1] state unit test `internal/client/state/state_test.go` (non-privileged): `Save`/`Load` round-trip, `Exists` true/false, atomic overwrite, `Clear`, and that only non-secret fields are present
- [X] T010 [US1] onboard happy-path unit test `internal/client/onboard/onboard_test.go` (non-privileged): with a fake apiclient + fake keyring + temp state dir, drive `Provision` for the create-account and sign-in modes; assert the private key is in the vault, the device is registered (fake), and the state record holds the address + server public key + endpoint + network
- [X] T011 [US1] Privileged integration test `internal/client/onboard/onboard_integration_test.go` (`testutil.RequireNetAdmin`, `unshare -rUn`): stand up a real server (`api.NewRouter` with real store + real `wg.Server` + real `netfw`) on `httptest.NewTLSServer`; mint an invite; construct the real apiclient with the test server's certificate as its `RootCAs` (verify-on, **not** `--insecure`, so FR-015's default-verification path is genuinely exercised); run the real apiclient + real onboard for the create-account and sign-in paths against it → assert the server has the device with a `100.127.x.y` address, the state record is written, and the vault holds the key

**Checkpoint**: US1 is demonstrable end to end against a real server (MVP).

---

## Phase 4: User Story 2 — 重启后直接进入，不再要求设置 (P2)

**Goal**: a machine that is already set up skips the wizard on relaunch.

**Independent test**: with a state record present, the startup decision is "home"; with it
absent/invalid, it is "wizard".

- [X] T012 [US2] Add a Fyne-free startup decision in `internal/client/onboard/onboard.go` (e.g. `StartupTarget(statePath string) (Target, *state.Record)` returning `Home`+record when present/valid, else `Wizard`); have `cmd/lanweave-client/main.go` route on it (show home placeholder vs wizard)
- [X] T013 [P] [US2] Unit test in `internal/client/onboard/onboard_test.go`: `StartupTarget` returns `Home`+record when a valid record exists, and `Wizard` when the record is absent or unreadable

**Checkpoint**: first run is a one-time event; relaunch lands on home.

---

## Phase 5: User Story 3 — 向导安全、可控、可恢复 (P2)

**Goal**: every step is reversible/cancellable, network actions show progress, failures
are clear and recoverable, the key never leaves the machine, no UI cert-skip, and an
interrupted setup leaves nothing behind.

**Independent test**: drive each failure → recoverable typed error; cancel → no vault key
and no state record; partial failure → idempotent recovery with no duplicate device.

- [X] T014 [US3] Extend `internal/client/onboard/onboard.go`: surface apiclient typed errors as step-recoverable errors; `Cancel()`/cleanup that deletes the vault key and clears any partial state; and the pubkey-idempotent partial-failure recovery (`ErrPubKeyTaken` → `apiclient.ListNodes` → match the chosen name → continue with that address; `ErrNodeNameTaken` → return to the DeviceName step)
- [X] T015 [US3] Extend `internal/client/ui/wizard.go` (`//go:build gui`): progress indicator shown around each network action, Back and Cancel controls on every step, keyboard Enter (confirm) / Escape (cancel/back), human-readable error messages, and **no** certificate-skip control anywhere in the UI
- [X] T016 [P] [US3] onboard robustness tests in `internal/client/onboard/onboard_test.go`: each failure (auth/invite/username/name-taken/unreachable) yields the mapped recoverable error; `Cancel` leaves neither a vault key nor a state record; a partial failure (device registered but the state write fails) is recovered on retry via the pubkey path with exactly one device (no duplicate)

**Checkpoint**: the core flow is safe, recoverable, and leaves no half-set-up machine.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T017 [P] Run `gofmt -w` and `go vet` on the Fyne-free core; `go build ./internal/client/wgkey/... ./internal/client/state/... ./internal/client/keyring/... ./internal/client/apiclient/... ./internal/client/onboard/...` (headless); confirm `go build ./...` (whole module, no `gui` tag) still succeeds
- [X] T018 [P] Run `go test ./internal/client/...` (non-privileged) and `unshare -rUn go test ./internal/client/onboard/... -run Integration`; confirm the core (apiclient/onboard/state/keyring/wgkey) reaches ≥ 70% line coverage, noting GUI/DPAPI as the manual-on-Windows exception
- [X] T019 Validate quickstart Scenarios A–D automatically (A/B privileged, C/D non-privileged); record Scenario E (Windows GUI + DPAPI + `%LOCALAPPDATA%` path) as the manual target-OS check; optionally build the GUI with `-tags gui` if a toolchain is available

---

## Dependencies & Execution Order

- **Setup (T001)** blocks everything.
- **Foundational (T002–T005)**: independent packages → all `[P]`. Block all stories.
- **US1 (T006–T011)**: T006 needs T002–T005; T007 (UI) needs T006; T008 needs T005; T009 needs T003; T010 needs T006; T011 needs T006 + the real-server harness. T008/T009 run alongside T006/T007.
- **US2 (T012–T013)**: T012 needs T003 + T006; T013 needs T012. Independent of US3.
- **US3 (T014–T016)**: T014 extends T006 (same file, sequential); T015 extends T007 (same file); T016 needs T014. Depends on US1.
- **Polish (T017–T019)**: after all implementation/tests.

### File coordination (sequential within a file)

- `internal/client/onboard/onboard.go`: T006 → T012 → T014.
- `internal/client/onboard/onboard_test.go`: T010 → T013 → T016.
- `internal/client/ui/wizard.go`: T007 → T015.
- `cmd/lanweave-client/main.go`: T007 → T012.

## Parallel Execution Examples

- **Foundational**: T002 (`wgkey`) ∥ T003 (`state`) ∥ T004 (`keyring`) ∥ T005 (`apiclient`) — four independent packages.
- **US1 tests**: T008 (`apiclient/client_test.go`) ∥ T009 (`state/state_test.go`).
- **Polish**: T017 (lint/build) ∥ T018 (test/coverage).

## Implementation Strategy

**MVP** = Setup + Foundational + US1 (T001–T011): a fresh run completes onboarding against
a real server — device registered, key vaulted, state recorded — verified end to end
(privileged). US2 makes it a one-time event; US3 hardens the flow (errors, cancel,
idempotent recovery) and wires the constitutional wizard UX. The Fyne-free core is fully
automated; the GUI shell and Windows DPAPI vault are validated manually on Windows.
