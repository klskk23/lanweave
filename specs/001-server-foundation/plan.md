# Implementation Plan: Server Foundation

**Branch**: `001-server-foundation` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-server-foundation/spec.md`

## Summary

Establish the runnable host process for the lanweave relay server: load a single
TOML configuration, open a SQLite database and bring its schema to the latest
version through an embedded migration framework, emit structured JSON logs,
serve HTTPS with a health-check endpoint and a global token-bucket rate limiter,
and — on first start — write the configured admin user into the database with an
argon2id-hashed password (idempotent on subsequent starts). No business API
(register/login/node/zone) is in scope; this feature delivers only the
infrastructure every later feature builds on.

## Technical Context

**Language/Version**: Go 1.23+ (uses `log/slog`, `net/http` method-pattern mux, `errors.Join`)

**Primary Dependencies**:
- `github.com/pelletier/go-toml/v2` — TOML config parsing (fast, maintained, strict decoding)
- `modernc.org/sqlite` — pure-Go SQLite driver (no CGO → trivial cross-compile and single static binary for .deb)
- `github.com/pressly/goose/v3` — migration runner driven by an `embed.FS` of SQL files
- `golang.org/x/crypto/argon2` — argon2id password hashing
- `golang.org/x/time/rate` — token-bucket rate limiting
- stdlib `log/slog` (JSON handler), `net/http`, `database/sql`, `crypto/tls`, `os/signal`

**Storage**: SQLite — single file at `<data_dir>/db.sqlite`. This feature creates the `users` table and the goose-managed version table only.

**Testing**: Go stdlib `testing`, table-driven. Unit tests for config validation, password hash/verify, rate-limit behavior. Integration tests against a **real** temp SQLite file for migrations + admin bootstrap idempotency. Acceptance/smoke test builds the binary and exercises it over real HTTPS (self-signed) per `quickstart.md`.

**Target Platform**: Linux server (Debian 12 / Ubuntu 22.04+), x86-64.

**Project Type**: Single Go project (server daemon). Mirrors DESIGN.md §13 skeleton, scoped to the server foundation packages.

**Performance Goals** (from constitution Principle IV):
- Cold start → first `/api/v1/healthz` 200 in ≤ 3 s.
- Sustains 1000 req/s health checks without crashing; over-limit requests rejected with 429.
- Graceful shutdown after SIGTERM in ≤ 10 s.

**Constraints**:
- HTTPS only; plaintext HTTP MUST NOT be served.
- Config loaded exactly once at startup; no scattered env reads (single documented override bridge only).
- No plaintext password, TLS key, JWT secret, or other secret in any log line.
- SQLite is the single source of truth; no runtime-only authoritative state.

**Scale/Scope**: Single server instance per host. Foundation only; designed to grow to ~1000 nodes in later features. This feature: ~8 small packages, 1 endpoint, 1 table.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Applies? | How this plan honors it |
|-----------|----------|-------------------------|
| **I. Code Quality** | Yes | One package per concern (`config`, `store`, `auth`, `api`, `logging`, `app`). Config loaded once in `app.Run`. Errors are values; the only `log.Fatal`/`os.Exit` lives at the top of `main`/`app.Run` boot path. SQLite is the sole source of truth — no in-memory authoritative state. `gofmt`/`go vet`/`staticcheck` enforced in CI. No abstraction introduced for hypothetical futures (e.g., no generic "store interface" until a second backend exists). |
| **II. Testing Standards (NON-NEGOTIABLE)** | Yes | Each of the 4 user stories gets an acceptance test. SQLite is **not** mocked — migration and bootstrap tests run on a real temp DB file. The HTTPS skeleton, health endpoint, and rate limiter are exercised over a real TLS listener. Bootstrap idempotency (US2) is proven by asserting the stored hash is byte-identical across repeated `app` boots. No WG/nftables in this feature, so those system-boundary tests are N/A here. |
| **III. UX Consistency** | **N/A** | The Windows client is the only end-user surface (constitution III); this feature is server-only and has no human UI. The one machine-facing surface — the JSON error envelope for 429 and the healthz body — is kept uniform so later features inherit a consistent shape. Recorded as N/A with that note. |
| **IV. Performance Requirements** | Yes | Cold-start, shutdown, and 1000 req/s budgets are listed above and each has a smoke test in `quickstart.md`. Pure-Go SQLite + a one-table migration keeps cold start far under 3 s. |
| **Security & Operational Discipline** | Yes | argon2id with OWASP-aligned parameters (see research.md). A dedicated test asserts no secret appears in captured log output for every code path that handles one. Input validated at the HTTP boundary (body size cap on the request reader). Root + `CAP_NET_ADMIN` narrowing is a packaging concern (feature 012); a minimal dev systemd unit is provided for local testing only. |

**Result**: PASS. No violations → Complexity Tracking table left empty.

## Project Structure

### Documentation (this feature)

```text
specs/001-server-foundation/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── health.md        # GET /api/v1/healthz contract
│   ├── errors.md        # Shared JSON error envelope (429 etc.)
│   └── config.md        # config.toml schema + validation rules
├── checklists/
│   └── requirements.md  # From /speckit-specify
└── tasks.md             # /speckit-tasks output (NOT created here)
```

### Source Code (repository root)

```text
cmd/
└── lanweaved/
    └── main.go                  # flag parsing (--config), calls app.Run, maps error → exit code

internal/
└── server/
    ├── app/
    │   ├── app.go               # Run(ctx, configPath): load → store → migrate → bootstrap → serve → graceful stop
    │   └── app_test.go          # acceptance/integration wiring tests (real DB, real TLS listener)
    ├── config/
    │   ├── config.go            # TOML structs, Load(path), Validate()
    │   └── config_test.go       # unit: required fields, CIDR parse, missing-file, bad-cert-path
    ├── logging/
    │   └── logging.go           # Setup(level) *slog.Logger (JSON handler → stdout), level parsing
    ├── store/
    │   ├── store.go             # Open(dbPath) + Migrate() via goose + embed.FS
    │   ├── users.go             # UserRepo: GetByUsername, CreateAdmin
    │   ├── migrations/          # embedded SQL
    │   │   └── 0001_users.sql
    │   └── store_test.go        # integration: real temp DB, migrate-twice idempotency, user CRUD
    ├── auth/
    │   ├── password.go          # HashPassword (argon2id, PHC string), VerifyPassword
    │   ├── bootstrap.go         # EnsureAdmin(repo, cfg): create-if-absent, skip-if-present
    │   └── auth_test.go         # unit: hash≠plaintext, salt randomness, verify; bootstrap idempotency
    └── api/
        ├── router.go            # http.ServeMux wiring, middleware chain
        ├── health.go            # GET /api/v1/healthz handler
        ├── middleware.go        # rate-limit (429), request logging, panic recovery, body-size cap
        └── api_test.go          # unit: healthz 200 shape, 429 under burst, recovery, no-secret logs

pkg/
└── protocol/
    ├── health.go                # HealthResponse DTO (shared with future client)
    └── errors.go                # ErrorResponse DTO (shared error envelope)

deploy/
└── systemd/
    └── lanweaved.dev.service    # local-dev unit only; production packaging is feature 012

config.toml.example             # documented sample config
go.mod
go.sum
```

**Structure Decision**: Single Go project. The layout is the server-side subset of
DESIGN.md §13, introducing only the packages this feature needs. `pkg/protocol`
holds the two DTOs (health + error envelope) that the future Windows client and
later server features will share, so the JSON contract is defined once. The
`app` package exists so wiring is testable end-to-end (`app.Run` is callable from
acceptance tests with a test config and a cancelable context), keeping `main.go`
to a few lines — this is justified plumbing, not speculative abstraction.

## Complexity Tracking

> No constitution violations. Table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
