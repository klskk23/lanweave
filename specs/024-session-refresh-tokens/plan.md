# Implementation Plan: session-refresh-tokens

**Branch**: `024-session-refresh-tokens` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/024-session-refresh-tokens/spec.md`

## Summary

Add a server-issued, revocable **refresh token (RT)** alongside the existing short-lived
(2h) access JWT so the Fyne client stops prompting for a password every ~2 hours. Login now
returns `{token, refresh_token}`; the access JWT stays stateless and unchanged. RTs are
opaque 32-byte `crypto/rand` values returned to the client once and stored server-side only
as a SHA-256 hash in a new `refresh_tokens` table (FK to `users` with `ON DELETE CASCADE`).
A new public `POST /api/v1/refresh` exchanges a valid RT for a fresh access token and slides
the RT's 30-day expiry; a new `POST /api/v1/logout` idempotently revokes one RT (consumed by
slice 025). The client persists the RT in the OS keyring next to the session token (same
persistence point as the 019 fix) and refreshes **lazily**: an authenticated call that gets a
401 (`ErrSessionExpired`) triggers a silent `POST /refresh`, rewrites the keyring, and retries
the original request once; if refresh fails the session is cleared and the user falls back to
password sign-in. No rotation, no replay detection, no proactive timer. DESIGN.md §4/§7.3/§8/
§11 are amended in the same PR (Constitution: DESIGN.md authority).

## Technical Context

**Language/Version**: Go 1.23 (module `lanweave`)

**Primary Dependencies**: `modernc.org/sqlite` (CGo-free SQLite), `pressly/goose/v3`
(migrations — **one new migration** `0005_refresh_tokens.sql`), `golang-jwt/jwt/v5` (access
token, unchanged), `crypto/rand` + `crypto/sha256` (stdlib, RT generation/hashing),
`fyne.io/fyne/v2` (client). No new third-party dependencies.

**Storage**: SQLite (single source of truth). New table `refresh_tokens` (id, user_id FK
`ON DELETE CASCADE`, token_hash UNIQUE, expires_at, revoked_at nullable, created_at). Access
JWT remains stateless (not stored).

**Testing**: `go test ./...` under `unshare -rUn` (Constitution II), real SQLite, no mocks.
Server: `internal/server/store` (RT repo integration — issue/validate/slide/revoke/cascade),
`internal/server/api` (login returns RT; `/refresh` valid/expired/revoked/unknown; `/logout`
idempotent; cascade-on-delete-user) via the existing `newHarness` helper. Client:
`internal/client/apiclient` (httptest server — 401→silent refresh→retry; refresh-fails path)
and `internal/client/panel` / `onboard` (fake api boundary — RT persisted/cleared). Time-based
expiry is tested via an injectable clock in the store (not wall-clock sleeps).

**Target Platform**: Linux server (root) + Windows 10/11 Fyne client.

**Project Type**: Client/server (Go monorepo). Server under `internal/server/**`, client under
`internal/client/**`, shared wire types under `pkg/protocol`.

**Performance Goals**: Within existing budgets (Constitution IV). `/refresh` is one indexed
lookup by `token_hash` + one `UPDATE` (slide); negligible against the ≤300 ms write budget.
Lazy refresh adds at most one extra round trip only on the rare expiry boundary, not per call.

**Constraints**: Single server instance (SQLite file lock). Config loaded once at startup; the
30-day RT lifetime is a fixed constant this slice (not configurable). No new `os.Getenv`
(Constitution I). Access JWT TTL stays `2h` (`jwt_ttl`) — unchanged by this feature.

**Scale/Scope**: One new table + one migration; one new store repo (`RefreshTokenRepo`); login
response gains one field; two new public endpoints (`/refresh`, `/logout`); client keyring gains
one name (`RefreshTokenName`), `apiclient` gains lazy-refresh in `do()` + `Refresh`/`Logout`
methods, `onboard`/`panel` persist+clear the RT. Designed for thousands of device sessions.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Applies? | How this design honors it |
|-----------|----------|---------------------------|
| **I. Code Quality** | Yes | One new file `store/refresh_tokens.go` with a single responsibility (the `refresh_tokens` repo: Issue/Validate/Revoke), mirroring the existing `NodeRepo`/`ZoneRepo` shape. No speculative abstraction — `apiclient` gets a concrete `Refresh`/`Logout` method and a single retry path in `do()`, not a pluggable "token provider". SQLite stays the single source of truth (RT validity is a DB query, never in-memory). No new config knob, no scattered `os.Getenv`. Errors are values (`ErrRefreshInvalid` in store, reuse `ErrSessionExpired`/new `ErrRefreshFailed` in client). Comments explain WHY (e.g. why refresh is RT-authed, not JWT-authed). |
| **II. Testing (NON-NEGOTIABLE)** | Yes | Real SQLite, no mocks. Each user story ≥1 acceptance test: **US1** (api: `/login` returns RT; apiclient: 401→silent `/refresh`→original call retried green); **US2** (api: `/logout` revokes then `/refresh` 401; idempotent re-logout; store: delete-user cascades RT rows away); **US3** (store: validate slides `expires_at` +30d; expired RT → invalid, via injected clock). RT secrecy asserted: store never returns the plaintext on read, logs/fixtures never contain it (Security gate test). Time tested through an injectable `now func() time.Time`, not sleeps (no flake). |
| **III. UX Consistency** | Yes | The win is *fewer* interruptions: the password dialog stops appearing on the 2h boundary (FR-006/FR-007). When refresh genuinely fails the user still sees the existing human-readable sign-in prompt, not a stack trace. No new always-visible UI; status indicators unchanged. Silent background refresh shows nothing because it is sub-second and the in-flight operation already shows its own feedback. |
| **IV. Performance** | Yes | `/refresh`: one indexed `SELECT … WHERE token_hash = ?` + one `UPDATE … SET expires_at`. Within the ≤300 ms write budget. Lazy (not proactive) refresh means zero added latency on the common path; one extra round trip only at the expiry boundary. Server memory unaffected (RTs live in SQLite, not RAM). |
| **Security & Ops** | Yes | RT is `crypto/rand` 32 bytes (high entropy), stored only as SHA-256 — DB compromise does not yield usable tokens; plaintext shown to client once, never logged (Constitution "secrets in logs" — asserted by test). `/refresh` and `/logout` validate body size/shape at the boundary (`decodeJSON`). No new crypto primitive (stdlib SHA-256 for a high-entropy random token — not a password, so a fast hash is correct; argon2id is for the low-entropy `users.password_hash` and is untouched — the Constitution's "Crypto choices" clause was clarified to v1.0.1 to make this scope explicit: argon2id governs low-entropy passwords, high-entropy `crypto/rand` tokens may use SHA-256). New accepted risks recorded in `DESIGN.md §11` (the only place allowed): "restart no longer logs everyone out" and "client keeps one long-lived RT locally (DPAPI-protected)". Single-instance assumption preserved (RT validity = single SQLite writer). |
| **Workflow** | Yes | Spec-Kit flow followed. DESIGN.md amended in the same PR (§4 table adds `refresh_tokens`; §7.3 splits stateless-access vs stateful-RT; §8.1 `/login` response gains `refresh_token`, adds `/refresh` + `/logout`; §11 risk register updated) — refines, contradicts where it must (the old "restart = everyone kicked" line), and updates DESIGN.md in the same change as required. ROADMAP slice 024 already added. Tests-first per user story. |

**Result**: PASS. No unjustified violations → Complexity Tracking table empty.

## Project Structure

### Documentation (this feature)

```text
specs/024-session-refresh-tokens/
├── plan.md              # This file
├── research.md          # Phase 0 — RT form, storage, TTL/sliding, lazy-refresh, endpoint auth, DESIGN deltas
├── data-model.md        # Phase 1 — refresh_tokens table, RT lifecycle states, response/keyring entities
├── quickstart.md        # Phase 1 — operator curl walkthrough + manual GUI "no more password prompt" matrix
├── contracts/
│   └── refresh-api.md    # Phase 1 — /login (changed), POST /refresh (new), POST /logout (new)
├── checklists/
│   └── requirements.md   # Spec quality checklist (already PASS)
└── tasks.md             # Phase 2 — /speckit-tasks (NOT created here)
```

### Source Code (repository root)

```text
internal/server/store/
├── migrations/0005_refresh_tokens.sql   # NEW: refresh_tokens table (FK user_id ON DELETE CASCADE, token_hash UNIQUE idx)
├── refresh_tokens.go                    # NEW: RefreshTokenRepo — Issue(userID)→plaintext, Validate(rt)→userID+slide, Revoke(rt); ErrRefreshInvalid; injectable now()
└── refresh_tokens_test.go               # NEW: issue+validate, slide +30d, expired→invalid, revoked→invalid, unknown→invalid, delete-user cascade

internal/server/auth/
└── refreshtoken.go                      # NEW (optional): GenerateRefreshToken() (crypto/rand 32B → base64url) + HashRefreshToken() (sha256 hex); keeps token crypto in the auth pkg next to jwt.go

pkg/protocol/
└── auth.go                              # CHANGE: LoginResponse +RefreshToken; ADD RefreshRequest{refresh_token}, RefreshResponse{token}, LogoutRequest{refresh_token}

internal/server/api/
├── auth_handlers.go                     # CHANGE: login issues+returns RT; ADD refresh handler (RT-authed, issues access token, slides RT); ADD logout handler (idempotent revoke → 204)
├── router.go                            # ADD public routes: POST /api/v1/refresh, POST /api/v1/logout (NO AuthRequired — RT-authed in body)
└── auth_handlers_test.go                # ADD: login returns RT; refresh valid→new token; expired/revoked/unknown→401; logout idempotent 204; cascade-on-delete-user

internal/client/keyring/
└── store.go                             # ADD: RefreshTokenName = "lanweave-refresh-token"

internal/client/apiclient/
├── client.go                            # ADD: refreshToken field + SetRefreshToken/RefreshToken accessors; Login decodes RT; ADD Refresh()/Logout(); do(): on authed 401 try one silent Refresh + retry; ADD ErrRefreshFailed
└── client_test.go                       # ADD: 401→silent refresh→retry success; refresh fails→surfaces ErrSessionExpired; logout posts RT

internal/client/onboard/
├── onboard.go                           # CHANGE: apiClient iface +RefreshToken()/SetRefreshToken(); Provision persists RefreshTokenName; Cleanup deletes it
└── onboard_test.go                      # ADD: RT persisted after Provision; removed after Cleanup

internal/client/panel/
├── panel.go                             # CHANGE: api iface +RefreshToken()/SetRefreshToken(); LoadSession restores RT into client; SignIn persists RT; Logout deletes RefreshTokenName (and calls api.Logout best-effort to revoke server-side)
└── panel_test.go                        # ADD: LoadSession seeds RT; SignIn caches RT; Logout clears RT

DESIGN.md                                # §4.1 add refresh_tokens row; §7.3 stateless-access + stateful-RT; §8.1 /login response +refresh_token, add /refresh + /logout; §11 risk register update
```

**Structure Decision**: Existing client/server monorepo layout — one new migration and one new
store file on the server, no new package. The server threads the RT through the same `handlers`
struct (no new dependency wiring beyond `h.store.RefreshTokens()`). On the client, the RT lives
beside the existing session token: same keyring store, same persistence points (`onboard.Provision`
last-step save and `panel.SignIn`/`LoadSession`), and the lazy-refresh retry is folded into the
single existing `apiclient.do()` chokepoint so every authenticated call inherits it without per-call
changes. `/refresh` and `/logout` are deliberately **public** routes (no `AuthRequired`): they
authenticate via the RT in the body precisely because the access JWT may be expired at that moment.

## Complexity Tracking

> No Constitution violations. Table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
