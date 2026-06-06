# Implementation Plan: Client Firewall Control and TOFU Certificate Pinning

**Branch**: `018-client-firewall-and-tofu-pin` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/018-client-firewall-and-tofu-pin/spec.md`

## Summary

Two strongly-related client changes that share one client-state schema migration.

**TOFU certificate pinning** (P1, US1): replace feature 017's session-level insecure opt-in with
trust-on-first-use. When a server's certificate fails system verification and no pin is recorded,
the client shows a first-trust prompt displaying the leaf certificate's SHA-256 fingerprint;
accepting persists that fingerprint in `state.json`. Verification thereafter passes if the leaf
fingerprint equals the pin **or** the certificate passes system verification, so later connects —
including after restart — are silent, while any *other* unverifiable certificate is still rejected.
A pinned server presenting a different, still-unverifiable certificate raises a heavier
"certificate changed" warning (`ErrCertChanged`) that blocks the connection until the user
explicitly accepts, which overwrites the pin. The `--insecure` CLI flag is retained as an
escape hatch; the per-session "continue insecurely, not remembered" UI path from 017 is removed.

**Client firewall control** (P2, US2): a footer toggle (default OFF, persisted) that, while ON
**and** the tunnel is connected, installs a named Windows Defender inbound-allow rule
(`lanweave-vpn-inbound`, `remoteip=100.127.0.0/16`, all ports + ICMP, `profile=any`) so VPN peers
can reach this device. The rule exists exactly when (preference ON ∧ Connected): added on
connect-while-on or toggle-on-while-connected, removed on disconnect / toggle-off / logout / exit.
It is named and applied idempotently (delete-then-add), and a startup sweep clears any orphan left
by an unclean shutdown. An inline warning beside the toggle states that enabling it exposes all
local services to same-subnet peers; no confirmation dialog (per spec). Enforcement is Windows-only
(no-op elsewhere); the preference is still persisted everywhere.

Both new fields land in one `state.Record` schema bump (1 → 2). DESIGN.md §275–§277 / §362 / §365
are amended in the same PR (TOFU posture supersedes 017's reactive session opt-in) and a client
host-inbound-rule clause + §11 risk row are added.

## Technical Context

**Language/Version**: Go 1.26.2 (module `lanweave`)

**Primary Dependencies**: client GUI Fyne v2.7.4 (behind `//go:build gui`); standard library
`crypto/tls`, `crypto/x509`, `crypto/sha256` (already used by `apiclient`); Windows `netsh
advfirewall` via `os/exec` (mirrors the existing `addr_windows.go` adapter calls). No new module
dependency.

**Storage**: No server/SQLite change. Client `state.json` schema advances `SchemaVersion` 1 → 2,
adding two optional fields: `pinned_cert_sha256` (string, empty = unpinned) and
`firewall_allow_vpn` (bool, false = off). Older v1 records load unchanged with both defaulted; the
device private key and session token remain keyring-only (never written to state).

**Testing**: `go test` under the standing `unshare -rUn bash -c 'ip link set lo up && go test
./...'` gate. New automated tests, all headless: (a) **apiclient** — against an `httptest` TLS
server with a self-signed certificate: first-trust path surfaces `CertError` carrying the correct
leaf fingerprint; building with `WithPinnedCert(fp)` verifies that same server with verification
ON; a *different* self-signed certificate under an existing pin yields `ErrCertChanged`; a
system-CA-valid certificate passes regardless of pin (pin-or-CA). (b) **panel.Controller** — with
a fake firewall seam and the existing fake `api`: `SetFirewallAllowed`/`ReconcileFirewall` enforce
"rule present ⟺ preference ON ∧ connected", persist the preference to `state.json`, and clear on
logout; idempotent re-apply. (c) **state** — load a v1 record → v2 in memory with new fields
defaulted; round-trip save/load of the two new fields. The actual `netsh` execution and all Fyne
dialogs/indicators/the toggle are verified manually on Windows under the standing GUI/exec
exception (DESIGN §365; `quickstart.md` matrix).

**Target Platform**: Windows 10/11 desktop client (the only end-user surface; firewall enforcement
is Windows-only) + Linux CI for headless unit/integration tests (firewall enforcement no-op).

**Project Type**: Client/server Go monorepo; this feature touches only the **client** (new
`firewall` package, plus `apiclient`, `state`, `panel`, `ui`, `main`) and a DESIGN.md amendment.
No server code changes.

**Performance Goals**: N/A for new hot paths. TOFU adds one SHA-256 compare per TLS connection
(sub-millisecond). A `netsh` add/remove is a local, sub-second, off-the-hot-path operation tied to
connect/disconnect, well outside the Principle IV API budgets.

**Constraints**: `apiclient` bakes TLS config at `New()` — a pin is supplied at construction
(`WithPinnedCert`) and "re-pin" means **rebuilding** the client, never mutating a live one (same
pattern 017 used for insecure). The pin is the leaf certificate's SHA-256 (captured from the failed
verification, not the chain). The firewall rule must be **named** and **idempotent** (delete-then-
add) with a **startup sweep**, so an abrupt exit can never strand an open inbound rule. Enabling the
rule is a security-posture change (exposes local services to same-subnet peers) — default OFF, with
a persistent inline warning. The pin and firewall preference are non-secret and live in
`state.json`; the device key stays keyring-only and is never logged.

**Scale/Scope**: Small. New package `internal/client/firewall/` (`firewall.go` +
`firewall_windows.go` + `firewall_other.go`, ~60 LOC + a fake for tests). Edited:
`internal/client/apiclient/client.go` (+`WithPinnedCert`, custom `VerifyConnection` for pin-or-CA,
`CertError`+`ErrCertChanged`, fingerprint capture), `internal/client/state/state.go` (two fields +
`SchemaVersion`=2 + v1 acceptance), `internal/client/panel/panel.go` (firewall seam +
`FirewallAllowed`/`SetFirewallAllowed`/`ReconcileFirewall`; pin read/write helpers; `New` gains the
firewall seam), `internal/client/ui/panel.go` (footer firewall `Check` + inline warning;
connect/disconnect/logout reconcile hooks; replace `offerInsecure` with the TOFU first-trust +
cert-changed dialogs + neutral pinned indicator), `internal/client/ui/wizard.go` (TOFU prompts in
`runProvision`; pin-aware client build; neutral indicator), `cmd/lanweave-client/main.go` (build
client with stored pin; inject firewall seam; startup orphan sweep + exit clear), `DESIGN.md`
(§275–§277, §362, §365 + new client-inbound clause), `CLAUDE.md` (plan pointer). New tests alongside
edited packages.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS. One new package with a single named responsibility (`firewall`:
  the host inbound-allow rule for the VPN subnet), platform-split exactly like the existing
  `addr_linux.go` / `addr_windows.go`. No premature abstraction: the firewall seam is one concrete
  interface with two methods (`Allow`/`Clear`) injected for headless testing — not a speculative
  framework. `apiclient` gains options/errors in place (no new package). The firewall preference +
  reconcile logic folds into the existing `panel.Controller` (already the client's headless
  local-device controller since 017), rather than spawning a parallel controller for ~10 lines of
  state. SQLite stays the single source of truth (server untouched); the host firewall rule is
  derivative, fully reconstructible from (preference ∧ connection) and swept on startup — no hidden
  runtime-only state survives a restart. No panics; errors are values.
- **II. Testing Standards (NON-NEGOTIABLE)** — PASS. US1 (TOFU) acceptance is automated against a
  **real** TLS round trip (`httptest` with a self-signed certificate): the pin-or-CA verifier,
  fingerprint capture, and the changed-cert path are exercised with no crypto mocking. US2
  (firewall) decision logic — "rule present ⟺ preference ON ∧ connected", persistence, idempotent
  re-apply, clear-on-logout — is unit-tested in `panel.Controller` against a fake firewall seam.
  This feature crosses **no** SQLite/nftables/WireGuard boundary (the host firewall is a client-side
  `netsh` call, a peer of `addr_windows.go`), so Principle II's "never mock those three" does not
  apply here; the un-mockable boundary is the `netsh` exec + Fyne UI, verified by the standing
  Windows manual matrix (DESIGN §365). `state` migration is unit-tested. Each user story has an
  automated acceptance path for its testable core plus a manual matrix row for its GUI/exec tail.
- **III. User Experience Consistency** — PASS. The TOFU first-trust prompt **names** the server and
  shows the certificate fingerprint; the cert-changed warning is visibly heavier and blocks until
  explicit confirmation; the neutral "self-signed (trusted on this device)" indicator and the
  `--insecure` "⚠ certificate not verified" banner are persistent status elements (consistent with
  "connection/trust state always visible"). Enabling the firewall is **not** a destructive
  operation (no data loss, fully reversible by toggling off), so Principle III's "destructive ops
  must confirm and name the entity" does not require a dialog here; the chosen persistent **inline
  warning** beside the toggle satisfies the "user must understand the consequence" intent without a
  modal. Long operations (connect, re-pin retry) already show progress. Fyne dialogs honor
  Enter/Escape.
- **IV. Performance Requirements** — PASS / N/A. One SHA-256 compare per connection; one local
  `netsh` call per connect/disconnect/toggle. No new server hot path; nothing approaches the API or
  nftables budgets.
- **Security & Operational Discipline** — PASS with recorded amendments. TOFU is strictly safer
  than 017's session-level insecure: there is no sticky "skip verification" state; verification is
  pin-or-CA; a changed certificate forces a heavier, explicit decision. The firewall rule opens
  inbound **only** while the user has opted in **and** is connected, defaults closed, and is swept
  on startup — minimal exposure window. Both are project-wide posture changes, so DESIGN §275–§277
  (cert-trust口径) and §362 (§11 risk row) are amended to the TOFU posture (superseding 017), a new
  clause records that the client may add a host inbound-allow rule for the VPN subnet, and §365's
  GUI manual-verify exception is extended to cover the `netsh` path and the TOFU dialogs. The pin
  and firewall preference are non-secret; the device key remains keyring-only and is never written
  to `state.json` or logged.

No blocking violations. DESIGN amendments and the new package/field/seam are recorded in Complexity
Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/018-client-firewall-and-tofu-pin/
├── plan.md              # This file
├── research.md          # Phase 0 — decisions (TOFU verification, fingerprint capture, firewall lifecycle)
├── data-model.md        # Phase 1 — state schema 1→2; entities; firewall rule shape
├── quickstart.md        # Phase 1 — headless test list + Windows manual verify matrix
├── contracts/
│   └── client-surface.md  # apiclient/state/firewall/panel deltas (no server endpoint change)
├── checklists/
│   └── requirements.md  # spec quality checklist (from /speckit-specify)
└── tasks.md             # Phase 2 output (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/client/firewall/        # NEW package — host inbound-allow rule for the VPN subnet
├── firewall.go                  # rule name + cross-platform Control interface + default System()
├── firewall_windows.go          # netsh advfirewall add/delete (delete-then-add idempotent; sweep)
└── firewall_other.go            # //go:build !windows — no-op Control (preference still persisted)

internal/client/apiclient/
├── client.go            # EDIT — +WithPinnedCert(fpHex); VerifyConnection = pin-or-system + fp capture;
│                        #        +CertError{Fingerprint,Changed}; +ErrCertChanged; fingerprint helper
└── client_test.go       # EDIT — httptest self-signed: first-trust fp, pinned verify-on, changed→ErrCertChanged, CA-valid passes

internal/client/state/
├── state.go             # EDIT — +PinnedCertSHA256, +FirewallAllowVPN; SchemaVersion=2; accept v1 load
└── state_test.go        # EDIT — v1→v2 default migration; round-trip new fields

internal/client/onboard/
└── onboard.go           # EDIT — Provisioner +PinnedCertSHA256 field, written into the saved Record
                         #        (persists a pin accepted during first-run onboarding)

internal/client/panel/
├── panel.go             # EDIT — firewall seam; New(+fw); FirewallAllowed()/SetFirewallAllowed(on,connected)/
│                        #        ReconcileFirewall(connected); SetPinnedCert/UseClient; Logout also clears rule
├── panel_test.go        # EDIT — fake firewall seam; preference∧connected matrix; persistence; clear-on-logout;
│                        #        SetPinnedCert/UseClient (replaces 017 UseInsecureClient test)
└── panel_integration_test.go  # EDIT (if needed) — unchanged server path; logout still reconciles

internal/client/ui/
├── panel.go             # EDIT (gui) — footer firewall Check + inline warning; connect/disconnect/logout
│                        #        reconcile; replace offerInsecure with TOFU first-trust + cert-changed + pinned indicator
└── wizard.go            # EDIT (gui) — runProvision TOFU prompts; build client with stored pin; neutral indicator

cmd/lanweave-client/
└── main.go              # EDIT (gui) — build apiclient with stored pin; inject firewall seam;
                         #        startup firewall.Clear() sweep; defer firewall.Clear() on exit

DESIGN.md                # EDIT — §275–§277, §362, §365 amended (TOFU supersedes 017) + client-inbound clause
docs/ROADMAP.md          # already edited on this branch (018 row + detail section)
CLAUDE.md                # EDIT — active-plan pointer → 018 plan
```

**Structure Decision**: Reuse the existing client layout and add one small platform-split
`firewall` package modeled on `addr_*.go`. TOFU lives in `apiclient` (the existing TLS owner) so the
verifier and fingerprint capture are unit-testable against `httptest`; the pin is persisted by
`state` and threaded through `panel.Controller`, which already owns the client's headless
local-device concerns (statePath, session, insecure bit). The firewall decision logic
(preference ∧ connection) also lives in `panel.Controller` behind a two-method seam so it is
headless-testable; the `gui`-tagged `ui/panel.go` and `main.go` own only the Fyne toggle/dialogs and
the connect/disconnect/startup/exit wiring. No server change.

## Complexity Tracking

| Violation / Exception | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| New `internal/client/firewall` package | The host inbound rule is a distinct OS concern with a Windows/other split; it has a single responsibility and mirrors the existing `addr_*.go` pattern | Folding `netsh` firewall calls into `tunnel` was rejected — the tunnel does not (and should not) know the user's toggle preference, and mixing host-firewall policy into the WireGuard adapter lifecycle blurs two responsibilities (Principle I) |
| Re-amend DESIGN §275–§277 / §362 to TOFU, superseding 017's reactive session opt-in; add a client host-inbound-rule clause; extend §365 | TOFU is a project-wide cert-trust posture change that supersedes 017, and the client now manages a host firewall rule (new posture not previously described) | Leaving §275–§277 describing the 017 session opt-in would make DESIGN contradict the shipped behavior; spec may refine but not contradict DESIGN, so DESIGN is updated in the same PR (DESIGN authority, Principle/Workflow gate) |
| `state.Record` gains two fields in one `SchemaVersion` 1→2 bump | TOFU needs the pin and the firewall toggle needs a persisted preference; both are additive non-secret fields | Two separate migrations (1→2 then 2→3) were rejected as needless churn; bundling them in one bump keeps a single, well-tested migration and matches the spec's atomic-unit framing |
| `panel.Controller` grows a firewall seam (constructor param) + three methods, and pin read/write helpers | The firewall decision logic and the pin persistence must be headless-testable (Principle II) and need `statePath`/`record`, which the Controller already holds | A separate "device controller" for ~10 lines of preference∧connection logic was rejected as more surface than the logic justifies; injecting one two-method seam keeps the data flow visible and the logic unit-testable without a new package boundary |
