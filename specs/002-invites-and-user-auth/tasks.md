---
description: "Task list for 002-invites-and-user-auth"
---

# Tasks: Invites and User Auth

**Input**: Design documents from `/specs/002-invites-and-user-auth/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present). Builds on feature 001 (already merged on this branch).

**Tests**: REQUIRED per constitution Principle II. Each user story has acceptance tests; the register transaction and one-time race run against a real temp SQLite (no mocks). Test tasks are written FIRST and must FAIL before their implementation tasks.

**Organization**: Tasks grouped by user story. US1+US2+US3 are P1 (the MVP onboarding loop); US4 (P2) hardens it.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4; Setup/Foundational/Polish carry no story label

## Path Conventions

Single Go project `lanweave`. Paths are exact and match plan.md's Structure Decision. Files marked CHANGED extend feature-001 code.

---

## Phase 1: Setup

- [ ] T001 Add dependency `github.com/golang-jwt/jwt/v5` and run `go mod tidy`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, DTOs, JWT, auth middleware, and the wiring every story needs.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [ ] T002 Create migration `internal/server/store/migrations/0002_invites.sql` (invites table per data-model.md; FKs `ON DELETE SET NULL`). The existing `//go:embed migrations/*.sql` already includes it.
- [ ] T003 [P] Define DTOs in `pkg/protocol/auth.go`: `LoginRequest`, `LoginResponse`, `RegisterRequest`, `RegisterResponse`, `MeResponse`, `CreateInviteResponse`, `InviteListItem`
- [ ] T004 [P] Add `_txlock=immediate` to the SQLite DSN in `internal/server/store/store.go` so registration transactions take the write lock on `BeginTx` (research.md R2)
- [ ] T005 [P] Write JWT unit test `internal/server/auth/jwt_test.go`: issue→verify round-trip; tampered signature, expired `exp`, and `alg`≠HS256 (incl. `none`) all rejected; a token signed with secret A fails under a manager with secret B (US4-4) — MUST FAIL before T006
- [ ] T006 Implement `JWTManager` (`Issue(Claims)`, `Verify(token)`, HS256-pinned via `WithValidMethods`) and `Claims` in `internal/server/auth/jwt.go` (research.md R1)
- [ ] T007 [P] Add `DummyVerify(password string)` (verify against a fixed PHC constant, discard result) in `internal/server/auth/password.go` with a unit test in `internal/server/auth/password_test.go` (research.md R3)
- [ ] T008 [P] Add a request-decode helper (`http.MaxBytesReader` ~16 KiB + `json.Decoder` with `DisallowUnknownFields`) in `internal/server/api/decode.go` (research.md R7)
- [ ] T009 Write auth-middleware unit test `internal/server/api/middleware_auth_test.go`: missing/malformed/expired token → 401; valid non-admin token → `AdminRequired` 403; valid token populates identity in context — MUST FAIL before T010
- [ ] T010 Implement `AuthRequired`, `AdminRequired`, the typed context key, and `IdentityFrom(ctx)` in `internal/server/api/middleware_auth.go` (research.md R4)
- [ ] T011 Expand `api.Options` (add `Store *store.Store`, `JWT *auth.JWTManager`) and thread them through `api.NewRouter` in `internal/server/api/router.go`
- [ ] T012 Wire dependencies in `internal/server/app/app.go`: parse `jwt_ttl`, build `JWTManager` from `cfg.Auth`, pass `store` + `JWT` into `api.NewRouter`

**Checkpoint**: builds; migration applies; JWT + auth middleware unit-tested; no new endpoints yet.

---

## Phase 3: User Story 1 — 登录并访问受保护资源 (Priority: P1) 🎯 MVP

**Goal**: Existing user logs in for a token and reads their identity from `/me`; bad/absent tokens are refused; login does not leak account existence.

**Independent Test**: With only the bootstrap admin, login → token; `/me` with token → 200 identity; `/me` with no/garbage token → 401; wrong-password and unknown-user logins return identical 401.

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

- [ ] T013 [P] [US1] Acceptance test in `internal/server/api/auth_handlers_test.go`: login(admin)→200+token; `/me`+token→200 with correct `user_id`/`username`/`is_admin`; `/me` with absent and garbage token→401; wrong-password vs unknown-user login both→401 with identical body (SC-005) — MUST FAIL

### Implementation for User Story 1

- [ ] T014 [US1] Implement the login handler in `internal/server/api/auth_handlers.go`: decode, `GetByUsername`, `VerifyPassword` (or `DummyVerify` on unknown user), issue JWT on success, `invalid_credentials` (401) otherwise — never log the password/token
- [ ] T015 [US1] Implement the `/me` handler in `internal/server/api/auth_handlers.go`: read identity from context (no DB), return `MeResponse`
- [ ] T016 [US1] Register routes `POST /api/v1/login` (public) and `GET /api/v1/me` (wrapped in `AuthRequired`) in `internal/server/api/router.go`

**Checkpoint**: US1 independently functional. T013 green.

---

## Phase 4: User Story 2 — 管理员签发与查看邀请码 (Priority: P1) 🎯 MVP

**Goal**: Admin generates and lists one-time codes; non-admins and anonymous callers are refused.

**Independent Test**: As admin (US1 token), create a code → 201; list → it appears unused; as a non-admin token → 403; with no token → 401.

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

- [ ] T017 [P] [US2] Integration test `internal/server/store/invites_test.go` (real temp DB): `Create` returns a unique unguessable code; `List` returns newest-first with status; a code whose creator row is deleted still lists with `created_by` = nil — MUST FAIL
- [ ] T018 [P] [US2] Acceptance test in `internal/server/api/invite_handlers_test.go`: admin creates code (201) and lists it (unused); non-admin token → 403; no token → 401 — MUST FAIL

### Implementation for User Story 2

- [ ] T019 [US2] Implement `InviteRepo` in `internal/server/store/invites.go`: `Create(ctx, createdByUserID)` (160-bit `crypto/rand` base64url code, unique-retry) and `List(ctx)` (newest-first, LEFT JOIN usernames) per data-model.md
- [ ] T020 [US2] Implement invite handlers (create, list) in `internal/server/api/invite_handlers.go` mapping to `CreateInviteResponse` / `InviteListItem`; never log the code value
- [ ] T021 [US2] Register routes `POST /api/v1/admin/invites` and `GET /api/v1/admin/invites` (wrapped in `AuthRequired` + `AdminRequired`) in `internal/server/api/router.go`

**Checkpoint**: US1 + US2 functional. Admin can mint and review codes.

---

## Phase 5: User Story 3 — 受邀者凭一次性码注册 (Priority: P1) 🎯 MVP

**Goal**: A new person registers with a valid unused code, creating a non-admin account and consuming the code; bad/used codes and taken usernames are refused; no open registration.

**Independent Test**: Mint a code (US2), register with it → 201 non-admin; the new user logs in (US1) and is not admin; reuse the code → 422; bad code → 422; taken username (fresh code) → 409 with code left unused; missing code → 400.

### Tests for User Story 3 (REQUIRED per constitution Principle II) ⚠️

- [ ] T022 [P] [US3] Integration test `internal/server/store/register_test.go` (real temp DB): happy path creates user + marks code used; already-used code → `ErrInviteInvalid`, no user; nonexistent code → `ErrInviteInvalid`; taken username → `ErrUserExists` and the code remains unused — MUST FAIL
- [ ] T023 [P] [US3] Acceptance test in `internal/server/api/auth_handlers_test.go`: register(valid code)→201 `{is_admin:false}`; subsequent login works; reuse→422; bad code→422; taken username→409; missing code→400 — MUST FAIL

### Implementation for User Story 3

- [ ] T024 [US3] Implement `Store.Register(ctx, username, passwordHash, code)` in `internal/server/store/register.go`: one transaction — verify code unused, insert non-admin user, `UPDATE invites SET used_by_user_id, used_at WHERE code=? AND used_at IS NULL` requiring `RowsAffected()==1`; typed `ErrInviteInvalid` / `ErrUserExists`; roll back on any failure (research.md R2)
- [ ] T025 [US3] Implement the register handler in `internal/server/api/auth_handlers.go`: validate username (≤64, non-empty) and password (≥8), `HashPassword`, call `Store.Register`, map errors → 201/400/409/422 per contracts/auth.md; never log the password
- [ ] T026 [US3] Register route `POST /api/v1/register` (public) in `internal/server/api/router.go`

**Checkpoint**: Full onboarding loop works: admin mints → invitee registers → invitee logs in.

---

## Phase 6: User Story 4 — 安全边界在滥用下成立 (Priority: P2)

**Goal**: One-time guarantee holds under concurrency; no secret reaches logs; rate limiting and secret-rotation invalidation behave as specified.

**Independent Test**: Concurrent registrations with one code → exactly one 201; log scan over a register+login cycle finds no password/token/code; (rate limiting inherited from 001).

### Tests for User Story 4 (REQUIRED per constitution Principle II) ⚠️

- [ ] T027 [P] [US4] Concurrency test in `internal/server/store/register_test.go`: launch N goroutines calling `Store.Register` with ONE code and distinct usernames → exactly 1 succeeds, N-1 get `ErrInviteInvalid`, and exactly 1 user row exists (SC-002) — MUST FAIL
- [ ] T028 [P] [US4] No-secret-log test in `internal/server/api/auth_handlers_test.go`: run register+login with a captured slog buffer; assert the plaintext password, the issued token, and the invite code value never appear (SC-006) — MUST FAIL

### Implementation for User Story 4

- [ ] T029 [US4] Resolve any failures surfaced by T027/T028: confirm the `BeginTx` + `_txlock=immediate` path makes the race deterministic, and confirm handlers/middleware pass secrets only through redacted/never-logged paths
- [ ] T030 [US4] Confirm secret-rotation invalidation (US4-4) is covered by the JWT verify test (T005) and that all new endpoints inherit the global rate limiter (FR-021, US4-3) — add an explicit assertion if any gap remains

**Checkpoint**: All four stories functional; adversarial guarantees proven.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T031 [P] Update `config.toml.example` comments to note `jwt_secret`/`jwt_ttl` are now actively consumed by login/auth
- [ ] T032 Run `make lint` (gofmt + `go vet` + staticcheck) clean; confirm ≥70% coverage on new code (`auth`, `api`, `store` additions) and document any gaps
- [ ] T033 Execute `quickstart.md` end-to-end against the running server: verify US1–US4 plus SC-002 (one-time), SC-004 (token rejection matrix), SC-005 (no enumeration), SC-006 (no secret logs)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: depends on Setup — **blocks all user stories**. Within it: T006 before T009/T010 (middleware needs JWT); T011 before/with T012; T002/T003/T004 independent.
- **US1 (Phase 3)**: depends on Foundational. Introduces `/login` + `/me`.
- **US2 (Phase 4)**: depends on Foundational; uses a US1 token in its acceptance test but the code paths are independent — can be built right after Foundational.
- **US3 (Phase 5)**: depends on Foundational; its acceptance test reuses US1 (login) and US2 (mint a code), so demonstrate after US1+US2. The store transaction (T024) is independent of US1/US2 code.
- **US4 (Phase 6)**: depends on US3 (the register transaction) being in place.
- **Polish (Phase 7)**: after the targeted stories.

### Critical Path

```
Setup → Foundational → US1 ─┐
                       US2 ─┼─→ US3 ─→ US4 ─→ Polish
                            ┘
```

### Within Each User Story

- Test tasks (⚠️) written first and MUST FAIL before implementation.
- In US2/US3: store repo/transaction before handlers; handlers before route registration.

---

## Parallel Opportunities

- **Foundational**: T003 [P] (DTOs), T004 [P] (store DSN), T005 [P] (jwt test), T007 [P] (dummy verify), T008 [P] (decode helper) are independent files. T006/T009/T010/T011/T012 then sequence.
- **US2**: T017 [P] (store test) and T018 [P] (acceptance test) in parallel.
- **US3**: T022 [P] (store test) and T023 [P] (acceptance test) in parallel.
- **US4**: T027 [P] and T028 [P] in parallel.

### Parallel Example: Foundational independent files

```bash
Task T003: DTOs in pkg/protocol/auth.go
Task T004: _txlock=immediate in internal/server/store/store.go
Task T005: JWT unit test in internal/server/auth/jwt_test.go
Task T007: DummyVerify + test in internal/server/auth/password*.go
Task T008: decode helper in internal/server/api/decode.go
```

---

## Implementation Strategy

### MVP (US1 + US2 + US3)

1. Setup → Foundational (JWT, auth middleware, schema, wiring).
2. US1 → login + `/me` (auth backbone).
3. US2 → admin invite mint + list.
4. US3 → invite-gated registration.
5. **STOP & VALIDATE**: a working invite→register→login→identify loop — the onboarding MVP every later feature needs.

### Incremental Delivery

- Add US4 → prove one-time-under-race, no-secret-logs, rotation invalidation.
- Each increment is independently testable and adds no regression to prior stories.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- Reuses feature 001: `users` table, argon2id (`HashPassword`/`VerifyPassword`), config (`jwt_secret`/`jwt_ttl`), structured logging + `Secret` redaction, global rate limiter, shared error envelope.
- SQLite exercised for real in T017/T022/T027 — no mocking of the system boundary (constitution Principle II).
- Verify each ⚠️ test fails before implementing.
- Commit after each task or logical group; check feature 002 off in `ROADMAP.md` at merge.
- Out of scope (later features): WireGuard (003), node/IPAM (004), zones/nftables (005), account lockout/invite expiry (v1.1).
