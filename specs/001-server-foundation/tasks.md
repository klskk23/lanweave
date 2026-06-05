---
description: "Task list for 001-server-foundation"
---

# Tasks: Server Foundation

**Input**: Design documents from `/specs/001-server-foundation/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present)

**Tests**: REQUIRED per constitution Principle II (Testing Standards, NON-NEGOTIABLE). Every user story includes at least one acceptance test; the store (SQLite system boundary) has integration tests against a real temp DB. Test tasks are written FIRST and must FAIL before their implementation tasks.

**Organization**: Tasks grouped by user story. US1 + US2 are both P1 (the MVP); US3 (P2) and US4 (P3) are independent increments layered on top.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4; Setup/Foundational/Polish carry no story label

## Path Conventions

Single Go project at repo root: `cmd/`, `internal/server/`, `pkg/protocol/`. Paths below are exact and match plan.md's Structure Decision.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project scaffolding and toolchain.

- [X] T001 Initialize Go module `lanweave` (`go mod init lanweave`, Go 1.23) and create the directory skeleton from plan.md: `cmd/lanweaved/`, `internal/server/{app,config,logging,store/migrations,auth,api}/`, `pkg/protocol/`, `deploy/systemd/`
- [X] T002 Add and pin dependencies in `go.mod`, then `go mod tidy`: `github.com/pelletier/go-toml/v2`, `modernc.org/sqlite`, `github.com/pressly/goose/v3`, `golang.org/x/crypto`, `golang.org/x/time`
- [X] T003 [P] Add `Makefile` targets (`build`, `test`, `lint` → gofmt + `go vet` + staticcheck) and `.gitignore` (ignore `run/`, `*.sqlite*`, `/lanweaved`)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Config, persistence, logging, shared DTOs, and the `app.Run` wiring that every user story depends on.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T004 [P] Define `HealthResponse` struct in `pkg/protocol/health.go` per contracts/health.md
- [X] T005 [P] Define `ErrorResponse` struct + `WriteJSONError(w, status, code, msg)` helper in `pkg/protocol/errors.go` per contracts/errors.md
- [X] T006 [P] Implement `logging.Setup(level string) *slog.Logger` (JSON handler → stdout, `slog.LevelVar`, installs as `slog.Default`) in `internal/server/logging/logging.go`
- [X] T007 [P] Write unit test `internal/server/config/config_test.go` covering: missing required fields, bad CIDR, nonexistent cert/key path, non-writable data_dir, defaults applied (log level/rps/burst/jwt_ttl), and all-errors-joined output — MUST FAIL before T008/T009
- [X] T008 Define config structs + `Load(path)` (TOML decode; path resolution `--config` > `LANWEAVE_CONFIG` > `/etc/lanweave/config.toml`) in `internal/server/config/config.go` per contracts/config.md
- [X] T009 Implement `config.Validate()` in `internal/server/config/config.go`: collect all failures via `errors.Join` (host:port, cert/key/data_dir stat + readability, `net.ParseCIDR`, `jwt_secret` ≥32B, duration parse, default fill); add `LogValue()` returning `"[REDACTED]"` on `jwt_secret` and `admin.password` fields (FR-019)
- [X] T010 [P] Write integration test `internal/server/store/store_test.go` against a real temp DB: `Migrate` run twice is idempotent; `CreateAdmin` then `GetByUsername` round-trip; `GetByUsername` returns `(nil,nil)` on absence; duplicate username trips typed already-exists error — MUST FAIL before T011–T013
- [X] T011 [P] Create `internal/server/store/migrations/0001_users.sql` (goose Up/Down for `users` table per data-model.md) and the `//go:embed migrations/*.sql` FS declaration
- [X] T012 Implement `store.Open(dbPath)` (modernc `sqlite` driver, DSN pragmas `busy_timeout=5000`, `journal_mode=WAL`, `foreign_keys=ON`) and `store.Migrate()` (goose library mode, dialect sqlite3, logs each applied version) in `internal/server/store/store.go`
- [X] T013 Implement `UserRepo` with `GetByUsername` and `CreateAdmin` (typed `ErrUserExists` on unique violation) in `internal/server/store/users.go`
- [X] T014 Implement `app.Run(ctx, configPath)` scaffold in `internal/server/app/app.go`: load config → `logging.Setup` → `store.Open` → `store.Migrate` (log each) → bootstrap placeholder → serve placeholder → on `ctx` cancel run graceful-stop placeholder; return wrapped fatal errors (no `os.Exit` here)
- [X] T015 Implement `cmd/lanweaved/main.go`: `--config` flag, `var version` (set via `-ldflags`), `signal.NotifyContext(SIGINT, SIGTERM)`, call `app.Run`, `os.Exit(1)` on returned error

**Checkpoint**: `go build ./...` succeeds; binary loads config, opens DB, applies migration, then exits cleanly (no serving yet). T007 and T010 now pass.

---

## Phase 3: User Story 1 — 启动服务并确认存活 (Priority: P1) 🎯 MVP

**Goal**: The server serves HTTPS, answers `/api/v1/healthz` with 200, and shuts down gracefully on SIGTERM.

**Independent Test**: Write a valid `config.toml` + self-signed cert, start the binary, `curl --cacert ca https://host/api/v1/healthz` → 200 JSON; send SIGTERM → exits ≤10 s. Bad/missing cert → startup aborts, no port bound.

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

> Write FIRST, ensure they FAIL before implementation.

- [X] T016 [P] [US1] Acceptance test in `internal/server/app/app_test.go`: boot `app.Run` with a temp config + generated self-signed cert over a real TLS listener; assert `GET /api/v1/healthz` returns 200 with `status:"ok"`; assert first-200 latency budget; cancel ctx → `app.Run` returns within 10 s (SC-002, SC-003)
- [X] T017 [P] [US1] Unit test in `internal/server/api/api_test.go`: healthz handler returns 200 + correct JSON shape; router returns `not_found`/`method_not_allowed` envelopes; `tls.LoadX509KeyPair` failure path surfaces a clear error (US1-3)

### Implementation for User Story 1

- [X] T018 [US1] Implement healthz handler (`GET /api/v1/healthz` → 200 `HealthResponse{status:"ok", version}`) in `internal/server/api/health.go`
- [X] T019 [US1] Implement `api.Router(...)` building `http.ServeMux` with method patterns and 404/405 → `ErrorResponse` in `internal/server/api/router.go`
- [X] T020 [US1] In `app.Run`, replace the serve placeholder: `tls.LoadX509KeyPair` (fail-fast with clear error, **no listener** on failure), `http.Server` with Read/Write/Idle/ReadHeader timeouts + min TLS 1.2, serve via `ServeTLS` (HTTPS only, FR-009/FR-012)
- [X] T021 [US1] In `app.Run`, replace the graceful-stop placeholder: on ctx cancel call `server.Shutdown` with a 10 s deadline; log `shutdown initiated` / `shutdown complete` (FR-011)

**Checkpoint**: US1 fully functional and independently testable. T016/T017 green.

---

## Phase 4: User Story 2 — 首次启动写入 admin 账号 (Priority: P1) 🎯 MVP

**Goal**: On first boot the configured admin is written with an argon2id hash and `is_admin=1`; subsequent boots skip and never mutate the stored hash.

**Independent Test**: Boot once → `users` row with admin + PHC hash (not plaintext). Restart (even after changing the TOML password) → hash byte-identical, log says "skipping". Empty admin password → startup aborts.

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

> Write FIRST, ensure they FAIL before implementation.

- [X] T022 [P] [US2] Unit test in `internal/server/auth/password_test.go`: `HashPassword` emits a valid argon2id PHC string; two hashes of the same plaintext differ (random salt, FR-014/SC-006); `VerifyPassword` true for correct, false for wrong
- [X] T023 [P] [US2] Integration test in `internal/server/auth/bootstrap_test.go` (real temp DB via store): first `EnsureAdmin` inserts `is_admin=1`; second call skips and leaves the hash byte-identical even when the configured password changes (FR-015/SC-007); empty admin password → error, no row written (US2-3)

### Implementation for User Story 2

- [X] T024 [P] [US2] Implement `HashPassword`/`VerifyPassword` (argon2id, params m=19456 t=2 p=1, 16B `crypto/rand` salt, 32B key, PHC encoding, `subtle.ConstantTimeCompare`) in `internal/server/auth/password.go`
- [X] T025 [US2] Implement `EnsureAdmin(ctx, repo, cfg)` in `internal/server/auth/bootstrap.go`: error if admin creds missing; `GetByUsername`; if absent `HashPassword`+`CreateAdmin` and log `admin created`; else log `admin exists, skipping`; never `UPDATE` (FR-013/015/016)
- [X] T026 [US2] In `app.Run`, replace the bootstrap placeholder: call `EnsureAdmin` after migrate and before serve; any failure aborts startup with no listener opened

**Checkpoint**: US1 + US2 both work independently. Admin bootstrap idempotent across restarts.

---

## Phase 5: User Story 3 — 结构化日志可观测 (Priority: P2)

**Goal**: Every request and every lifecycle event emits a parseable JSON log line; no secret ever appears in logs.

**Independent Test**: Hit endpoints, capture stdout → each line is JSON with `time`+`level`; each request line carries method/path/status/duration; grep confirms no `jwt_secret`/admin password string anywhere.

### Tests for User Story 3 (REQUIRED per constitution Principle II) ⚠️

> Write FIRST, ensure they FAIL before implementation.

- [X] T027 [P] [US3] Test in `internal/server/api/middleware_test.go`: request-logging middleware emits one line per request with method/path/status/duration (US3-1); a captured-log scan asserts no known secret string leaks (FR-019); panic-recovery returns `internal_error` envelope and logs the stack at ERROR

### Implementation for User Story 3

- [X] T028 [US3] Implement request-logging middleware (wrap `ResponseWriter` to capture status + byte count + duration) in `internal/server/api/middleware.go`
- [X] T029 [US3] Implement panic-recovery middleware (log stack at ERROR, return generic `internal_error` envelope — no detail in body) in `internal/server/api/middleware.go`
- [X] T030 [US3] Compose logging + recovery middleware into the router chain in `internal/server/api/router.go`
- [X] T031 [US3] Audit `app.Run` for full lifecycle log coverage (startup, config loaded, each migration, bootstrap decision, listening, shutdown start/complete) per FR-018; add any missing events

**Checkpoint**: All US1–US3 independently functional. Logs fully structured and secret-free.

---

## Phase 6: User Story 4 — 过载时主动限流 (Priority: P3)

**Goal**: A global token-bucket limiter returns 429 (with `Retry-After`) above the configured rate; normal traffic is unaffected; bucket refills.

**Independent Test**: Flood healthz above the rate → mix of 200 and 429 (`rate_limited` envelope + `Retry-After`); pause → requests succeed again.

### Tests for User Story 4 (REQUIRED per constitution Principle II) ⚠️

> Write FIRST, ensure they FAIL before implementation.

- [X] T032 [P] [US4] Test in `internal/server/api/middleware_test.go`: under burst, some requests get 429 with `rate_limited` envelope + `Retry-After` header; after refill window, requests return 200 again (FR-023)

### Implementation for User Story 4

- [X] T033 [US4] Implement `RateLimitMiddleware(limiter *rate.Limiter, keyFn ...)` returning 429 + `ErrorResponse{rate_limited}` + `Retry-After` on `!Allow()`; expose a key-function extension point but wire only the global key (FR-021/022/023/024) in `internal/server/api/middleware.go`
- [X] T034 [US4] Construct the shared `*rate.Limiter` from config (`rps`/`burst`, defaults 100/200) and mount the limiter as the outermost middleware on `/api/...` in `internal/server/api/router.go` + `app.Run`

**Checkpoint**: All four user stories independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Operator-facing artifacts and final verification across stories.

- [X] T035 [P] Write `config.toml.example` at repo root, fully commented, per contracts/config.md
- [X] T036 [P] Write `deploy/systemd/lanweaved.dev.service` (local-dev unit; production packaging is feature 012)
- [X] T037 Run `make lint` (gofmt + `go vet` + staticcheck) clean; confirm ≥70% line coverage and document any uncovered paths in the PR (constitution Principle II)
- [X] T038 Execute `quickstart.md` end-to-end on a clean host: verify US1–US4 plus SC-002 (cold start ≤3 s), SC-003 (shutdown ≤10 s), SC-005 (JSON logs), SC-006 (PHC hash), SC-007 (idempotent hash across 10 restarts)
- [X] T039 [P] Performance smoke: drive 1000 req/s at healthz; confirm no crash and over-limit requests are cleanly rejected (SC-004)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies — start immediately.
- **Foundational (Phase 2)**: depends on Setup — **blocks all user stories**.
- **US1 (Phase 3)** and **US2 (Phase 4)**: both depend only on Foundational. Independent of each other — can be built in parallel or in either order. Together they form the MVP.
- **US3 (Phase 5)**: depends on Foundational; builds on the router/middleware introduced in US1 (composes middleware into `router.go`). Best done after US1.
- **US4 (Phase 6)**: depends on Foundational; mounts middleware in the same `router.go`/`app.Run`. Best done after US1; independent of US2/US3 behavior.
- **Polish (Phase 7)**: depends on all targeted stories being complete.

### Critical Path

```
Setup → Foundational → US1 ─┬─→ US3 ─┐
                            ├─→ US4 ─┼─→ Polish
                       US2 ─┘────────┘
```

### Within Each User Story

- Test tasks (marked ⚠️) are written first and MUST FAIL before the implementation tasks in the same phase.
- In Foundational: models/migrations before the repo; repo before `app.Run` wiring; `app.Run` before `main`.

---

## Parallel Opportunities

- **Setup**: T003 [P] runs alongside T001/T002 setup once the module exists.
- **Foundational**: T004, T005, T006 [P] (distinct files: protocol DTOs + logging). T007 [P] and T010 [P] (test files) and T011 [P] (migration SQL) can all be authored in parallel with each other. T008/T009 (same `config.go`) are sequential; T012/T013 (store) follow T011.
- **US1**: T016 [P] (`app_test.go`) and T017 [P] (`api_test.go`) in parallel.
- **US2**: T022 [P] (`password_test.go`) and T023 [P] (`bootstrap_test.go`) in parallel; T024 [P] (`password.go`) independent of the bootstrap wiring.
- **Polish**: T035, T036, T039 [P] (distinct files/concerns).

### Parallel Example: Foundational shared components

```bash
# Author these together (different files, no interdependency):
Task T004: HealthResponse DTO in pkg/protocol/health.go
Task T005: ErrorResponse DTO in pkg/protocol/errors.go
Task T006: logging.Setup in internal/server/logging/logging.go
Task T007: config unit test in internal/server/config/config_test.go
Task T010: store integration test in internal/server/store/store_test.go
Task T011: 0001_users.sql migration
```

---

## Implementation Strategy

### MVP First (US1 + US2)

1. Phase 1 Setup → Phase 2 Foundational (compiles, migrates, exits clean).
2. Phase 3 US1 → server is alive over HTTPS, graceful shutdown.
3. Phase 4 US2 → admin bootstrapped idempotently.
4. **STOP & VALIDATE**: a runnable, admin-seeded HTTPS server with a health check — the foundation every later ROADMAP feature needs. Demo-able.

### Incremental Delivery

- Add US3 (observability) → structured request logs + secret redaction.
- Add US4 (self-protection) → global rate limiting.
- Each increment is independently testable and adds no regression to prior stories.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- Every user story is independently completable and testable (constitution Principle II).
- Verify each ⚠️ test fails before implementing.
- SQLite is exercised for real in T010/T023 — no mocking of the system boundary (constitution Principle II, research.md R1).
- Commit after each task or logical group; check the feature off in `ROADMAP.md` at merge (constitution Workflow).
- Out of scope here (later features): business endpoints/JWT (002), WireGuard (003), IPAM/nodes (004), zones/nftables (005), production .deb packaging (012).
