# Implementation Plan: per-user-limits

**Branch**: `023-per-user-limits` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/023-per-user-limits/spec.md`

## Summary

Add two server-wide configuration caps — **max devices per user** and **max owned
zones per user**, each defaulting to 10 — enforced atomically at the SQLite store
layer when a regular user registers a device or creates a zone. A cap of `0` means
unlimited; a negative cap is a startup error; the admin account is exempt. The
enforcement mechanism is a single conditional-`INSERT` statement
(`INSERT … SELECT … WHERE (SELECT COUNT(*) …) < ?`) so the count-and-insert is one
atomic SQL statement under SQLite's writer lock — no read-then-write TOCTOU window,
no explicit transaction plumbing. Two new distinct error codes
(`device_limit_reached`, `zone_limit_reached`, both 409) propagate to the Fyne
client, which maps them to localized messages in the existing wizard (device setup)
and panel (zone creation) flows, in zh-Hans and en.

## Technical Context

**Language/Version**: Go 1.23 (module `lanweave`)

**Primary Dependencies**: `modernc.org/sqlite` (CGo-free SQLite), `pressly/goose`
(migrations — *none needed here*), `pelletier/go-toml/v2` (config), `fyne.io/fyne/v2`
(client UI + `lang` i18n). No new dependencies.

**Storage**: SQLite (single source of truth). Existing `nodes` and `zones` tables;
no schema change — caps are derived from row counts (`COUNT(*) … WHERE user_id`/
`owner_user_id`), not stored.

**Testing**: `go test ./...` run under `unshare -rUn` (Constitution II). Real SQLite,
no mocks. Server tests: `internal/server/config` (unit), `internal/server/store`
(integration, real DB), `internal/server/api` (integration, real router + real
store via the `newHarness` helper). Client tests: `internal/client/apiclient`
(httptest server), plus GUI presentation deferred to the manual quickstart matrix
per the Constitution II GUI/exec exemption.

**Target Platform**: Linux server (root) + Windows 10/11 Fyne client.

**Project Type**: Client/server (Go monorepo). Server under `internal/server/**`,
client under `internal/client/**`, shared wire types under `pkg/protocol`.

**Performance Goals**: Within existing budgets (Constitution IV). The conditional
insert adds one correlated `COUNT(*)` over a single user's rows (indexed by
`user_id`/`owner_user_id`); negligible against the ≤300 ms write budget.

**Constraints**: Single server instance (SQLite file lock). Config loaded once at
startup (no hot reload) — lowering a cap takes effect on next restart. No new
`os.Getenv` (Constitution I — config via the single TOML bridge).

**Scale/Scope**: Default cap 10; designed for thousands of nodes per server. Two
endpoints touched (`POST /nodes`, `POST /zones`); two store `Create` methods; one
config section; client error mapping + four locale strings.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Applies? | How this design honors it |
|-----------|----------|---------------------------|
| **I. Code Quality** | Yes | No new package; caps added to existing `config`, `store`, `api` packages, each keeping its single responsibility. No premature abstraction — two near-identical `Create` call sites get the same `maxN int` parameter (not a speculative "limiter" interface). Config stays in the single TOML file (`[limits]`), loaded once. `gofmt`/`vet`/`staticcheck` clean. Errors are values (`ErrDeviceLimitReached`, `ErrOwnedZoneLimitReached`). |
| **II. Testing (NON-NEGOTIABLE)** | Yes | Real SQLite, no mocks. Each user story gets ≥1 acceptance test: US1 device cap (api integration), US2 owned-zone cap incl. join-still-works (api integration), US3 config defaults/zero/negative (config unit) + admin exemption (api integration). Concurrency tests (SC-010) hammer `POST /nodes` **and** the owned-zone `Create` in parallel against a real store and assert the count never exceeds the cap (both share the same conditional-INSERT atomicity). Grandfathering test (SC-009) seeds over-cap rows then asserts create is refused but reads still work. |
| **III. UX Consistency** | Yes | Refusals reuse the established error-envelope → typed-error → localized-message pipeline (`wizard.errDeviceLimit`, `panel.errZoneLimit`) in both languages; no stack traces. Distinct codes so the user sees "you've reached your device limit" vs "…zone limit", never a generic failure (FR-011/FR-012). |
| **IV. Performance** | Yes | One indexed `COUNT(*)` folded into the insert; no extra round trip. Within write budget. |
| **Security & Ops** | Yes | Caps validated at the boundary at startup (negative → refuse to start, Validate joins all errors). No secrets involved. Single-instance assumption preserved (atomicity relies on SQLite's single-writer lock — explicitly noted, not a hidden multi-instance assumption). |
| **Workflow** | Yes | Spec-Kit flow followed; DESIGN.md §10.3 config example to be extended additively (no contradiction); ROADMAP slice 023 already added; tests-first per US. |

**Result**: PASS. No violations → Complexity Tracking table empty.

## Project Structure

### Documentation (this feature)

```text
specs/023-per-user-limits/
├── plan.md              # This file
├── research.md          # Phase 0 — atomicity decision, config three-state, admin exemption
├── data-model.md        # Phase 1 — derived allowances, config entity, no schema change
├── quickstart.md        # Phase 1 — operator + manual GUI verification matrix
├── contracts/
│   └── limits-api.md     # Phase 1 — changed error responses on POST /nodes and /zones
├── checklists/
│   └── requirements.md   # Spec quality checklist (already PASS)
└── tasks.md             # Phase 2 — /speckit-tasks (NOT created here)
```

### Source Code (repository root)

```text
internal/server/config/
├── config.go                 # ADD LimitsConfig + [limits] decode, applyDefaults (nil→10), Validate (reject <0)
└── config_test.go            # ADD unset→10, zero→unlimited, negative→error cases

internal/server/store/
├── nodes.go                  # CHANGE Create signature (+maxDevices int); conditional INSERT; ErrDeviceLimitReached
├── zones.go                  # CHANGE Create signature (+maxOwnedZones int); conditional INSERT; ErrOwnedZoneLimitReached
├── nodes_test.go             # ADD count cap, delete-frees-slot, unlimited(0), concurrency
└── zones_test.go             # ADD owner-count cap, delete-frees-slot, join-not-counted, unlimited(0)

internal/server/api/
├── router.go                 # ADD Options.MaxDevicesPerUser / MaxOwnedZonesPerUser (resolved ints); pass into handlers
├── auth_handlers.go          # ADD two int fields to handlers struct
├── node_handlers.go          # registerNode: effective = admin?0:cfg; map ErrDeviceLimitReached → 409 device_limit_reached
├── zone_handlers.go          # createZone: effective = admin?0:cfg; map ErrOwnedZoneLimitReached → 409 zone_limit_reached
├── node_handlers_test.go     # ADD device cap reached / admin bypass acceptance
└── zone_handlers_test.go     # ADD zone cap reached / join-still-works / admin bypass acceptance

internal/server/app/
└── app.go                    # Pass resolved cfg.Limits values into the api.NewRouter(api.Options{…}) call

internal/client/apiclient/
├── client.go                 # ADD ErrDeviceLimitReached / ErrOwnedZoneLimitReached; map new codes in mapError
└── client_test.go            # ADD code→error mapping cases

internal/client/ui/
├── wizard.go                 # device-setup error switch: ErrDeviceLimitReached → wizard.errDeviceLimit
└── panel.go                  # zone-create error switch: ErrOwnedZoneLimitReached → panel.errZoneLimit

internal/client/i18n/
├── zh-Hans.json              # ADD wizard.errDeviceLimit, panel.errZoneLimit
└── en.json                   # ADD same two keys

DESIGN.md                     # §10.3 config example: add [limits] block (additive)
```

**Structure Decision**: Existing client/server monorepo layout — no new packages or
directories. The feature threads a resolved integer cap from `config` → `api.Options`
→ `handlers` → `store.*.Create`, and a new error code back out to the client's
typed-error mapping. `api.Options` carries **already-resolved plain ints** (not the
config pointer type) so the test harness's zero-value Options reads as "unlimited"
and existing handler tests are unaffected.

## Complexity Tracking

> No Constitution violations. Table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
