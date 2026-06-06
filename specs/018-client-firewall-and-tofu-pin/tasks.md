---
description: "Task list for feature 018 — client firewall control and TOFU certificate pinning"
---

# Tasks: Client Firewall Control and TOFU Certificate Pinning

**Input**: Design documents from `/specs/018-client-firewall-and-tofu-pin/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/client-surface.md, quickstart.md

**Tests**: Mandatory per constitution Principle II (NON-NEGOTIABLE). US1 (TOFU) gets an automated
acceptance test against a **real** TLS round trip (`httptest` self-signed server) covering
first-trust fingerprint capture, pin-or-CA verification, and the changed-certificate path — no crypto
mocks. US2 (firewall) decision logic is unit-tested in the headless `panel.Controller` against a fake
firewall seam (the `netsh` execution + Fyne UI are the un-mockable boundary, verified by the Windows
manual matrix). This feature crosses **no** SQLite/nftables/WireGuard boundary, so Principle II's
"never mock those three" does not apply; the host-firewall `netsh` call is a peer of
`addr_windows.go` and carries the same standing GUI/exec manual-verify exception (DESIGN §365).
US2's end-to-end acceptance (the real rule install/remove + a peer reaching the device) is
intrinsically a real-Windows-host + real-peer outcome and cannot run under the `unshare` gate; it is
therefore satisfied by the headless decision-logic test (T015) plus the Windows manual matrix (T022),
under the §365/§11 GUI/exec exception that T013 extends to the firewall path (accepted finding C1).

**Organization**: Tasks are grouped by user story. US1 = TOFU certificate pinning (P1, MVP); US2 =
client firewall control (P2). No server code changes. Both stories share one `state.Record` schema
migration (Foundational).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1 (TOFU) or US2 (firewall); Setup/Foundational/Polish carry no story label
- All paths are repository-root-relative

## Path Conventions

Existing Go monorepo. Client code under `internal/client/...` and `cmd/lanweave-client/`. Tests live
beside the code they cover and run under `unshare -rUn bash -c 'ip link set lo up && go test ./...'`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish a known-green baseline so later failures are attributable to this feature.

- [X] T001 Establish baseline: run `unshare -rUn bash -c 'ip link set lo up && go test ./...'`,
  `CGO_ENABLED=0 go build ./cmd/lanweave-client`, and `go build -tags gui ./...`; confirm all pass
  before any edits.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The single `state.Record` schema migration (1 → 2) that BOTH stories build on — TOFU
needs `PinnedCertSHA256`, the firewall toggle needs `FirewallAllowVPN`. Bundled into one bump per the
spec's atomic-unit decision so there is exactly one migration to test.

**⚠️ CRITICAL**: No user-story work can begin until this phase compiles and its test passes.

- [X] T002 Migration test in `internal/client/state/state_test.go` (write FIRST, ensure it FAILS):
  (a) a `state.json` with `"schema_version": 1` and none of the new keys `Load`s successfully with
  `PinnedCertSHA256 == ""` and `FirewallAllowVPN == false`; (b) a round-trip `Save` then `Load` of a
  record carrying both new fields preserves them and persists `schema_version: 2`.
- [X] T003 In `internal/client/state/state.go`: bump `SchemaVersion` to `2`; add
  `PinnedCertSHA256 string` (json `pinned_cert_sha256`) and `FirewallAllowVPN bool` (json
  `firewall_allow_vpn`) to `Record`; keep `Load`'s validation as-is (still accepts schema 1 and 2,
  rejects 0). Makes T002 pass. No behavior change for existing callers.

**Checkpoint**: Schema v2 compiles, v1 records load with defaulted new fields, headless gate green.

---

## Phase 3: User Story 1 - Trust a self-signed server once, not every time (Priority: P1) 🎯 MVP

**Goal**: Replace 017's session-level insecure opt-in with trust-on-first-use. On an unverifiable
certificate with no pin, prompt once (showing the fingerprint); accepting persists the leaf SHA-256
in `state.json` so later connects — including after restart — are silent. Verify = pin OR system CA.
A changed certificate raises a heavier `ErrCertChanged` warning that blocks until accepted (which
overwrites the pin). `--insecure` CLI flag retained.

**Independent Test**: Point setup at a self-signed server; first connect prompts with a fingerprint;
accept → connects + neutral "self-signed (trusted on this device)" indicator; restart + reconnect →
no prompt; present a different cert → heavier "certificate changed" warning blocks until accepted.

### Governance gate (constitution — DESIGN authority)

- [X] T004 [US1] Amend `DESIGN.md` **in this same PR**: rewrite §275–§277 from the 017 reactive
  session opt-in to the TOFU posture (first-trust per server, persisted leaf SHA-256, verify = pin OR
  system CA, heavier warning on certificate change, `--insecure` CLI retained, no per-session
  "not remembered" UI path); update the §362 (§11) accepted-risks row accordingly; extend the §365
  GUI manual-verify exception to cover the TOFU dialogs/indicator. Marks 017's FR-009–014 superseded.
  Required before the UI tasks below are legitimate.

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

> Write these FIRST and ensure they FAIL before implementing T007–T009.

- [X] T005 [P] [US1] TOFU acceptance test in `internal/client/apiclient/client_test.go` against an
  `httptest.NewTLSServer` (self-signed): (a) a default `apiclient.New(url)` request fails with a
  `*CertError` where `errors.Is(err, ErrUntrustedCert)` and `Fingerprint == hex(SHA-256(leaf.Raw))`;
  (b) `apiclient.New(url, WithPinnedCert(fp))` with that fingerprint succeeds; (c) using
  `WithRootCAs(serverPool)` plus a bogus pin still succeeds (pin-OR-CA); (d) `WithPinnedCert(F)`
  against a server presenting a different self-signed cert `F'` fails with
  `errors.Is(err, ErrCertChanged)` and `*CertError{Fingerprint: F', Changed: true}`.
- [X] T006 [P] [US1] `Controller.SetPinnedCert` / `UseClient` unit test in
  `internal/client/panel/panel_test.go` (fake `api`): `SetPinnedCert("abc123")` writes
  `PinnedCertSHA256` into `state.json` at the controller's `statePath` (reload to confirm) and updates
  the in-memory record; `UseClient(fake2)` routes subsequent calls to `fake2` with the cached session
  token re-applied and **without** setting the insecure indicator. Replaces 017's
  `TestUseInsecureClient` (the reactive session-insecure swap is removed).

### Implementation for User Story 1

- [X] T007 [P] [US1] In `internal/client/apiclient/client.go`: add `WithPinnedCert(sha256Hex string)
  Option`; build the `tls.Config` with `InsecureSkipVerify: true` + a `VerifyConnection` that accepts
  iff the leaf fingerprint equals the pin OR the chain verifies against the configured roots
  (`WithRootCAs` pool else system, `DNSName` = host), else returns `*CertError{Fingerprint, Changed}`
  with `Changed == (pin != "")`; add `ErrCertChanged`, the `CertError` struct with `Error()`/`Is()`
  (Changed→ErrCertChanged else ErrUntrustedCert), and a leaf-fingerprint helper
  (`hex(sha256(cert.Raw))`); unwrap `*CertError` in `do()`. Leave `WithInsecure` short-circuit
  unchanged. Makes T005 pass.
- [X] T008 [US1] In `internal/client/onboard/onboard.go`: add a `PinnedCertSHA256 string` field to
  `Provisioner`, and set `rec.PinnedCertSHA256 = p.PinnedCertSHA256` in the `state.Record` it builds
  before `state.Save`, so a pin accepted during first-run onboarding is persisted.
- [X] T009 [US1] In `internal/client/panel/panel.go`: add `SetPinnedCert(fp string) error` that sets
  `c.record.PinnedCertSHA256 = fp` and `state.Save(c.statePath, c.record)` (for re-pinning during a
  running session after a certificate change); add `UseClient(a api)` that swaps `c.api` and
  re-applies the cached session token **without** flipping the insecure flag, replacing 017's
  `UseInsecureClient` (whose only caller — the reactive session opt-in — is removed in T011). Makes
  T006 pass.
- [X] T010 [US1] (gui) In `internal/client/ui/wizard.go`: build the provisioning client with the
  stored/accepted pin (`apiclient.WithPinnedCert`); on a `*CertError` from `runProvision` where
  `errors.Is(err, apiclient.ErrUntrustedCert)` show a first-trust dialog that NAMES the server and
  shows the fingerprint — on accept set the pin (thread it onto the `Provisioner` via T008's field)
  and re-run `runProvision`, on decline return to `stepServer`; on `errors.Is(err,
  apiclient.ErrCertChanged)` show a visibly heavier "certificate changed" dialog (accept overwrites,
  re-runs); render a neutral "self-signed (trusted on this device)" indicator while a pin is in use.
  Remove the 017 per-session "continue insecurely, not remembered" path.
- [X] T011 [US1] (gui) In `internal/client/ui/panel.go`: replace `offerInsecure` with TOFU handling —
  when a running-client operation or `start()`/`LoadSession` returns `*CertError`, show the first-trust
  dialog (`ErrUntrustedCert`) or the heavier changed dialog (`ErrCertChanged`); on accept call
  `ctrl.SetPinnedCert(fp)`, rebuild the API client with `apiclient.New(rec.ServerURL,
  apiclient.WithPinnedCert(fp))` via `ctrl.UseClient(...)` (re-applying the session token), and retry;
  show the neutral pinned indicator when pinned and keep the existing red "⚠ certificate not verified"
  banner only for the `--insecure` CLI case (`ctrl.Insecure()`).
- [X] T012 [US1] (gui) In `cmd/lanweave-client/main.go`: on the Home branch build
  `apiclient.New(rec.ServerURL, apiclient.WithPinnedCert(rec.PinnedCertSHA256))` (or
  `WithInsecure()` when the `--insecure` flag is set), so a previously trusted server connects
  silently on startup.

**Checkpoint**: US1 fully functional and independently testable (MVP). First-trust → silent reconnect
→ changed-cert warning all work; `--insecure` still works.

---

## Phase 4: User Story 2 - Allow VPN peers to reach this device, on purpose (Priority: P2)

**Goal**: A footer toggle (default OFF, persisted) that, while ON and connected, installs the named
`lanweave-vpn-inbound` Windows Defender inbound-allow rule scoped to `100.127.0.0/16`. Rule exists
iff (preference ON ∧ Connected); added on connect/toggle-on-while-connected, removed on
disconnect/toggle-off/logout/exit; named + idempotent + startup orphan sweep; inline exposure warning,
no confirm dialog; Windows-only enforcement.

**Independent Test**: Toggle off + connected → peer blocked; toggle on while connected → peer reaches
a local service; toggle off → blocked; disconnect → rule gone; kill + relaunch → orphan swept, no
duplicate; restart → toggle state remembered.

### Governance gate (constitution — DESIGN authority)

- [X] T013 [US2] Amend `DESIGN.md` **in this same PR**: add a clause near §77 that the Windows client
  MAY add a host inbound-allow rule (`lanweave-vpn-inbound`, `remoteip=100.127.0.0/16`, inbound, all
  ports, `profile=any`) when the user opts in AND is connected; add a §11 accepted-risks row noting
  that enabling it exposes all local services to same-subnet peers (default OFF, user-controlled,
  swept on startup). In the same §11/§365 edit, **extend the standing GUI/exec manual-verify
  exception** to cover the firewall `netsh` execution and the toggle UI — i.e. US2's end-to-end
  acceptance (the rule actually installing/removing and a peer reaching the device) is satisfied by
  the headless decision-logic test (T015) plus the Windows manual matrix (T022 M6/M7/M9), since the
  effect is intrinsically a real-Windows-host + real-peer outcome that cannot be exercised under the
  `unshare` gate (accepted finding C1). Required before the firewall UI task below is legitimate.

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

> Write T015 FIRST and ensure it FAILS before implementing T016.

- [X] T014 [US2] Create the `internal/client/firewall` package: `firewall.go` (the `Control`
  interface `{ Allow() error; Clear() error }`, `RuleName = "lanweave-vpn-inbound"`, `VPNSubnet =
  "100.127.0.0/16"`, `System() Control`, package-level `Clear() error` convenience for the startup
  sweep); `firewall_windows.go` (`netsh advfirewall firewall` delete-then-add for `Allow`,
  delete-ignoring-no-match for `Clear`, mirroring `addr_windows.go`'s `exec.Command`); `firewall_other.go`
  (`//go:build !windows` no-op `Control`).
- [X] T015 [P] [US2] Firewall decision unit test in `internal/client/panel/panel_test.go` with a fake
  `firewall.Control` (records Allow/Clear calls) + the existing fake `api`: assert the 4-cell truth
  table — `ReconcileFirewall(connected)` and `SetFirewallAllowed(on, connected)` call `Allow()` iff
  (preference ON ∧ connected) else `Clear()`; `SetFirewallAllowed` persists `FirewallAllowVPN` to
  `state.json` (reload to confirm); `Logout()` calls `Clear()`; two `Allow()`s are idempotent (each is
  delete-then-add, no duplicate "open" state in the fake).

### Implementation for User Story 2

- [X] T016 [US2] In `internal/client/panel/panel.go`: change `New` to
  `New(a api, record state.Record, keys keyring.Store, statePath string, insecure bool, fw
  firewall.Control) *Controller` storing `fw`; add `FirewallAllowed() bool`,
  `SetFirewallAllowed(on, connected bool) error` (persist `FirewallAllowVPN` via `state.Save`, then
  `fw.Allow()` if `on && connected` else `fw.Clear()`), and `ReconcileFirewall(connected bool) error`
  (`fw.Allow()` if `c.record.FirewallAllowVPN && connected` else `fw.Clear()`); have `Logout()` also
  call `fw.Clear()` before clearing state. Update the `newController` test helper (inject the fake
  firewall) and the two `panel.New` call sites — `cmd/lanweave-client/main.go` and
  `internal/client/ui/wizard.go` (`showHome`) — to pass `firewall.System()`. Makes T015 pass.
- [X] T017 [US2] (gui) In `internal/client/ui/panel.go`: add a footer `widget.Check` labelled "Allow
  inbound from VPN peers (100.127.0.0/16)" initialized from `ctrl.FirewallAllowed()`, with a
  persistent inline warning label beside it stating enabling it lets same-subnet peers reach all local
  services (no confirm dialog); the check handler calls `ctrl.SetFirewallAllowed(checked,
  p.tn.State() == tunnel.Connected)`; after a successful `tn.Connect()` in `onConnect` call
  `ctrl.ReconcileFirewall(true)`, and in `onDisconnect` / `confirmLogout` call
  `ctrl.ReconcileFirewall(false)`.
- [X] T018 [US2] (gui) In `cmd/lanweave-client/main.go`: inject `firewall.System()` into `panel.New`;
  call `firewall.Clear()` once at startup (orphan sweep) before building the panel; add
  `defer firewall.Clear()` next to the existing `defer tn.Close()` so app exit closes the device.

**Checkpoint**: US1 and US2 both work independently. The firewall opens only while opted-in AND
connected, and never strands an open rule.

---

## Phase 5: Polish & Cross-Cutting Concerns

- [X] T019 [P] Confirm the non-graphical stub still builds: `CGO_ENABLED=0 go build ./cmd/lanweave-client`.
- [X] T020 Run the full headless gate green: `unshare -rUn bash -c 'ip link set lo up && go test ./...'`
  (includes the TOFU acceptance test, the firewall decision tests, and the state migration test).
- [X] T021 [P] Confirm the GUI build + vet are clean: `go build -tags gui ./...`,
  `go vet -tags gui ./...`, and `gofmt -l .` (empty).
- [ ] T022 Execute the `quickstart.md` manual verify matrix (rows M1–M11) on a Windows desktop against
  a test server (self-signed for the TOFU rows) — the GUI/`netsh` acceptance gate for US1 + US2.
- [X] T023 Mark the `docs/ROADMAP.md` 018 row done (✅) in the merge commit.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**: none — run first to confirm a green baseline.
- **Foundational (T002–T003)**: depends on Setup; T002 (test) before T003 (impl). BLOCKS both stories
  (both add a field to the migrated record).
- **US1 (T004–T012)**: depends on Foundational. The MVP — can ship alone (firewall untouched). T004
  (DESIGN amendment) gates the UI tasks T010/T011.
- **US2 (T013–T018)**: depends on Foundational. Independent of US1 at the behavior level. T013 (DESIGN
  amendment) gates T017. Note: T016 grows `panel.New`'s signature and edits `main.go` + `wizard.go`,
  which US1's T010/T012 also touch — sequence these edits within the shared PR.
- **Polish (T019–T023)**: depends on the stories being delivered.

### Within US1

- T004 first (governance gate for the UI). Tests T005, T006 [P] (different files) before impl.
- T007 (apiclient) makes T005 pass; T009 (Controller.SetPinnedCert) makes T006 pass; T008 (Provisioner
  field) supports the wizard first-run pin persistence.
- T010 depends on T007 + T008; T011 depends on T007 + T009; T012 depends on T007. T010 ∥ T011 once
  their deps are met (different gui files) — but both follow T004.

### Within US2

- T013 first (governance gate). T014 (firewall package) before T015/T016 (type must exist).
- T015 (decision test) before T016 (impl) — tests-first.
- T017 depends on T013 + T016; T018 depends on T014 + T016. T017 ∥ T018 once deps are met.

### Parallel Opportunities

- US1 tests T005 ∥ T006; US1 impl T007 ∥ (T008, T009 different files); US1 gui T010 ∥ T011.
- US2 gui/impl: after T016, T017 ∥ T018.
- Polish: T019 ∥ T021.
- Whole stories: with two people, one takes US1 (T004–T012) and one takes US2 (T013–T018) after
  Foundational — coordinating the shared `panel.New`/`main.go`/`wizard.go` edits (T016).

---

## Parallel Example: User Story 1

```bash
# Tests first (different files → parallel):
Task: "T005 TOFU acceptance test in internal/client/apiclient/client_test.go"
Task: "T006 Controller.SetPinnedCert unit test in internal/client/panel/panel_test.go"

# Implementation (different files → parallel):
Task: "T007 WithPinnedCert + VerifyConnection + CertError in internal/client/apiclient/client.go"
Task: "T009 Controller.SetPinnedCert in internal/client/panel/panel.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. T001 Setup → green baseline.
2. T002–T003 Foundational → schema v2 compiles, v1 loads.
3. T004–T012 US1 → TOFU works end-to-end.
4. **STOP and VALIDATE**: run T005 acceptance + the TOFU rows of the manual matrix. Ship.

### Incremental Delivery

1. Setup + Foundational → ready.
2. US1 (TOFU) → acceptance-tested + DESIGN amended → manual TOFU rows → deliver (MVP).
3. US2 (firewall) → decision-tested + DESIGN amended → manual firewall rows → deliver.
4. Polish (T019–T023) → close out, check off ROADMAP.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- No server code changes; no SQLite/nftables/WireGuard boundary — the host firewall is a client-side
  `netsh` call (peer of `addr_windows.go`), so Principle II's "never mock those three" does not apply.
- Both new fields land in ONE `state.Record` schema bump (1 → 2); old v1 records load with defaults.
- TOFU supersedes 017's session-level insecure opt-in (DESIGN §275–§277/§362 amended in T004);
  `--insecure` CLI flag retained.
- The `CLAUDE.md` active-plan pointer was already set to this plan during `/speckit-plan`.
- Commit after each task or logical group; verify tests fail before implementing.
