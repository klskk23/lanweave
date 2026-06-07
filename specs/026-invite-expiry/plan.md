# Implementation Plan: Invite Code Expiry

**Branch**: `026-invite-expiry` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/026-invite-expiry/spec.md`

## Summary

Add an optional expiry to admin-issued invite codes. A single global config value
`invite_ttl` (a duration string, default `"24h"`) controls the window; at code
generation the server stamps `expires_at = created_at + invite_ttl`. At
registration, an invite whose `expires_at` has passed is rejected and folded into
the existing generic "invite invalid" error — indistinguishable from unknown or
already-used codes. `invite_ttl` of `0`/empty, and any pre-existing code (NULL
`expires_at`), means never-expire, so upgrades do not retroactively invalidate
issued codes. The admin code-generation surface (`lanweavectl invite`) reports the
expiry moment, or "never". No new listing command, no background cleanup.

## Technical Context

**Language/Version**: Go 1.23 (server). Client (Fyne) is untouched by this slice.

**Primary Dependencies**: `modernc.org/sqlite` (pure-Go driver), `github.com/pressly/goose/v3` (embedded SQL migrations), TOML config loader, standard `net/http`.

**Storage**: SQLite, single file, single source of truth. Schema changes ship as a goose migration embedded via `//go:embed migrations/*.sql`.

**Testing**: Go integration tests against real SQLite (Constitution II — no mocking SQLite/nftables/WireGuard); namespace-isolated suites run under `unshare -rUn`. Expiry is tested deterministically by inserting invite rows with `expires_at` in the past — no wall-clock sleeps.

**Target Platform**: Linux server (single host).

**Project Type**: Web service (HTTP API) + SQLite, with a shell admin helper (`lanweavectl`).

**Performance Goals**: Not performance-sensitive. Expiry check is one extra predicate on the existing single-row `UPDATE` at registration; no new query.

**Constraints**: Invite codes MUST NOT appear in logs. The registration rejection MUST NOT disclose *why* a code is invalid (unknown vs. used vs. expired all return one generic error). Config is read once at startup; no hot reload.

**Scale/Scope**: Single small deployment, admin-issued one-time codes; the change touches config, one migration, the store invite-create and register paths, the admin invite HTTP handler/response, the protocol structs, the `lanweavectl` helper, and the example config. DESIGN.md §7.1 and §4 are amended in the same PR.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Constitution v1.0.1. Applicable principles:

- **I — Simplicity / config loaded once / SQLite single source of truth / no premature abstraction**: PASS. Reuses the existing TOML-once-at-startup config pattern (`AuthConfig`), the existing `invites` table, and the existing register transaction. The expiry is one nullable column and one extra `WHERE` predicate — no new abstraction, no new query, no clock interface injected. Deliberately *no* code-level default for `invite_ttl`: empty/absent → NULL → never-expire; the 24h lives only in `config.toml.example`. This differs from the Limits `*int` default-to-10 pattern, justified in research.md (makes "0/空=永不过期" literally true and keeps upgrades non-surprising).
- **II — Testing (NON-NEGOTIABLE)**: PASS. No mocking of SQLite. Expiry verified via real DB rows with past-dated `expires_at`; the unexpired path uses a far-future expiry. No wall-clock sleeps. Suites run under `unshare -rUn` like the rest.
- **IV — security posture**: PASS. Codes stay out of logs (unchanged). Generic rejection: expired folds into `ErrInviteInvalid` → HTTP 422 `invite_invalid`, identical to unknown/used — no oracle. New accepted risks: none, so DESIGN.md §11 is untouched; DESIGN.md §7.1/§4 (invite model + data table) are amended to record that invites now carry an optional expiry.

No violations → Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/026-invite-expiry/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── admin-create-invite.md
│   ├── register.md
│   └── store-invite-expiry.md
├── spec.md              # /speckit-specify output
└── checklists/
    └── requirements.md
```

### Source Code (repository root)

```text
internal/server/
├── config/
│   └── config.go                       # add AuthConfig.InviteTTL (toml "invite_ttl"); Validate parses non-empty duration, rejects negative; NO applyDefaults fallback
├── store/
│   ├── migrations/
│   │   └── 0006_invite_expires.sql     # NEW: ALTER TABLE invites ADD COLUMN expires_at TEXT
│   ├── invites.go                      # Create(ctx, createdBy, ttl) → (code, expiresAt *time.Time, err): stamp expires_at = now+ttl, NULL when ttl<=0
│   └── register.go                     # extend UPDATE ... WHERE to AND (expires_at IS NULL OR expires_at > ?); RowsAffected!=1 → ErrInviteInvalid
├── api/
│   ├── router.go                       # Options gains InviteTTL time.Duration
│   ├── invite_handlers.go              # thread ttl into Create; put expiry in CreateInviteResponse; toInviteListItem adds "expired" status + expires_at
│   └── auth_handlers.go                # handlers carry inviteTTL; register mapping unchanged (expired already folds into ErrInviteInvalid → 422 invite_invalid)
└── app/
    └── app.go                          # parse invite_ttl (empty → 0) → Options.InviteTTL

pkg/protocol/
└── auth.go                             # CreateInviteResponse + InviteListItem gain ExpiresAt *string json:"expires_at,omitempty"

packaging/scripts/
└── lanweavectl.sh                      # cmd_invite parses .expires_at; prints "Expires: <ts>" or "Expires: never"

config.toml.example                     # [auth] gains invite_ttl = "24h" (开箱启用)

DESIGN.md                               # §7.1 invite model + §4 data table amended to record optional expiry (same PR)
```

**Structure Decision**: Existing single-service Go layout (`internal/server/{config,store,api,app}` + shared `pkg/protocol` + `packaging/scripts` + root example config). No new packages or directories; the feature is a thin additive change across the established invite path.

## Complexity Tracking

> No Constitution Check violations — section intentionally empty.
