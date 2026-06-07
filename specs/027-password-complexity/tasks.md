---
description: "Task list for feature 027 — password-complexity"
---

# Tasks: Password Complexity

**Input**: Design documents from `/specs/027-password-complexity/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: REQUIRED. Per Constitution Principle II (NON-NEGOTIABLE): the shared
policy gets exhaustive pure-Go table-driven unit tests; the server story gets an
acceptance test against real SQLite under `unshare -rUn`; the client stories get
wizard tests. No system boundary is mocked. The policy is a pure function, so its
tests are deterministic with no isolation needed.

**Organization**: Tasks grouped by user story (US1 P1, US2 P2, US3 P3) for
independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: US1 / US2 / US3 (maps to spec.md user stories)

## Path Conventions

Single Go module. Shared policy under `pkg/passwordpolicy`, server handler under
`internal/server/api`, client wizard under `internal/client/ui`, localization under
`internal/client/i18n`, example/DESIGN/ROADMAP at repo root. Tests are colocated
`*_test.go` files.

---

## Phase 1: Setup

**Purpose**: Known-green baseline before touching the auth path.

- [X] T001 On branch `027-password-complexity`, confirm baseline builds and the suite is green: `go build ./...`, then `unshare -rUn go test ./internal/server/... ./pkg/...` and `unshare -rUn sh -c 'ip link set lo up; go test ./internal/client/...'` — record the pass before changes.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The shared validator every user story depends on. Nothing else can be
built or tested until `pkg/passwordpolicy` exists and is proven.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T002 [P] Write `pkg/passwordpolicy/passwordpolicy_test.go` FIRST (must fail to compile/red): table-driven cases covering every row in `contracts/passwordpolicy.md` — OK cases (`Aa345678`, `Aa3!5678`, exactly-8, exactly-64), boundary 7/65, one row per reason (`ReasonNoUpper`/`NoLower`/`NoDigit`/`TooShort`/`TooLong`/`Charset` incl. space, non-ASCII `密码Aa345678`, emoji), the charset-first ordering case (e.g. `密` alone → `ReasonCharset`), and `Reason.String()` stable tokens.
- [X] T003 Implement `pkg/passwordpolicy/passwordpolicy.go`: `Reason` enum + consts (`ReasonOK,ReasonCharset,ReasonTooShort,ReasonTooLong,ReasonNoUpper,ReasonNoLower,ReasonNoDigit`), `Reason.String()` returning the documented lowercase tokens, and `Validate(pw string) (Reason, bool)` evaluating charset → length → upper → lower → digit per the data-model order. Pure, no logging, never echoes `pw`. Make T002 green.

**Checkpoint**: Shared validator ready and unit-green — user stories can begin.

---

## Phase 3: User Story 1 - Server rejects weak account passwords at registration (Priority: P1) 🎯 MVP

**Goal**: The `register` handler rejects any non-compliant password with the generic
`validation_error`/400 envelope and never creates an account; login stays ungated.

**Independent Test**: Drive `/api/v1/register` with compliant and non-compliant
passwords against real SQLite; confirm rejects create no account and don't consume
the invite, a compliant one succeeds, and a weak pre-existing account still logs in.

### Tests for User Story 1 (write first, ensure they FAIL) ⚠️

- [X] T004 [US1] In `internal/server/api/auth_handlers_test.go`, add a test driving `/api/v1/register` with one password per failure reason (no-upper, no-lower, no-digit, 7-char, 65-char, space, non-ASCII): each → `400` `validation_error`, **no account created**, invite **not** consumed; a compliant password → account created. Include a secrets-in-log assertion (rejection path logs no password material).
- [X] T005 [US1] In `internal/server/api/auth_handlers_test.go`, add a test that an account whose password was set directly (store layer, weak e.g. `bobs-strong-pw`) can still `POST /api/v1/login` successfully — guards FR-009 (login not gated).

### Implementation for User Story 1

- [X] T006 [US1] In `internal/server/api/auth_handlers.go`, replace the `case len(req.Password) < minPasswordLen` check in `register()` with `passwordpolicy.Validate(req.Password)`; on failure map the reason to the English message per `contracts/register.md` (short/long → "Password must be 8-64 characters."; class reasons → "Password must include an uppercase letter, a lowercase letter, and a digit."; charset → "Password may only contain ASCII letters, digits, and symbols (no spaces).") via `validation_error`/400. Remove the now-unused `minPasswordLen` const. Keep the check in the same position (before the invite-presence check).
- [X] T007 [US1] Fixture sweep — handler/login path: update every test that registers via `/api/v1/register` (or whose seeded account must satisfy the policy) to a compliant password, leaving deliberately-invalid fixtures (the "short password" / "wrong password" rejection cases) unchanged. Files: `internal/server/api/{auth_handlers_test.go,invite_handlers_test.go,node_handlers_test.go,zone_handlers_test.go,admin_user_handlers_test.go,perf_test.go}`, `internal/server/app/{app_test.go,dataplane_test.go,status_test.go,zones_dataplane_test.go}`, `internal/client/{apiclient/client_test.go,onboard/onboard_integration_test.go,panel/panel_integration_test.go}`. Prefer one compliant constant (e.g. `Aa345678`) to keep fixtures uniform.

**Checkpoint**: Weak passwords are rejected at registration end-to-end; login intact. MVP shippable.

---

## Phase 4: User Story 2 - Registrant gets immediate, specific feedback (Priority: P2)

**Goal**: The create-account wizard step blocks submission of a non-compliant password
and shows the specific failing rule, localized — no server round trip.

**Independent Test**: In the wizard create-account step, each single-rule-breaking
password blocks advancing and shows that rule's localized message; a compliant
password advances.

**Depends on**: Foundational (`passwordpolicy`).

### Tests for User Story 2 (write first, ensure they FAIL) ⚠️

- [X] T008 [US2] In `internal/client/ui/` (alongside existing wizard tests; create `wizard_test.go` if absent), add a test that in `CreateAccount` mode a non-compliant password (e.g. `aa345678`) does NOT advance past `stepAuth` and surfaces the reason-specific message, while a compliant password advances; SignIn mode is unaffected by the policy.

### Implementation for User Story 2

- [X] T009 [US2] In `internal/client/ui/wizard.go` `stepAuth()`, after the existing empty/invite checks, when `z.mode == onboard.CreateAccount` call `passwordpolicy.Validate(pass.Text)`; on failure set `errLbl` to `i18n.T("wizard.pwRule."+reason.String())` and return (block advance). SignIn path unchanged.
- [X] T010 [US2] In `internal/client/i18n/zh-Hans.json` and `internal/client/i18n/en.json`, add `wizard.pwRule.charset`, `wizard.pwRule.too_short`, `wizard.pwRule.too_long`, `wizard.pwRule.no_upper`, `wizard.pwRule.no_lower`, `wizard.pwRule.no_digit` with human-readable localized text per reason. (Same files are extended by T012 — keep additions in one place.)

**Checkpoint**: The form refuses weak passwords locally with precise, localized feedback. US1 still passes.

---

## Phase 5: User Story 3 - Rules are visible before the user types (Priority: P3)

**Goal**: A persistent rule hint sits beneath the password field in the create-account
step, visible before any input, in the UI language.

**Independent Test**: Open the wizard create-account step; the rule hint
("8–64 characters; upper, lower, and digit; ASCII only, no spaces") is visible under
the password field before typing, localized.

**Depends on**: Foundational; shares the wizard/i18n files with US2.

### Tests for User Story 3 (write first, ensure they FAIL) ⚠️

- [X] T011 [US3] In `internal/client/ui/wizard_test.go`, add a test asserting the create-account `stepAuth` body contains a visible hint label whose text equals `i18n.T("wizard.pwRule.hint")` before any input, and that it is present in CreateAccount mode.

### Implementation for User Story 3

- [X] T012 [US3] In `internal/client/ui/wizard.go` `stepAuth()`, add a persistent hint label (`i18n.T("wizard.pwRule.hint")`) beneath the password field in the create-account layout (visible without input; may hide in SignIn mode like the invite row). Add the `wizard.pwRule.hint` key to both `internal/client/i18n/zh-Hans.json` and `en.json`.

**Checkpoint**: All three stories independently functional.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T013 [P] Fixture sweep — store-layer consistency: update remaining non-handler test passwords (direct `.Register()` / `CreateAdmin()` seeds) to the same compliant value for uniformity, in `internal/server/store/{register_test.go,store_test.go,invites_test.go,nodes_test.go}` and `internal/server/api/api_helpers_test.go` (admin seed). Leave intentionally-invalid fixtures untouched. (Cosmetic consistency, not required for correctness since these bypass handler validation — do not change asserted behavior.)
- [X] T014 [P] Amend `DESIGN.md` §7 认证与权限: document the account-password policy (8–64 ASCII, upper+lower+digit, no spaces/non-ASCII; enforced at registration only; login, zone passwords, and bootstrap admin exempt). Same PR per the frozen-design rule.
- [X] T015 [P] Add the 027 entry to `docs/ROADMAP.md` (background / scope / acceptance / out-of-scope / dependencies), consistent with the 026 entry; check off at merge.
- [X] T016 Run `specs/027-password-complexity/quickstart.md` validation: `go test ./pkg/passwordpolicy/...`, `unshare -rUn go test ./internal/server/... ./pkg/...`, `unshare -rUn sh -c 'ip link set lo up; go test ./internal/client/...'`, and `go build ./...` — all green. `gofmt`/`go vet`/`staticcheck` clean.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: depends on Setup; BLOCKS all user stories (shared validator).
- **US1 (Phase 3)**: depends on Foundational. Independently testable (drives the endpoint). MVP.
- **US2 (Phase 4)**: depends on Foundational. Independent of US1 at the test level.
- **US3 (Phase 5)**: depends on Foundational; shares `wizard.go` + i18n JSON with US2 (sequential edits to those files).
- **Polish (Phase 6)**: after the stories it documents/validates.

### Within Each User Story

- Tests written and failing before implementation (Constitution II).
- Shared package before handlers and UI; handler/UI before docs.

### Parallel Opportunities

- T002 [P] (policy unit test) can be authored alongside Setup wrap-up.
- US1 and US2/US3 can proceed in parallel once Foundational lands (server vs client, different files) — except both touch test fixtures; coordinate T007 (handler-path) vs T013 (store-layer) to avoid overlapping the same files.
- T013 / T014 / T015 [P] are independent (different files) and can run together in Polish.
- Within US2+US3, `wizard.go` and the i18n JSONs are shared — treat T009/T012 and T010/T012 as sequential edits to those files.

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Setup → 2. Foundational (`pkg/passwordpolicy` + unit tests) → 3. US1 (server enforcement + handler-path fixture sweep) → **STOP & VALIDATE** weak passwords rejected at registration, login intact → ship.

### Incremental Delivery

1. Foundation + US1 → server-side guarantee (MVP).
2. Add US2 → client blocks submit with precise localized feedback.
3. Add US3 → persistent rule hint up front.
4. Polish → store-layer fixture sweep + DESIGN §7 + ROADMAP 027 + quickstart validation.

---

## Notes

- [P] = different files, no incomplete-task dependency.
- The policy is pure; its unit tests need no namespace isolation. Register acceptance runs under `unshare -rUn` against real SQLite; client wizard tests run with loopback up.
- Passwords MUST NOT appear in logs — preserved across T004/T006 (typed reason carries no password material).
- Hashing stays argon2id; the 64-char cap is a defensive input bound, not a hash limit (research Decision 3).
- `before_tasks` / `after_*` git auto-commit hooks exist but are NOT run automatically; commit only when the user asks, staging explicit files (exclude root `TODO.md`).
