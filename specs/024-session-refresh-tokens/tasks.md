# Tasks: Session Refresh Tokens

**Input**: Design documents from `specs/024-session-refresh-tokens/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/refresh-api.md, quickstart.md

**Tests**: REQUIRED. Constitution Principle II (NON-NEGOTIABLE) — real SQLite, no mocks, run under
`unshare -rUn go test ./...`; each user story carries ≥1 acceptance test. Time-based behavior is
exercised through an injectable `now func() time.Time` in the store, never wall-clock sleeps.

**Organization**: Tasks are grouped by user story (US1 P1 → US2 P2 → US3 P3) so each can be
implemented and tested independently. MVP = User Story 1.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 / US2 / US3 (omitted for Setup, Foundational, Polish)
- Go convention: tests live next to source (`*_test.go`), not under a separate `tests/` tree.

---

## Phase 1: Setup

**Purpose**: Establish a clean baseline before touching code.

- [X] T001 Confirm the privileged test harness is green on branch `024-session-refresh-tokens` before changes: `unshare -rUn go build ./...` and `unshare -rUn go test ./...` both pass (baseline to detect regressions introduced by this slice).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The shared data layer every user story compiles against. No user-story work starts until this phase compiles.

**⚠️ CRITICAL**: Blocks all of Phase 3–5.

- [X] T002 Create migration `internal/server/store/migrations/0005_refresh_tokens.sql` (goose up/down): table `refresh_tokens(id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`. Mirror the FK-cascade pattern in `0003_nodes.sql`; timestamps as RFC 3339 text (matches `users`/`nodes`).
- [X] T003 [P] Add token crypto helpers. **DEVIATION**: placing these in `internal/server/auth` would create an import cycle (`store → auth → store`, since `auth/bootstrap.go` imports `store`). The crypto is therefore inlined into `internal/server/store/refresh_tokens.go` — `crypto/rand` 32 bytes → base64url in `Issue`, and unexported `hashRefreshToken(plaintext)` (`crypto/sha256` → lowercase hex). Keeps RT crypto cohesive with its only consumer (the store) while preserving acyclic deps.
- [X] T004 [P] Add wire types in `pkg/protocol/auth.go`: add `RefreshToken string \`json:"refresh_token"\`` to `LoginResponse`; add `RefreshRequest{RefreshToken}`, `RefreshResponse{Token}`, `LogoutRequest{RefreshToken}`. `RegisterResponse` stays unchanged (D7).
- [X] T005 Create `internal/server/store/refresh_tokens.go` skeleton + wiring: `RefreshTokenRepo` struct (db handle + injectable `now func() time.Time` defaulting to `time.Now().UTC`), `ErrRefreshInvalid` sentinel, named const `refreshTTL = 30 * 24 * time.Hour`, and a `RefreshTokens() *RefreshTokenRepo` accessor on the store (`internal/server/store/store.go`). Method bodies added per story; package must compile.

**Checkpoint**: `unshare -rUn go build ./...` green — user stories can begin.

---

## Phase 3: User Story 1 - Stay signed in without re-entering the password (Priority: P1) 🎯 MVP

**Goal**: Login issues a refresh token; the client silently renews an expired session via `/refresh` and the user never sees a password prompt during normal use.

**Independent Test**: Sign in, force the access token to expire, perform a server action → it succeeds with no password prompt (quickstart A1–A3, B1–B3).

### Tests for User Story 1 (write first; ensure they FAIL) ⚠️

- [X] T006 [P] [US1] Store tests in `internal/server/store/refresh_tokens_test.go` (real SQLite, injected clock): `Issue` returns a plaintext RT and persists only its SHA-256 hash (assert the plaintext never appears in any stored column); `Validate(valid)` → owning `userID`; `Validate(unknown)` → `ErrRefreshInvalid`. **Concurrency (M2 / spec "concurrent expiry" edge case, D4)**: the same RT validated from N goroutines at once all succeed and return the same `userID` — no rotation, no call invalidates another.
- [X] T007 [P] [US1] API tests in `internal/server/api/auth_handlers_test.go` (real router+store via `newHarness`): `POST /login` returns `{token, refresh_token}`; `POST /refresh` with a valid RT → 200 `{token}` and that token authorizes `GET /me`; `/refresh` unknown RT → 401; `/refresh` missing/empty field → 400. Assert no plaintext RT is written to server logs. **TTL unchanged (M1 / FR-003)**: assert the access token minted by both `/login` and `/refresh` still has a ~2h lifetime (`exp − iat ≈ 2h`) — this feature must not alter the access JWT TTL.
- [X] T008 [P] [US1] apiclient tests in `internal/client/apiclient/client_test.go` (httptest server): an authenticated call that gets 401 triggers exactly one silent `POST /refresh`, then the original request is retried once and succeeds; when `/refresh` fails, `ErrSessionExpired` surfaces and there is no retry loop.

### Implementation for User Story 1

- [X] T009 [US1] Implement `RefreshTokenRepo.Issue(ctx, userID)` and `Validate(ctx, plaintext)` in `internal/server/store/refresh_tokens.go`: `Issue` inserts `(user_id, HashRefreshToken(plaintext), now+refreshTTL, NULL, now)` and returns the plaintext once; `Validate` looks up by `token_hash`, returns `ErrRefreshInvalid` if missing / `revoked_at` set / `expires_at ≤ now`, else slides `expires_at = now+refreshTTL` and returns `user_id`.
- [X] T010 [US1] In `internal/server/api/auth_handlers.go`: login handler issues an RT via `store.RefreshTokens().Issue` and returns it in `LoginResponse.RefreshToken`; add the `refresh` handler (decode `RefreshRequest` via the shared `decodeJSON`, `Validate` → mint an access JWT via the existing `JWTManager` → `RefreshResponse{Token}`; `ErrRefreshInvalid` → 401; validation → 400). RT-authed, never logs the plaintext.
- [X] T011 [US1] In `internal/server/api/router.go`: register **public** route `POST /api/v1/refresh` (NO `AuthRequired` — authenticates via the RT in the body because the access JWT is expired at refresh time).
- [X] T012 [P] [US1] In `internal/client/keyring/store.go`: add `RefreshTokenName = "lanweave-refresh-token"` (same DPAPI-backed store as `SessionTokenName`/`DeviceKeyName`).
- [X] T013 [US1] In `internal/client/apiclient/client.go`: add unexported `refreshToken` field + `SetRefreshToken`/`RefreshToken` accessors; `Login` decodes and stores the RT; add `Refresh()` (POST `/refresh` with the held RT → new access token); fold lazy refresh into the single `do()` chokepoint — on an authed 401 with an RT held, call `Refresh` once, set the new token, retry the original request exactly once; add `ErrRefreshFailed`.
- [X] T014 [US1] In `internal/client/onboard/onboard.go`: extend the `apiClient` interface with `RefreshToken()/SetRefreshToken()`; `Provision` persists `RefreshTokenName` at the same final step where it saves `SessionTokenName` (slice 019 persistence point).
- [X] T015 [US1] In `internal/client/panel/panel.go`: extend the `api` interface with `RefreshToken()/SetRefreshToken()`; `LoadSession` restores the RT from the keyring into the client; `SignIn` caches the RT after a successful login.

**Checkpoint**: MVP — silent refresh works end-to-end; quickstart A1–A3 pass. Stop & validate here.

---

## Phase 4: User Story 2 - Revoke a device's session (Priority: P2)

**Goal**: Logout revokes one RT server-side (idempotent); deleting a user cascades away all their RTs; a revoked device falls back to password login.

**Independent Test**: Sign in, `POST /logout` with the RT (or delete the user), then `/refresh` with it → 401 (quickstart A4, A6, B4–B5).

### Tests for User Story 2 (write first; ensure they FAIL) ⚠️

- [X] T016 [P] [US2] Store tests in `internal/server/store/refresh_tokens_test.go`: `Revoke(rt)` then `Validate` → `ErrRefreshInvalid`; `Revoke` on unknown / already-revoked → no error (idempotent); deleting the owning user removes its `refresh_tokens` rows (FK cascade) and subsequent `Validate` → `ErrRefreshInvalid`.
- [X] T017 [P] [US2] API tests in `internal/server/api/auth_handlers_test.go`: `POST /logout` with a valid RT → 204, then `/refresh` with it → 401; `/logout` with an unknown RT → 204 (idempotent); `/logout` missing field → 400; `DELETE /api/v1/admin/users/{id}` then `/refresh` with that user's RT → 401.
- [X] T018 [P] [US2] Client tests: in `internal/client/apiclient/client_test.go` assert `Logout()` POSTs the held RT to `/logout`; in `internal/client/panel/panel_test.go` assert `Logout` calls `api.Logout` (best-effort) and removes `RefreshTokenName` from the keyring.

### Implementation for User Story 2

- [X] T019 [US2] Implement `RefreshTokenRepo.Revoke(ctx, plaintext)` in `internal/server/store/refresh_tokens.go`: `UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`; idempotent (unknown / already-revoked → nil error). Cascade-on-delete needs no code — the FK from T002 handles it inside the existing `users.DeleteCascade` transaction.
- [X] T020 [US2] In `internal/server/api/auth_handlers.go`: add the `logout` handler — decode `LogoutRequest` via `decodeJSON`, call `Revoke`, always return 204 when well-formed; validation/oversized body → 400. Never an oracle for token state.
- [X] T021 [US2] In `internal/server/api/router.go`: register **public** route `POST /api/v1/logout` (NO `AuthRequired`).
- [X] T022 [US2] In `internal/client/apiclient/client.go`: add `Logout()` that POSTs the stored RT to `/logout` (best-effort; ignore network errors so local sign-out always proceeds).
- [X] T023 [US2] In `internal/client/panel/panel.go`: `Logout` first calls `api.Logout` to revoke server-side, then deletes `RefreshTokenName` from the keyring alongside the session token and device key.
- [X] T024 [US2] In `internal/client/onboard/onboard.go`: `Cleanup` deletes `RefreshTokenName` from the keyring (with the existing session token / device key cleanup).

**Checkpoint**: US1 + US2 both work; revocation and cascade verified (quickstart A4–A6).

---

## Phase 5: User Story 3 - Eventually require re-login after long inactivity (Priority: P3)

**Goal**: An RT expires after 30 days of inactivity; each successful renewal slides the expiry forward; an abandoned device must re-login.

**Independent Test**: Issue an RT, advance the injected clock past 30 days without using it, attempt `Validate` → `ErrRefreshInvalid`; a regularly-renewed RT never expires (quickstart C, store tests).

### Tests for User Story 3 (write first; ensure they FAIL) ⚠️

- [X] T025 [P] [US3] Store tests in `internal/server/store/refresh_tokens_test.go` (injected clock): each successful `Validate` slides `expires_at` to `now + 30d`; an RT unused for >30 days → `Validate` → `ErrRefreshInvalid` (expired); an RT renewed within the window on a repeating cadence never expires.

### Implementation for User Story 3

- [X] T026 [US3] Confirm the sliding 30-day window is driven by the single `refreshTTL` const (T005) in both `Issue` and `Validate`'s slide (`internal/server/store/refresh_tokens.go`); add a WHY comment that the window is fixed (not configurable) this slice (D3). No behavior change beyond what T009 implemented — this task exists to make the US3 guarantee explicit and test-covered.

**Checkpoint**: All three stories independently functional.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T027 [P] Amend `DESIGN.md` (Constitution: DESIGN.md authority, same PR): §4.1 add a `refresh_tokens` row; §7.3 split into stateless short access JWT (2h) + stateful revocable RT (30-day sliding, hash stored) and correct the "重启服务 = 全用户被踢" line (a restart now only invalidates access tokens; RTs survive against the DB); §8.1 `/login` response → `{token, refresh_token}`, add `POST /refresh` and `POST /logout`; §11 risk register (restart no longer global-revoke; client holds one long-lived DPAPI-protected RT).
- [X] T028 [P] Security assertion — satisfied by `TestRefreshNoSecretInLogs` (no plaintext RT in logs across login+refresh) plus the store test `TestRefreshIssueValidate` (only the SHA-256 hash is persisted, never the plaintext, in any column). The plaintext appears only in `/login`'s one-time issuance.
- [~] T029 Quickstart Part A (A1–A6) is covered 1:1 by automated api-layer acceptance tests against the real router+store (real SQLite, no mocks): A1 `TestLoginReturnsRefreshToken`; A2/A3 `TestRefreshEndpoint` + `TestRefreshKeepsAccessTTL`; A4/A5 `TestLogoutRevokesRefreshToken` + `TestRefreshEndpoint` (401/400 paths); A6 `TestDeleteUserRevokesRefreshTokens`. The literal live-server curl run (TLS + WireGuard + nftables + root) belongs to the same manual/integration matrix as Part B under the Constitution II GUI/exec exemption — not run in the `unshare` test gateway. **Status: covered by automation; live curl deferred to the Windows/integration matrix.**
- [X] T030 Final sweep: `unshare -rUn bash -c 'ip link set lo up && go test ./...'` green (all packages), `go vet ./...` clean, `gofmt -l` reports nothing under the changed packages.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)** → no deps.
- **Foundational (T002–T005)** → after Setup; **blocks all user stories**.
- **US1 (T006–T015)** → after Foundational. MVP.
- **US2 (T016–T024)** → after Foundational; touches the same `apiclient/onboard/panel` files as US1, so in practice runs after US1 (P1 → P2).
- **US3 (T025–T026)** → after Foundational; verifies behavior in `Validate` (implemented in US1 T009), so order it after US1.
- **Polish (T027–T030)** → after the user stories you intend to ship.

### Within Each User Story

- Tests (write first, must FAIL — in Go a not-yet-implemented method = compile failure = fail) → store methods → server handler → router → client.
- Same-file edits run sequentially: `store/refresh_tokens.go` (T009→T019), `apiclient/client.go` (T013→T022), `panel/panel.go` (T015→T023), `onboard/onboard.go` (T014→T024), `auth_handlers.go` (T010→T020), `router.go` (T011→T021), `auth_handlers_test.go` (T007→T017), `refresh_tokens_test.go` (T006→T016→T025).

### Parallel Opportunities

- Foundational: T003 and T004 are [P] (separate files); T002 (migration) is independent of both.
- US1 tests T006 / T007 / T008 are [P] (three different files) — write them together.
- US2 tests T016 / T017 / T018 are [P].
- T012 (keyring) is [P] within US1 (own file).
- Polish T027 / T028 are [P].

---

## Parallel Example: User Story 1 tests

```text
# Launch the three US1 test files together (different files, no shared deps):
Task: "T006 Store tests in internal/server/store/refresh_tokens_test.go"
Task: "T007 API tests in internal/server/api/auth_handlers_test.go"
Task: "T008 apiclient tests in internal/client/apiclient/client_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup (T001).
2. Phase 2 Foundational (T002–T005) — CRITICAL, blocks everything.
3. Phase 3 US1 (T006–T015).
4. **STOP & VALIDATE**: quickstart A1–A3 + manual B1–B3 — silent refresh, no password prompt. Shippable MVP.

### Incremental Delivery

1. Setup + Foundational → data layer ready.
2. US1 → silent refresh (MVP) → validate → demo.
3. US2 → logout + delete-user revocation → validate → demo.
4. US3 → 30-day inactivity expiry → validate.
5. Polish → DESIGN.md amendments, security assertion, quickstart + full test sweep.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- Constitution II: real SQLite (no mocks), `unshare -rUn`; time via injectable clock, never sleeps.
- The cascade (FR-009) is schema-level (FK `ON DELETE CASCADE`) — no application code beyond the migration; verified by T016/T017.
- GUI/exec parts (keyring, Fyne panel flows) carry the DESIGN §11 manual-verification exemption — covered by quickstart Part B, plus the fake-api boundary tests T008/T018.
- Do **not** run the optional `speckit.git.commit` hooks with `git add .` — repo-root `TODO.md` is personal and must stay unstaged.
