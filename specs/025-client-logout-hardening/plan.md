# Implementation Plan: Client Logout Hardening

**Branch**: `025-client-logout-hardening` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/025-client-logout-hardening/spec.md`

## Summary

Harden the Windows client logout so it never leaves an orphaned ("zombie") node
on the server. Today `panel.Controller.Logout()` is best-effort-remote +
always-clear-local: it wipes local credentials even when the server was
unreachable, stranding a registered node nobody can remove. This slice reorders
the flow to **remove the remote node first (while the tunnel is still up), retry
on failure up to 3× at a fixed 1 s interval, and only then tear down locally**.
A purely network-unreachable failure **blocks** logout (local state untouched)
and surfaces a two-button prompt — *Cancel* (default) / *Force log out anyway*.
The force path is the old full local teardown, knowingly accepting a server-side
orphan. On the success path the device's refresh token is revoked server-side
(slice 024 endpoint) so no renewable credential lingers. All new prompt text is
bilingual (zh-Hans + en).

The change is **client-only**: the server endpoints it relies on already exist —
`DELETE /api/v1/nodes/{id}` (017) and `POST /api/v1/logout` (024). No schema,
API, or server behavior changes.

## Technical Context

**Language/Version**: Go 1.23

**Primary Dependencies**: existing client packages only — `internal/client/panel`
(headless controller, the locus of change), `internal/client/apiclient` (typed
errors incl. `ErrUnreachable`, `ErrSessionExpired`, lazy refresh in `do`),
`internal/client/ui` (Fyne `//go:build gui` panel + `confirmLogout`),
`internal/client/i18n` (JSON bundles), `internal/client/firewall`,
`internal/client/tunnel`, `internal/client/state`, `internal/client/keyring`.

**Storage**: N/A — no DB changes. Client-side secrets remain in the OS keyring;
local state in the state file. Server `refresh_tokens` is touched only via the
existing 024 revoke endpoint (no new columns).

**Testing**: `go test` headless against the real REST client over `httptest`
(loopback must be UP: `unshare -rUn bash -c 'ip link set lo up && go test ./...'`);
`panel.Controller` driven against a fake `api` boundary + real `firewall`/`state`
fakes; injected clock for the 3×1 s retry (no wall-clock sleeps, Constitution II).
GUI two-button dialog verified manually on the Mesa-OpenGL Windows VM
(`//go:build gui`).

**Target Platform**: Windows 10/11 desktop client (the only end-user surface).

**Project Type**: Desktop application (Go + Fyne), headless controller core.

**Performance Goals**: Blocking decision completes within ~3 s worst case (3
attempts × 1 s interval). In-progress indicator shown during retries (Principle
III: no silent wait > 500 ms).

**Constraints**: Network-unreachable vs. any-HTTP-response distinction is the
blocking trigger and MUST be exact (`errors.Is(err, apiclient.ErrUnreachable)`
only). No new sentinel primitives; reuse existing typed errors. Logout must reach
the remote API over the public network *before* the tunnel is torn down.

**Scale/Scope**: One controller method reworked into a small state machine, one
GUI dialog reshaped from one-button to two-button, ~6 new i18n keys × 2 locales.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Applies? | How this design honors it |
|-----------|----------|---------------------------|
| I. Code Quality | Yes | Change is local to `panel.Controller.Logout` + the GUI `confirmLogout`; no new package, no speculative abstraction. The retry/branch logic is one named method with a small typed result; errors stay values (no panics). SQLite remains the source of truth — no client-side hidden state. `gofmt`/`go vet`/`staticcheck` clean. |
| II. Testing (NON-NEGOTIABLE) | Yes | Each user story gets a headless acceptance test against the real apiclient: US1 block-on-unreachable (no local clear), US2 clean removal + RT revoke + local clear, US3 force teardown, plus the 401→refresh→retry edge. Retry timing uses an **injected clock**, never a wall-clock sleep. No mocking of the DB/firewall/WG boundary — `httptest` server is the real REST surface; firewall/state use the existing real fakes. GUI dialog is the one manually-verified surface (documented in quickstart). |
| III. UX Consistency | Yes | Retry shows the existing infinite-progress indicator (no silent wait). The blocked prompt names the action and offers Enter=Cancel (safe default) / Escape=cancel keyboard behavior. Destructive force-logout is explicit and labeled. Errors are human sentences, bilingual. |
| IV. Performance | Yes | Worst-case blocking path is bounded (3 × 1 s). No server-side cost; the two extra calls (DeleteNode, logout-revoke) already existed. No new budget impact. |
| Security & Operational Discipline | Yes | Logout now revokes the refresh token on the success path (no lingering renewable credential) and prevents the orphaned-node residue. No secrets logged. Reuses vetted crypto via existing endpoints — no new primitives. The accepted residue of a **forced** logout (server-side orphan) is an explicit, user-initiated trade-off; recorded in DESIGN.md §11 amendment in this PR. |

**Result**: PASS. No violations; Complexity Tracking table omitted.

**DESIGN.md amendment (same PR)**: §11 / onboarding description changes from
"logout works offline, only warns about possible residue" to "logout blocks on
network-unreachable to avoid residue, with an explicit force-logout escape hatch
that accepts a server-side orphan." This refines, not contradicts, DESIGN.md, so
no constitution version bump is required.

## Project Structure

### Documentation (this feature)

```text
specs/025-client-logout-hardening/
├── plan.md              # This file
├── research.md          # Phase 0 — decisions (retry, error taxonomy, clock, ordering)
├── data-model.md        # Phase 1 — logout outcome state machine (no DB entities)
├── quickstart.md        # Phase 1 — headless + manual GUI test scenarios
├── contracts/
│   └── logout-controller.md   # Behavioral contract for Controller.Logout / ForceLogout
└── checklists/
    └── requirements.md  # Spec quality checklist (from /speckit-specify)
```

### Source Code (repository root)

```text
internal/client/
├── panel/
│   ├── panel.go                    # MODIFY: Logout() → retry+branch state machine;
│   │                               #         add ForceLogout(); inject clock/sleep seam
│   ├── panel_test.go               # MODIFY/ADD: US1 block, US2 clean, US3 force, 401 edge
│   └── panel_integration_test.go   # MODIFY: real-server logout path if affected
├── apiclient/
│   └── client.go                   # READ-ONLY: ErrUnreachable / ErrSessionExpired reused
├── ui/
│   ├── panel.go                    # MODIFY (//go:build gui): confirmLogout → two-button
│   │                               #         blocked prompt + force path; reorder teardown
│   └── panel_overflow_test.go      # MODIFY if logout entry assertions shift
└── i18n/
    ├── en.json                     # ADD: blocked-prompt + force-logout keys (en)
    ├── zh-Hans.json                # ADD: same keys (zh-Hans)
    └── i18n_test.go                # parity test already enforces key coverage

DESIGN.md                           # MODIFY: §11 / onboarding logout description
docs/ROADMAP.md                     # checked off in the merge commit (per constitution)
```

**Structure Decision**: Single desktop-client module; all behavior change lands
in the headless `panel` controller (testable without Fyne) with a thin GUI
reshaping in `ui/panel.go`. No new packages — Principle I (abstract on the
fourth callsite, not the second) keeps this as edits to existing files.

## Complexity Tracking

> No constitution violations — table intentionally omitted.
