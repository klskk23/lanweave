---
description: "Task list for feature 026 — invite-expiry"
---

# Tasks: Invite Code Expiry

**Input**: Design documents from `/specs/026-invite-expiry/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: REQUIRED. Per Constitution Principle II (NON-NEGOTIABLE), this feature crosses the SQLite boundary, so each user story carries acceptance + integration tests against the real store. Tests are deterministic — expiry is exercised with past/future-dated `expires_at` rows, never wall-clock sleeps (research.md Decision 4). Suites run namespace-isolated under `unshare -rUn`.

**Organization**: Tasks grouped by user story (US1 P1, US2 P2, US3 P3) for independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: US1 / US2 / US3 (maps to spec.md user stories)

## Path Conventions

Single Go service. Server code under `internal/server/{config,store,api,app}`, shared wire types under `pkg/protocol`, admin helper under `packaging/scripts`, example config + DESIGN at repo root. Tests are colocated `*_test.go` files.

---

## Phase 1: Setup

**Purpose**: Establish a known-green baseline before touching the invite path.

- [X] T001 On branch `026-invite-expiry`, confirm baseline builds and existing suite is green: `go build ./...` then `unshare -rUn go test ./internal/server/...` — record the pass before making changes.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema column every user story depends on. Nothing else can be built or tested until the `expires_at` column exists.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T002 Create goose migration `internal/server/store/migrations/0006_invite_expires.sql` with `-- +goose Up` → `ALTER TABLE invites ADD COLUMN expires_at TEXT;` and `-- +goose Down` → `ALTER TABLE invites DROP COLUMN expires_at;` (additive, nullable, no backfill — existing rows stay NULL = never-expire).
- [X] T003 [P] Add migration coverage in `internal/server/store/store_test.go`: after migrating a fresh DB, assert the `invites` table has a nullable `expires_at` column and that a row inserted without it reads back NULL (proves grandfathering substrate).

**Checkpoint**: Schema ready — user stories can begin.

---

## Phase 3: User Story 1 - Expired codes can no longer be redeemed (Priority: P1) 🎯 MVP

**Goal**: Registration rejects an invite code whose `expires_at` has passed, folding it into the existing generic invalid-code error with no disclosure. NULL/future codes still redeem.

**Independent Test**: Insert an invite row with `expires_at` in the past → register is rejected with the generic error; insert one with a future `expires_at` (or NULL) → register succeeds. No config or admin-create path required (rows inserted directly), so this story stands alone.

### Tests for User Story 1 (write first, ensure they FAIL) ⚠️

- [X] T004 [P] [US1] In `internal/server/store/register_test.go`, add a test that inserts an invite with `expires_at = now()-1h` and asserts `Register` returns `ErrInviteInvalid` and leaves the row unused.
- [X] T005 [P] [US1] In `internal/server/store/register_test.go`, add a test that inserts invites with (a) `expires_at = now()+24h` and (b) `expires_at = NULL` (grandfathered) and asserts `Register` succeeds for both and marks them used (covers SC-004 / FR-006 / FR-007).
- [X] T006 [P] [US1] In `internal/server/api/auth_handlers_test.go`, add a test asserting an expired code returns HTTP 422 `invite_invalid` with a body byte-identical to the unknown-code and already-used-code responses (FR-003 / SC-005 — no oracle).

### Implementation for User Story 1

- [X] T007 [US1] In `internal/server/store/register.go`, extend the claim `UPDATE invites SET used_by_user_id=?, used_at=? WHERE code=? AND used_at IS NULL` with `AND (expires_at IS NULL OR expires_at > ?)`, binding the same `now()` RFC3339 value; keep `RowsAffected != 1 → ErrInviteInvalid` (expired folds in, no new error/branch/log).

**Checkpoint**: Expiry is enforced end-to-end at registration. MVP shippable.

---

## Phase 4: User Story 2 - Operator controls the expiry window globally (Priority: P2)

**Goal**: A single config value `invite_ttl` decides the window; newly issued codes are stamped `created_at + invite_ttl`; `0`/empty disables expiry (writes NULL); a negative value fails startup. Pre-existing codes are never retroactively expired.

**Independent Test**: Set `invite_ttl` and create a code → its `expires_at` matches the window. Set `invite_ttl="0"` → created code has NULL `expires_at` (never expires). Provide a negative duration → server refuses to start. (Grandfathering of pre-existing rows is already proven by T005.)

### Tests for User Story 2 (write first, ensure they FAIL) ⚠️

- [X] T008 [P] [US2] In `internal/server/config/config_test.go`, add tests: `invite_ttl="24h"` validates and resolves to 24h; empty/absent is valid and means disabled (0); a negative duration (e.g. `"-1h"`) fails `Validate` at startup (FR-011); a non-duration string fails `Validate`.
- [X] T009 [P] [US2] In `internal/server/store/invites_test.go`, add tests: `Create(ctx, admin, 24h)` returns `expiresAt ≈ created_at+24h` and the DB row matches; `Create(ctx, admin, 0)` returns `expiresAt == nil` and writes `expires_at = NULL` (FR-001 / FR-005).

### Implementation for User Story 2

- [X] T010 [US2] In `internal/server/config/config.go`, add `InviteTTL string `toml:"invite_ttl"`` to `AuthConfig`; in `Validate`, parse non-empty `InviteTTL` via `time.ParseDuration` and reject negative; do NOT add an `applyDefaults` fallback (empty stays empty = never-expire — research.md Decision 1).
- [X] T011 [US2] In `internal/server/store/invites.go`, change `Invites().Create` to `Create(ctx, createdByUserID int64, ttl time.Duration) (code string, expiresAt *time.Time, err error)`: compute `created_at=now()`; if `ttl <= 0` insert `expires_at=NULL` and return nil, else insert `expires_at = created_at.Add(ttl)` (RFC3339, truncate to second) and return a pointer; include `expires_at` in the INSERT column list.
- [X] T011a [US2] Update **every** existing `Invites().Create` call site to the new 3-return signature so the tree compiles (pass `0` ttl where the caller has no configured window): `internal/server/store/register_test.go:20`, `internal/server/store/invites_test.go:32,36,69`, `internal/server/store/users_delete_test.go:39`, `internal/client/panel/panel_integration_test.go:94`, `internal/client/onboard/onboard_integration_test.go:54`. (The handler caller is covered by T013; T004/T005/T009 may already touch the store test files — reconcile there.)
- [X] T012 [US2] In `internal/server/api/router.go`, add `InviteTTL time.Duration` to `Options`; in `internal/server/api/auth_handlers.go` (or wherever `handlers` is built) thread it into the `handlers`/invite handler so the create path can pass it to `Create`.
- [X] T013 [US2] In `internal/server/app/app.go`, resolve `cfg.Auth.InviteTTL` (empty → `0`, else `time.ParseDuration`) and set `Options.InviteTTL`; update the `createInvite` caller in `internal/server/api/invite_handlers.go:18` to pass the resolved TTL into `Create` and accept the new 3-return signature (response payload unchanged here — expiry is surfaced in US3) so the tree compiles. (Remaining non-handler call sites are handled by T011a.)
- [X] T014 [P] [US2] In `config.toml.example`, add `invite_ttl = "24h"` under `[auth]` with a comment stating `0`/empty = codes never expire (开箱启用).

**Checkpoint**: Operators can set/disable the window; codes are stamped accordingly; bad config is rejected at startup. US1 still passes.

---

## Phase 5: User Story 3 - Admin sees the expiry when generating a code (Priority: P3)

**Goal**: The admin create surface reports when a freshly generated code expires (or that it never does).

**Independent Test**: `lanweavectl invite` with expiry enabled prints `Expires: <RFC3339>`; with `invite_ttl="0"` it prints `Expires: never`. The admin HTTP response carries `expires_at` (present when stamped, omitted when never).

**Depends on**: US2 (T011 makes `Create` return the stamped expiry that this story surfaces).

### Tests for User Story 3 (write first, ensure they FAIL) ⚠️

- [X] T015 [P] [US3] In `internal/server/api/invite_handlers_test.go`, add tests: with `Options.InviteTTL>0`, `POST /api/v1/admin/invites` response includes `expires_at` ≈ now+TTL; with `InviteTTL=0`, the response omits `expires_at` (FR-009 / SC-006).

### Implementation for User Story 3

- [X] T016 [US3] In `pkg/protocol/auth.go`, add `ExpiresAt *string `json:"expires_at,omitempty"`` to `CreateInviteResponse`; add `ExpiresAt *string `json:"expires_at,omitempty"`` to `InviteListItem` and allow status value `"expired"`.
- [X] T017 [US3] In `internal/server/api/invite_handlers.go`, populate `CreateInviteResponse.ExpiresAt` from the `expiresAt` returned by `Create` (RFC3339 string, nil → omit); in `toInviteListItem`, derive status `"used"` (if used) → else `"expired"` (if `expires_at` non-NULL and past) → else `"unused"`, and set `ExpiresAt`.
- [X] T018 [US3] In `packaging/scripts/lanweavectl.sh`, update `cmd_invite` to parse `.expires_at` from the response and print `Expires: <value>` when present, `Expires: never` when absent (keep the existing `Invite code:` line; do not log full code anywhere new).

**Checkpoint**: All three stories independently functional.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T019 [P] Amend `DESIGN.md` §7.1 (invite model — note codes now carry an optional expiry; NULL/`0`/empty = never) and §4 data table (add `invites.expires_at` nullable column). Same PR per the frozen-design rule.
- [X] T020 Run `specs/026-invite-expiry/quickstart.md` validation: `unshare -rUn go test ./internal/server/...`, then exercise `lanweavectl invite` under `invite_ttl="24h"` and `invite_ttl="0"` to confirm the `Expires:` output.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: depends on Setup; BLOCKS all user stories (schema column).
- **US1 (Phase 3)**: depends on Foundational only. Independently testable (inserts rows directly). MVP.
- **US2 (Phase 4)**: depends on Foundational. Independent of US1 at the test level; adds config + create-stamping.
- **US3 (Phase 5)**: depends on Foundational **and US2** (surfaces the expiry that `Create` returns).
- **Polish (Phase 6)**: after the stories it documents/validates.

### Within Each User Story

- Tests written and failing before implementation (Constitution II).
- Store/config layer before handlers; handlers before the shell helper.

### Parallel Opportunities

- T003 [P] runs alongside other foundational reads.
- US1 tests T004/T005/T006 [P] (different files) run together; impl T007 after.
- US2 tests T008/T009 [P] run together; T014 [P] (example config) is independent of the Go impl.
- US3 test T015 [P]; T019 [P] (DESIGN docs) is independent of code tasks.
- Once Foundational is done, US1 and US2 can proceed in parallel; US3 starts after US2's `Create` change lands.

---

## Parallel Example: User Story 1

```bash
# Launch US1 tests together (different files):
Task: "Store test — past-dated expires_at rejected in internal/server/store/register_test.go"
Task: "Store test — future + NULL expires_at accepted in internal/server/store/register_test.go"  # same file → run sequentially with the above
Task: "API test — expired → 422 invite_invalid, no oracle in internal/server/api/auth_handlers_test.go"
```

> Note: T004 and T005 touch the same file (`register_test.go`); treat them as sequential edits to that file even though both are [P] relative to the API test.

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup → 2. Phase 2 Foundational (migration) → 3. Phase 3 US1 (register enforcement) → **STOP & VALIDATE** expired codes are rejected with the generic error → ship.

### Incremental Delivery

1. Foundation + US1 → expiry enforced (MVP).
2. Add US2 → operators control/disable the window; codes get stamped.
3. Add US3 → admin sees the deadline at generation time.
4. Polish → DESIGN.md amendment + quickstart validation.

---

## Notes

- [P] = different files, no incomplete-task dependency.
- Expiry tests use concrete past/future timestamps relative to `now()` — never `sleep` (Constitution II, research.md Decision 4).
- Invite codes MUST NOT appear in logs — preserved across T017/T018.
- `after_tasks` / `after_implement` git auto-commit hooks exist but are NOT run automatically here; commit only when the user asks, staging explicit files (exclude root `TODO.md`).
