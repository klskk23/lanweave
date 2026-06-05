# Research: Windows Client Skeleton & First-Run Wizard

**Feature**: 009-windows-client-skeleton | **Date**: 2026-06-05

This is the first client feature, so the open questions are about the desktop stack and
how to keep it testable. Decisions below resolve them; none remain as NEEDS CLARIFICATION.

## Decision 1 — UI-framework-free onboarding core, thin Fyne shell

- **Decision**: Put all behavior-bearing logic in Fyne-free packages
  (`apiclient`, `onboard`, `state`, `keyring`, `wgkey`) and keep `internal/client/ui` +
  `cmd/lanweave-client` as a thin Fyne shell that binds to the `onboard` controller.
- **Rationale**: Fyne needs a GUI/GL toolchain that a headless CI host may lack, and GUI
  rendering can't be asserted headlessly anyway. By isolating the logic, the
  constitution-critical behavior is unit- and integration-tested on the build host
  against a real server, and only pixels remain for manual checks. It also keeps each
  package small and single-purpose (Principle I).
- **Alternatives considered**:
  - *Logic embedded in Fyne callbacks*: couples correctness to the untestable GUI and to
    the GL toolchain. Rejected.
  - *A non-Fyne UI (CLI/web)*: contradicts DESIGN §9.1 (Fyne). Rejected.

## Decision 2 — Fyne for the UI (DESIGN §9.1)

- **Decision**: Use `fyne.io/fyne/v2` for the wizard and placeholder home.
- **Rationale**: Pure-Go desktop UI mandated by DESIGN; single-binary friendly; has a
  headless `test` package for light widget-wiring smoke tests.
- **Build-environment note**: Fyne's desktop driver requires a CGO/GL toolchain. The Fyne
  packages therefore build on the Windows target (or a properly equipped cross-compile
  environment), **not necessarily on the headless CI host**. The Fyne-free core builds and
  tests everywhere, so CI coverage of the logic is unaffected. This is captured so tasks
  don't assume `go build ./...` of the UI succeeds on a bare CI box.
- **Alternatives considered**: Walk/other Windows UI libs — not pure Go / not the DESIGN
  choice. Rejected.

## Decision 3 — Secure secret store: interface + Windows Credential Manager (DPAPI), dev backend, test fake

- **Decision**: Define `keyring.Store { Set(name, secret); Get(name); Delete(name) }`.
  The Windows backend uses the Credential Manager (DPAPI-backed) via a build-tagged file
  (`store_windows.go`); a build-tagged non-Windows backend supports dev; unit tests use an
  in-memory fake.
- **Rationale**: DESIGN §9.1 mandates "Windows credential manager (DPAPI 封装)". The OS
  vault is platform-specific and unavailable on the headless host, so an interface lets
  the onboarding logic be tested with a fake (our own seam — *not* one of the
  constitution's forbidden mocks of SQLite/nftables/WireGuard). The real DPAPI binding is
  validated manually on Windows.
- **Alternatives considered**:
  - *Cross-platform keyring lib (e.g. zalando/go-keyring)*: convenient, but on Linux it
    needs the Secret Service (D-Bus), typically absent in CI; the abstraction-plus-fake
    gives the same portability without that dependency in tests. May still be used as the
    Windows backend if simpler than raw cred-manager calls — an implementation detail
    behind the interface.
  - *Encrypt the key into the state file*: violates "private key never in a plain file"
    (FR-005). Rejected.

## Decision 4 — Local state record: JSON at a per-user path, atomic write, no secret

- **Decision**: A `state.Record` (server URL, device name, address, server public key,
  endpoint, network) written as JSON to `%LOCALAPPDATA%\lanweave\state.json` on Windows
  and `os.UserConfigDir()/lanweave/state.json` elsewhere, via temp-file + rename. Presence
  of the record means "already set up" (skip the wizard).
- **Rationale**: Matches DESIGN §9 ("公钥/IP/server pubkey/endpoint 写本地状态文件",
  path `%LOCALAPPDATA%\lanweave\state.json`). Atomic write avoids a torn record on crash.
  Excluding the secret keeps the key solely in the OS vault (FR-005/FR-007).
- **Alternatives considered**: A small local DB — overkill for one record. Rejected.

## Decision 5 — Partial-failure idempotency via the device public key

- **Decision**: Generate the key pair once per wizard session and store the private key in
  the vault **before** registering the device. If `POST /nodes` returns `409 pubkey_taken`
  on a retry, treat it as "our own previous attempt already registered this device" and
  recover the assigned address by matching the chosen device name in `GET /nodes`, then
  continue. A `409 node_name_taken` (a *different* device already uses the name) is a real
  conflict surfaced to the user.
- **Rationale**: A fresh key pair is effectively unique, so `pubkey_taken` during the same
  session can only mean our earlier attempt succeeded server-side while local persistence
  failed. Recovering instead of erroring prevents stranding and avoids creating a
  duplicate device (FR-008, SC-007). The node name is returned by `GET /nodes`, so it is a
  reliable match key (the public key is intentionally never returned by the API).
- **Alternatives considered**:
  - *Always create a brand-new key/name on retry*: orphans the server-side node and
    confuses the user. Rejected.
  - *A server-side "upsert node" endpoint*: new server surface, out of scope; the existing
    409 semantics suffice. Rejected.

## Decision 6 — Cancel/interruption cleanup

- **Decision**: On cancel or unrecoverable failure, delete the stored private key and any
  partially written state record so the next launch starts fresh; a setup is "complete"
  only after both server registration and the state write succeed.
- **Rationale**: FR-008/FR-010/SC-007 — no half-set-up machine; the presence of the state
  record is the single, trustworthy "onboarded" signal.

## Decision 7 — TLS trust and the `--insecure` flag

- **Decision**: Verify the server certificate against the system trust store by default.
  Expose skip-verify only as a `--insecure` command-line flag on the client binary; it is
  never shown in the UI. An untrusted certificate yields a clear, plain-language error.
- **Rationale**: DESIGN §9.3 / §11 accepted risk — prevents users from blindly disabling
  security; troubleshooting still possible from the command line (FR-014/FR-015, SC-006).
- **Testability (resolves analyze M1)**: the client also accepts an **optional trust root**
  (a specific CA/cert pool) so the verify-on path can be tested against a self-signed test
  certificate *without* disabling verification. The integration test (Decision 8) passes
  the `httptest` server's certificate as the trust root, genuinely exercising FR-015's
  default-verification path rather than bypassing it with `--insecure`. Trust root and
  `--insecure` are mutually exclusive; verification stays on unless `--insecure` is set.

## Decision 8 — Testing strategy (three tiers, real server boundary)

- **Decision**:
  - *Unit (non-privileged)*: apiclient typed-error mapping against an `httptest` server
    returning canned status/JSON; `onboard` controller flow/validation/cancel/partial-
    failure with a fake apiclient + fake keyring + temp state; `state` round-trip.
  - *Integration (privileged, `unshare -rUn`)*: real apiclient + real `onboard` against a
    **real** server — `api.NewRouter` wired with a real store + real `wg.Server` + real
    `netfw`, served over `httptest.NewTLSServer`, with the client trusting that server's
    certificate via its `RootCAs` (verify-on, not `--insecure`) — for both the register and
    sign-in paths; assert the server has the device, the state record is written, and the
    vault holds the key. This reuses the same real-kernel harness pattern as the server's
    node tests.
  - *Acceptance/smoke (manual on Windows)*: built client per `quickstart.md` — GUI flow,
    DPAPI vault, and the `%LOCALAPPDATA%` path.
- **Rationale**: Honors Principle II — the server (with its real SQLite/WG/nft) is never
  mocked; only our own apiclient/keyring seams are faked in unit tests. The unavoidable
  GUI/DPAPI manual portion is the documented exception.

## Resolved unknowns summary

| Topic | Resolution |
|-------|------------|
| UI framework | Fyne, confined to a thin shell |
| Testability of GUI | logic is UI-free + tested vs real server; GUI/DPAPI manual on Windows |
| Secret storage | `keyring.Store` interface; Windows DPAPI backend; dev backend; test fake |
| State location | `%LOCALAPPDATA%\lanweave\state.json` (Windows) / `UserConfigDir` (dev), atomic JSON |
| Orphan on partial failure | pubkey-based idempotent retry; name-match via `GET /nodes` |
| TLS skip | `--insecure` CLI flag only, never in UI |
| Build env | Fyne packages need the GL toolchain; core builds headless |
