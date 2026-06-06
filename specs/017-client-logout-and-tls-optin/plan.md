# Implementation Plan: Client Logout and TLS Opt-In

**Branch**: `017-client-logout-and-tls-optin` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/017-client-logout-and-tls-optin/spec.md`

## Summary

Give the desktop client two missing experience pieces. **Logout** (P1): a confirmed action in
the main panel that tears the tunnel down, removes this device's own node on the server via the
existing `DELETE /api/v1/nodes/{id}` endpoint, clears the local session token, device private
key, and state record, and returns the user to the first setup step so they can re-enter a
server URL. Remote removal is best-effort — local logout always completes (FR-008).
**Interactive insecure-TLS opt-in** (P2): when a connection fails certificate verification
(`apiclient.ErrUntrustedCert`), instead of an opaque failure the user gets an explicit prompt to
continue insecurely; accepting it rebuilds that session's API client with verification disabled
and shows a persistent "certificate not verified" indicator. The choice is per-session and never
persisted; the existing `--insecure` CLI flag is retained. This reverses DESIGN §275/§360's
"never in the UI" rule (amended in the same PR) while preserving its "no mindless toggle" intent
via a reactive, per-session, clearly-warned opt-in.

## Technical Context

**Language/Version**: Go 1.26.2 (module `lanweave`)

**Primary Dependencies**: client GUI Fyne v2.7.4 (behind `//go:build gui`); standard library
`crypto/tls` + `crypto/x509` (already used by `apiclient`). No new dependency.

**Storage**: None added. SQLite (server) is untouched — logout reuses the existing
`deleteNode` handler, whose cascade already reconciles WireGuard peers and nftables zone sets.
Client `state.json` schema is unchanged (`SchemaVersion` stays 1); logout *removes* the record.
The per-session insecure choice is in-memory only.

**Testing**: `go test` under the standing `unshare -rUn bash -c 'ip link set lo up && go test
./...'` gate. New tests: (a) headless unit — `panel.Controller.Logout` and
`UseInsecureClient` against the existing `fakeAPI` seam; (b) integration — logout against a
**real** server (real SQLite + nftables + `wireguard-go`) in `panel_integration_test.go`,
asserting the node, its WG peer, and its zone-set membership are gone; (c) `apiclient.DeleteNode`
against an `httptest` server. GUI dialogs/indicators are verified manually on Windows per the
project's standing GUI exception (`quickstart.md`).

**Target Platform**: Windows 10/11 desktop client (the only end-user surface) + Linux CI for
headless + integration tests.

**Project Type**: Client/server Go monorepo; this feature touches only the **client** (apiclient,
panel controller, ui, main) plus a DESIGN.md amendment. No server code changes.

**Performance Goals**: N/A for new hot paths. Logout issues one authenticated write
(`DeleteNode`), already within the Principle IV write budget (≤ 300 ms incl. nft/WG side
effects). Tunnel teardown and local clears are local and sub-second.

**Constraints**: `apiclient` bakes TLS verification at `New()` (`InsecureSkipVerify` is set in
the constructor) — "go insecure" therefore means **rebuilding** the client, never mutating a
live one. Logout's local clears MUST always run even when the server is unreachable (FR-008).
The insecure choice MUST NOT be persisted (FR-012). The destructive logout MUST confirm and name
the affected device/server (Principle III).

**Scale/Scope**: Small. Edited: `internal/client/apiclient/client.go` (+`DeleteNode`,
+`Insecure()`), `internal/client/panel/panel.go` (`api` iface +`DeleteNode`; `New` gains
`statePath`+`insecure`; +`Logout`, +`UseInsecureClient`, +`Insecure`),
`internal/client/ui/panel.go` (logout button + confirm + insecure banner + cert opt-in + retry +
`restart` callback), `internal/client/ui/wizard.go` (cert opt-in in `runProvision` + indicator),
`cmd/lanweave-client/main.go` (pass `statePath`/`insecure` to `panel.New`; supply `restart`),
`DESIGN.md` (§275, §360). New tests alongside the edited packages. No new package, no new
external API.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS. Small, reversible, no new package, no premature abstraction. The
  insecure-retry seam is two concrete call sites (wizard flips its existing field; controller
  gains `UseInsecureClient`) rather than a speculative factory framework. SQLite stays the single
  source of truth (server logic untouched; logout uses the existing reconciling handler). No
  scattered config; no panics added.
- **II. Testing Standards (NON-NEGOTIABLE)** — PASS. Logout crosses to the server's real
  SQLite + nftables + WireGuard, so its acceptance test is an **integration** test against a real
  server (no mocks of those systems), added to `panel_integration_test.go`. Headless unit tests
  cover `Logout` orchestration and `UseInsecureClient` via the existing fake `api` seam (the
  HTTP boundary is the only mockable seam). `apiclient.DeleteNode` gets an `httptest` test. US1
  (logout) → integration acceptance test; US2 (insecure opt-in) → the decision logic
  (`errors.Is(err, ErrUntrustedCert)` → rebuild) is unit-tested; the Fyne dialog/indicator is on
  the standing manual-GUI exception. Regression-style assertions accompany the behavior.
- **III. User Experience Consistency** — PASS. Logout is destructive → a confirmation that
  **names** the device and server and states the consequences (disconnect, node removal, re-enter
  URL) before proceeding (FR-002). The insecure indicator is a persistent status element,
  consistent with the principle's "connection state always visible". Errors stay human-readable
  (reuse `panelMessage`/`friendly`); Enter/Escape already wired in dialogs.
- **IV. Performance Requirements** — PASS / N/A. One existing-budget write; no new server hot
  path; local teardown/clears are sub-second.
- **Security & Operational Discipline** — PASS with a recorded amendment. Exposing insecure in
  the UI is a project-wide security posture change, so it is re-registered in `DESIGN.md §11`
  (§360) with its mitigations (reactive only, per-session, never persisted, persistent warning,
  CLI flag retained) and §275 is amended in the same PR (DESIGN authority). Logout improves
  hygiene (clears session token + device key). No secret is logged; the device key is deleted
  via the keyring, never written to a file.

No blocking violations. One DESIGN amendment is recorded in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/017-client-logout-and-tls-optin/
├── plan.md              # This file
├── research.md          # Phase 0 — 10 decisions
├── data-model.md        # Phase 1 — entities + (no) schema changes
├── quickstart.md        # Phase 1 — headless + manual verify matrix
├── contracts/
│   └── client-operations.md  # reused DELETE-node endpoint + client interface deltas
├── checklists/
│   └── requirements.md  # spec quality checklist (from /speckit-specify)
└── tasks.md             # Phase 2 output (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/client/apiclient/
├── client.go            # EDIT — +DeleteNode(nodeID); +Insecure() getter; update WithInsecure doc
└── client_test.go       # EDIT — DeleteNode httptest test (204 → nil; non-2xx → mapped error)

internal/client/panel/
├── panel.go             # EDIT — api iface +DeleteNode; New(+statePath,+insecure);
│                        #        +Logout()(remoteRemoved,err); +UseInsecureClient(api); +Insecure()
├── panel_test.go        # EDIT — fakeAPI +DeleteNode; Logout + UseInsecureClient unit tests
└── panel_integration_test.go  # EDIT — logout against a real server (node/peer/zone-set gone)

internal/client/ui/
├── panel.go             # EDIT (gui) — Logout button + confirm dialog; insecure banner;
│                        #        cert-error opt-in + retry; restart callback param on NewPanel
└── wizard.go            # EDIT (gui) — runProvision cert-error opt-in (flip z.insecure + retry);
                         #        insecure indicator line; showHome passes restart to NewPanel

cmd/lanweave-client/
└── main.go              # EDIT (gui) — panel.New(...statePath, *insecure); restart closure for Home

DESIGN.md                # EDIT — §275 (UI exposure rule) + §360 (§11 risk register) amended
docs/ROADMAP.md          # already edited on this branch (017 row + detail section)
CLAUDE.md                # EDIT — SPECKIT marker → 017 plan
```

**Structure Decision**: Reuse the existing client layout. All behavior changes land in the
already-headless `apiclient` and `panel` packages (testable in the `unshare` gate) plus the two
`//go:build gui` UI files and `main.go`. No server change (the DELETE-node endpoint and its
cascade already exist). The Fyne-free `panel.Controller` owns the destructive logout sequence and
the insecure-state bit so both are unit/integration testable; the `gui`-tagged files own only the
dialogs, the banner, and window navigation.

## Complexity Tracking

| Violation / Exception | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Reverses DESIGN §275/§360 ("certificate-skip never in the UI") to allow a reactive insecure opt-in, re-registered in DESIGN §11 | GUI users pointed at self-signed/internal-CA servers (the common test/lab case) currently hit an opaque failure with no path forward except an undocumented CLI flag (US2) | A persistent checkbox was rejected at the spec stage (mindless toggling). Leaving it CLI-only leaves GUI users stuck. The chosen reactive-only, per-session, clearly-warned opt-in preserves §275's intent while unblocking the user; cost is contained and recorded in §11 |
| `panel.New` signature grows (`statePath`, `insecure`) and `ui.NewPanel` gains a `restart` callback | Logout must clear `state.json` (needs the path) and report/seed the insecure indicator; and the panel must navigate back to the wizard (no such edge exists today) | Threading these via package globals or a new app-navigator object was rejected as hidden state / premature abstraction (Principle I); explicit constructor params keep the data flow visible and the controller headless-testable |
