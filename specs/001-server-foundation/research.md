# Phase 0 Research: Server Foundation

All Technical Context items were resolvable from DESIGN.md, the constitution, and
established Go ecosystem practice. No `NEEDS CLARIFICATION` markers remain. Each
decision below records the choice, why, and what was rejected.

---

## R1. SQLite driver — pure-Go vs CGO

**Decision**: `modernc.org/sqlite` (pure Go, registered as database/sql driver `"sqlite"`).

**Rationale**:
- DESIGN.md §10 ships a single static binary in a `.deb`. CGO (`mattn/go-sqlite3`)
  forces a C toolchain at build time and complicates reproducible cross-compiles;
  pure Go keeps `CGO_ENABLED=0` and a fully static artifact.
- Performance is more than adequate for a single-instance control plane whose
  hot path is small auth'd reads, not analytical queries.
- Constitution Principle I values reversibility and low operational surprise; no
  C dependency removes a whole class of build/runtime variance.

**Alternatives considered**:
- `mattn/go-sqlite3` (CGO): fastest, most battle-tested, but reintroduces CGO and
  the cross-compile pain we are explicitly avoiding.
- Embedded KV (bbolt) / Postgres: rejected — DESIGN.md §1 fixes SQLite as the store.

**Notes for implementation**:
- Open with DSN pragmas: `_pragma=busy_timeout(5000)`, `_pragma=journal_mode(WAL)`,
  `_pragma=foreign_keys(ON)`. WAL improves concurrent read/write; `busy_timeout`
  turns transient lock contention into a short wait instead of an immediate error
  (relevant to the "DB locked" edge case — but a *persistent* external lock still
  surfaces as a startup error, per spec).
- Set `db.SetMaxOpenConns(1)` is **not** required with WAL; allow the default pool
  but ensure writes are serialized by SQLite itself. Keep it simple: rely on WAL +
  busy_timeout. (Revisit only if a real contention bug appears — no premature tuning.)

---

## R2. Migration framework

**Decision**: `github.com/pressly/goose/v3` driven by an `embed.FS` of `.sql` files under `internal/server/store/migrations/`.

**Rationale**:
- Migrations compile into the binary (`//go:embed`), so the `.deb` carries them; no
  separate migration files to deploy (satisfies FR-006 "carries a migration framework").
- goose records applied versions in its own table and runs only pending migrations
  in order — directly satisfies FR-006 (idempotent, ordered) and the half-applied
  recovery edge case (goose wraps each migration in a transaction where the DB
  supports it; a crashed run re-runs the un-recorded migration).
- Plain SQL up-migrations keep the schema obvious and reviewable (Principle I).

**Alternatives considered**:
- `golang-migrate/migrate`: comparable; chosen against only because goose's
  library-mode API + embed.FS integration is marginally simpler for a single binary.
- Hand-rolled migration runner: rejected — reinventing a solved problem violates
  "no premature/unnecessary code" and would still need its own test surface.

**Notes for implementation**:
- Use goose library mode: `goose.SetBaseFS(embedFS); goose.SetDialect("sqlite3"); goose.Up(db, "migrations")`.
- One migration this feature: `0001_users.sql` (see data-model.md).
- Each migration runs and is logged individually (FR-018).

---

## R3. Password hashing parameters (argon2id)

**Decision**: `golang.org/x/crypto/argon2`, `argon2id`, encoded as a PHC string
(`$argon2id$v=19$m=...,t=...,p=...$<b64salt>$<b64hash>`).

**Parameters** (OWASP "argon2id" minimum, second configuration):
- memory `m = 19456` KiB (19 MiB)
- iterations `t = 2`
- parallelism `p = 1`
- salt = 16 random bytes from `crypto/rand`
- key length = 32 bytes

**Rationale**:
- OWASP Password Storage Cheat Sheet lists `m=19MiB, t=2, p=1` as an accepted
  argon2id configuration; it balances brute-force resistance against the cold-start
  budget (one hash at boot is sub-100 ms with these params, far inside the 3 s budget).
- PHC string is self-describing: it embeds algorithm, version, params, and salt, so
  verification needs no out-of-band parameter storage and future param changes stay
  backward-compatible (FR-014 random salt, FR-015 stable stored hash).
- Constitution Security section mandates argon2id at OWASP guidance — direct match.

**Alternatives considered**:
- bcrypt (`golang.org/x/crypto/bcrypt`): allowed by DESIGN.md §4.3 as a fallback,
  but argon2id is the constitution's named primary and resists GPU attacks better.
- scrypt: viable but argon2id is the modern consensus and explicitly named.

**Notes for implementation**:
- Provide `HashPassword(plain string) (string, error)` returning the PHC string and
  `VerifyPassword(plain, phc string) (bool, error)` that parses params from the PHC
  string and uses `subtle.ConstantTimeCompare`.
- A unit test asserts two hashes of the same plaintext differ (random salt) and both
  verify true — proves FR-014 and SC-006.

---

## R4. Structured logging

**Decision**: stdlib `log/slog` with `slog.NewJSONHandler(os.Stdout, …)`.

**Rationale**:
- Stdlib (Go 1.21+) — no dependency, JSON output is machine-parseable with
  `time`, `level`, `msg` keys by default, satisfying FR-017 and SC-005.
- journald collects stdout per DESIGN.md §10.6; no file rotation needed in-process.
- `slog`'s leveled handler with a configurable `slog.LevelVar` satisfies FR-020
  (debug/info/warn/error switchable from config).

**Alternatives considered**:
- `zerolog` / `zap`: faster, but stdlib slog meets the throughput need and adds
  zero dependency surface (Principle I — no unnecessary deps).

**Notes for implementation**:
- `logging.Setup(level string) *slog.Logger` parses the level and installs the
  logger as `slog.Default()` so all packages log consistently.
- **Secret hygiene (FR-019)**: never pass a secret as a log attribute. The config
  struct's secret fields get a `LogValue() slog.Value` returning `"[REDACTED]"`, and
  an `api_test.go` case scans captured output to assert no known secret string leaks.

---

## R5. HTTP server, routing, and graceful shutdown

**Decision**: stdlib `net/http` with the Go 1.22 method+pattern `ServeMux`
(`mux.HandleFunc("GET /api/v1/healthz", …)`), `http.Server` with `TLSConfig`, and
`Server.Shutdown(ctx)` on signal.

**Rationale**:
- One endpoint and a middleware chain need no framework; stdlib mux now supports
  method-scoped patterns, removing the historical reason to pull in chi/gin.
- `Server.Shutdown` drains in-flight requests within a deadline — satisfies FR-011
  and the ≤10 s budget (SC-003); we pass a 10 s context.
- TLS-only is enforced by serving exclusively via `ServeTLS`/`ListenAndServeTLS`;
  no plaintext listener is ever opened (FR-009).

**Alternatives considered**:
- chi / gin / echo: rejected for this scope — unnecessary dependency.

**Notes for implementation**:
- `http.Server` fields: `ReadHeaderTimeout` (mitigate slowloris), `ReadTimeout`,
  `WriteTimeout`, `IdleTimeout` all set to sane defaults.
- TLS: `tls.LoadX509KeyPair(cert, key)`; a load failure aborts startup with a clear
  error and no listener (FR-012, US1 scenario 3). Minimum TLS 1.2.
- Signal handling via `signal.NotifyContext(ctx, SIGINT, SIGTERM)`; cancellation
  triggers `Shutdown`. `app.Run` returns when the server has stopped.

---

## R6. Rate limiting

**Decision**: process-global token bucket via `golang.org/x/time/rate.Limiter`,
applied as the outermost middleware on all `/api/...` routes.

**Rationale**:
- DESIGN.md §7.4 and FR-021..024 call for a single global limiter in MVP (not
  per-account). `x/time/rate` is the canonical implementation.
- A single shared `*rate.Limiter` with `rate = cfg.RateLimit.RPS`,
  `burst = cfg.RateLimit.Burst` gives the documented default (100 rps / burst 200)
  when config omits them (FR-022).
- On `!limiter.Allow()` respond `429` with the shared error envelope and a
  `Retry-After` header (FR-023). Health endpoint is **not** exempt (spec decision),
  so the limiter wraps it too — quickstart's load test accounts for this.

**Alternatives considered**:
- Per-IP limiter map: deferred to v1.1 (DESIGN.md §7.4); the framework leaves an
  extension point (middleware takes a "key function") but this feature wires only
  the global key.
- `golang.org/x/time/rate` vs `uber-go/ratelimit`: x/time/rate is already an
  indirect stdlib-adjacent dependency and supports burst; chosen.

**Notes for implementation**:
- Expose `RateLimitMiddleware(limiter *rate.Limiter) func(http.Handler) http.Handler`
  so later features can compose additional limiters without rewriting this one
  (FR-024 extension point) — but no path-level differentiation is built now.

---

## R7. TOML config loading & validation

**Decision**: `github.com/pelletier/go-toml/v2` with strict decoding
(`toml.Decoder.DisallowUnknownFields()` optional; start permissive, log unknown keys).

**Rationale**:
- Mature, fast, well-maintained; decodes directly into typed structs.
- Validation is explicit Go code in `config.Validate()` — required-field checks,
  `net.ParseCIDR` for the WG network, `os.Stat` readability checks for cert/key/
  data_dir — satisfying FR-002, FR-003 and the "missing field / bad path" scenarios.

**Alternatives considered**:
- `BurntSushi/toml`: equally fine; pelletier/v2 chosen for its faster decoder and
  active maintenance. Low-stakes, reversible choice.

**Notes for implementation**:
- A single override bridge (Principle I): only `LANWEAVE_CONFIG` env may redirect the
  config path if `--config` is absent; no other env reads anywhere in the codebase.
- `Validate()` returns a joined error (`errors.Join`) listing *all* problems at once,
  so the operator fixes the file in one pass rather than iteratively.

---

## R8. What is deliberately deferred (scope guard)

To keep this feature a true foundation (and honor "no unnecessary code"):
- **No** business endpoints, JWT issuance, or session logic — `auth.jwt_secret` and
  `auth.jwt_ttl` are only *validated present* here; feature 002 consumes them.
- **No** WireGuard or nftables code — `wireguard.network` is only *validated as a CIDR*;
  feature 003/004/005 consume it.
- **No** production packaging — feature 012. A dev-only systemd unit is included
  purely so the acceptance test / operator can run the binary as a service locally.
- **No** per-IP/per-account rate limiting — v1.1.
- **No** TLS auto-provisioning (Let's Encrypt) — v1.1; operator supplies cert/key paths.
