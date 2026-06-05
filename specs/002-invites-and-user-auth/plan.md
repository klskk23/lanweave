# Implementation Plan: Invites and User Auth

**Branch**: `002-invites-and-user-auth` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/002-invites-and-user-auth/spec.md`

## Summary

Layer authentication and invite-gated onboarding onto the feature-001 foundation:
a public login that issues a short-lived HS256 JWT, JWT verification middleware
that establishes caller identity for protected routes, a protected `/me`, admin-only
invite generation and listing, and public invite-gated registration that atomically
creates a non-admin account and consumes a one-time code. All of it reuses the
existing config (`jwt_secret`, `jwt_ttl`), argon2id hashing, structured logging
with secret redaction, the global rate limiter, and the shared JSON error envelope.

## Technical Context

**Language/Version**: Go 1.23+ (existing module `lanweave`)

**Primary Dependencies** (new vs inherited):
- **NEW** `github.com/golang-jwt/jwt/v5` — vetted JWT implementation; pins HS256 on verify to avoid algorithm-confusion attacks.
- Inherited from 001: `modernc.org/sqlite`, `github.com/pressly/goose/v3`, `golang.org/x/crypto/argon2`, `golang.org/x/time/rate`, `github.com/pelletier/go-toml/v2`, stdlib `log/slog`, `net/http`, `database/sql`.

**Storage**: SQLite. Adds an `invites` table via migration `0002_invites.sql`. Reuses the `users` table (registration inserts non-admin rows).

**Testing**: Go stdlib `testing`. Unit: JWT issue/verify (incl. tampered/expired/wrong-alg), registration input validation. Integration (**real** temp SQLite): invite create/list, the register transaction, and the one-time race (concurrent redemptions → exactly one success). Acceptance (built handler over real HTTPS / httptest): full invite→register→login→`/me` flow and the authz matrix (401/403).

**Target Platform**: Linux server (Debian 12 / Ubuntu 22.04+), x86-64.

**Project Type**: Single Go project — extends the existing server packages.

**Performance Goals** (constitution IV): login ≤ 300 ms P50 (one argon2id verify); `/me` ≤ 100 ms P50 (no DB read — identity comes from the verified token, FR-007); invite create/list ≤ 100 ms.

**Constraints**:
- One invite code is redeemable at most once, even under concurrent registration (FR-018).
- No user enumeration: wrong-password and unknown-user logins are identical in status and body (FR-002), including comparable timing.
- No secret (password, token, invite code) in any log line (FR-020).
- Stateless tokens; no server-side session store or revocation (FR-004).

**Scale/Scope**: Single instance. This feature: 1 new table, 1 new dependency, 5 new endpoints, ~5 new source files plus extensions to `store`, `api`, `app`, and `pkg/protocol`.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Applies? | How this plan honors it |
|-----------|----------|-------------------------|
| **I. Code Quality** | Yes | Cohesive placement: JWT in `auth/jwt.go`, invites in `store/invites.go`, the cross-table register transaction in `store` (it owns `*sql.DB`), HTTP handlers in `api/`. No premature abstraction — no generic "auth provider" interface for a single token type. Errors are typed values (`ErrInviteInvalid`, `ErrUserExists`, `ErrInvalidCredentials`). SQLite stays the single source of truth; tokens are derived, not stored. |
| **II. Testing Standards (NON-NEGOTIABLE)** | Yes | Each of US1–US4 gets an acceptance test. SQLite is **not** mocked: the register transaction and the one-time race run on a real temp DB (SC-002). JWT verify is unit-tested against tampered/expired/wrong-alg tokens (SC-004). A log-scan test proves no secret leaks (SC-006). |
| **III. UX Consistency** | **N/A** | Server-only feature; no human UI (constitution III targets the Windows client). The machine-facing surface — JSON error envelope and stable error codes (`unauthorized`, `forbidden`, `invalid_credentials`, `invite_invalid`, `username_taken`, `validation_error`) — is kept uniform so the future client parses one shape. Recorded N/A with that note. |
| **IV. Performance Requirements** | Yes | `/me` performs zero DB reads (identity from token, FR-007) → well under the 100 ms read budget. Login is one argon2id verify, inside the 300 ms write budget. Budgets are smoke-checked in quickstart. |
| **Security & Operational Discipline** | Yes | argon2id reused (no new primitive). JWT pins HS256 on verify (rejects `alg:none` / RS/HS confusion). No user enumeration (dummy verify on unknown user equalizes timing; identical response). Request bodies are size-capped at the boundary. Secrets redacted in logs (token, password, invite code never logged). Invite/`jwt_secret` handled as secrets. |

**Result**: PASS. No violations → Complexity Tracking table empty.

## Project Structure

### Documentation (this feature)

```text
specs/002-invites-and-user-auth/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── auth.md          # /login, /register, /me
│   ├── invites.md       # /admin/invites (create, list)
│   └── token.md         # JWT claims + verification rules
├── checklists/
│   └── requirements.md  # From /speckit-specify
└── tasks.md             # /speckit-tasks output (NOT created here)
```

### Source Code (repository root) — new and changed

```text
internal/server/
├── auth/
│   ├── jwt.go            # NEW: JWTManager (Issue/Verify), Claims, HS256-pinned
│   ├── jwt_test.go       # NEW: issue/verify, tampered, expired, wrong-alg
│   └── password.go       # (reused; add a DummyVerify helper for timing equalization)
├── store/
│   ├── migrations/
│   │   └── 0002_invites.sql   # NEW: invites table (FKs ON DELETE SET NULL)
│   ├── invites.go        # NEW: InviteRepo (Create, List)
│   ├── register.go       # NEW: Store.Register — tx: insert user + consume code (one-time)
│   ├── users.go          # (reused; GetByUsername already present)
│   ├── invites_test.go   # NEW: integration — create/list, dangling creator
│   └── register_test.go  # NEW: integration — happy path, used/invalid code, username race, one-time race
├── api/
│   ├── auth_handlers.go  # NEW: login, register, me
│   ├── invite_handlers.go# NEW: create invite, list invites
│   ├── middleware_auth.go# NEW: AuthRequired, AdminRequired, identity-in-context
│   ├── router.go         # CHANGED: register new routes; Options gains deps
│   ├── handlers_test.go  # NEW: acceptance — full flow + authz matrix + no-secret logs
│   └── middleware_auth_test.go # NEW: unit — 401/403 paths, context identity
├── app/
│   └── app.go            # CHANGED: build JWTManager + repos, pass into api.NewRouter
└── config/
    └── config.go         # (reused; jwt_secret/jwt_ttl already validated)

pkg/protocol/
└── auth.go               # NEW: LoginRequest/Response, RegisterRequest, MeResponse,
                          #      CreateInviteResponse, InviteListItem
```

**Structure Decision**: Continue the single-project layout from 001. The register
flow spans two tables (`users` + `invites`) and must be atomic, so it lives as
`Store.Register` at the data layer that owns the `*sql.DB` and can open a
transaction — rather than being orchestrated in a handler with two repo calls
(which could not be made atomic). JWT lives in the existing `auth` package next to
password hashing, since both are credential concerns. New DTOs go in the shared
`pkg/protocol` so the future Windows client reuses them.

## Complexity Tracking

> No constitution violations. Table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
