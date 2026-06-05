# Phase 0 Research: Invites and User Auth

Most context is fixed by DESIGN.md, the constitution, and the feature-001
codebase. No `NEEDS CLARIFICATION` markers remain. Decisions below.

---

## R1. JWT library vs hand-rolled HS256

**Decision**: `github.com/golang-jwt/jwt/v5`, restricted to HS256.

**Rationale**:
- DESIGN §7.3 specifies stateless JWT, HS256. golang-jwt v5 is the de-facto
  community-standard, audited implementation and handles `exp`/`iat`/`nbf`
  validation and base64url encoding correctly.
- The constitution's Security section says "no new primitives; use vetted
  community packages." Hand-rolling JWT parsing invites classic mistakes
  (algorithm-confusion, `alg:none`, non-constant-time compares). A vetted library
  is the lower-risk choice — the same reasoning that picked goose over a hand-rolled
  migrator in feature 001.
- We pin the accepted algorithm on verify with
  `jwt.WithValidMethods([]string{"HS256"})`, so a token claiming a different `alg`
  is rejected before the key is used (defeats HS/RS confusion).

**Alternatives considered**:
- Hand-rolled HS256 with `crypto/hmac` (~50 lines): smaller dep graph, but more
  security surface to own and test. Rejected on the "don't reinvent solved,
  security-sensitive problems" principle.
- PASETO: arguably safer-by-design, but DESIGN explicitly says JWT/HS256 and the
  future client ecosystem expects JWT.

**Notes**:
- Claims: `RegisteredClaims` with `Subject` = user id (string), `IssuedAt`,
  `ExpiresAt` = now + `jwt_ttl`; plus private claims `username` (string) and
  `is_admin` (bool).
- `JWTManager{secret []byte, ttl time.Duration}` with `Issue(Claims)` and
  `Verify(token) (*Claims, error)`. Secret comes from `cfg.Auth.JWTSecret.Reveal()`;
  ttl from parsing `cfg.Auth.JWTTTL`.

---

## R2. One-time invite redemption under concurrency

**Decision**: Single SQLite transaction in `Store.Register`: insert the user, then
`UPDATE invites SET used_by_user_id=?, used_at=? WHERE code=? AND used_at IS NULL`
and require `RowsAffected() == 1`; otherwise roll back.

**Rationale**:
- The conditional `WHERE used_at IS NULL` + `RowsAffected` check is the atomic
  guard: SQLite serializes writers, so among concurrent redemptions of one code
  exactly one UPDATE matches a still-unused row; the rest match zero rows and roll
  back (FR-018, SC-002). No application-level locking needed.
- Username uniqueness is enforced by the existing `UNIQUE COLLATE NOCASE` index;
  a duplicate insert returns `ErrUserExists` and the transaction rolls back, leaving
  the code unused (FR-015).
- Ordering: validate the code is present/unused with an initial `SELECT` to produce
  a clean "invalid code" error early (FR-014); the authoritative guarantee is still
  the conditional UPDATE's `RowsAffected`, which closes the check-then-act race.

**Concurrency mechanics**:
- The DB is opened (feature 001) with `busy_timeout=5000` and WAL, so a writer that
  briefly contends the write lock waits rather than failing with `SQLITE_BUSY`.
- `BEGIN IMMEDIATE` would grab the write lock up front and avoid a deferred-to-write
  upgrade; modernc honors the `_txlock=immediate` DSN parameter. **Decision**: add
  `_txlock=immediate` to the DSN so registration transactions take the write lock
  on `BeginTx`, making contention deterministic. (Read-only paths are unaffected in
  practice for this single-writer workload.)

**Alternatives considered**:
- `SELECT ... FOR UPDATE`: not supported by SQLite.
- Application mutex: rejected — doesn't survive multiple processes and is weaker
  than letting the database be the authority.

---

## R3. No user enumeration on login

**Decision**: Login returns one generic `invalid_credentials` (401) for both
unknown-username and wrong-password. When the username is unknown, perform a
**dummy argon2id verify** against a fixed precomputed hash so response timing does
not reveal account existence.

**Rationale**:
- FR-002 / SC-005 require status and message to be indistinguishable. The dummy
  verify equalizes the dominant timing cost (argon2id) between the two paths,
  closing the timing side channel cheaply.

**Notes**:
- Add `auth.DummyVerify(password string)` that runs `VerifyPassword` against a
  package-level constant PHC hash and discards the result. Called when
  `GetByUsername` returns no row.

---

## R4. Identity propagation without an extra DB read

**Decision**: `AuthRequired` middleware verifies the bearer token, builds an
`auth.Claims`, and stores it in the request context. Handlers and `AdminRequired`
read identity from context. `/me` answers purely from the token claims (FR-007).

**Rationale**:
- The token already carries `user id`, `username`, `is_admin`; trusting the
  verified signature means no DB round-trip is needed to authenticate or to answer
  `/me`, meeting the ≤100 ms read budget and keeping the hot path DB-free.
- A typed context key (`type ctxKey struct{}`) avoids string-key collisions.

**Trade-off acknowledged**: because `/me` and admin checks trust the token, an
account whose admin flag changes mid-token-lifetime keeps its old privileges until
the token expires (≤ ttl). This is consistent with the stateless, no-revocation
model (FR-004) and acceptable for v1.

---

## R5. Invite code generation

**Decision**: 160 bits of `crypto/rand`, base64url (no padding) → a 27-char
URL-safe string.

**Rationale**:
- 160 bits is infeasible to guess or enumerate (FR-008); base64url is safe to paste
  into a URL or config and to display. `crypto/rand` is the only acceptable source.
- Stored as-is in `invites.code` with a `UNIQUE` constraint; the astronomically low
  collision probability is still guarded by the unique index (regenerate on the rare
  conflict).

**Alternatives considered**:
- Hashing the code at rest (like a password): rejected for v1 — invite codes are
  low-value, short-lived-by-use secrets; the operational simplicity of storing the
  value (so an admin can re-read/redisplay a still-unused code when listing)
  outweighs the marginal benefit. Listing returns the code value for unused invites
  so the admin can hand it out; this is an accepted, documented choice.

---

## R6. Registration input validation

**Decision**: username non-empty, trimmed, ≤ 64 chars (matches the feature-001
admin limit); password ≥ 8 characters. Constants in code, not config, for v1.

**Rationale**:
- Reuses the account-length limit already validated in 001. The 8-char minimum is
  the spec's documented default; making it configurable is a future hook, not a v1
  need (avoids touching 001's config + validation for no current requirement).
- Validation happens at the handler boundary; the store assumes validated input.

---

## R7. Request body limits & decoding

**Decision**: Wrap request bodies with `http.MaxBytesReader` (e.g., 16 KiB) and
decode with a `json.Decoder` configured via `DisallowUnknownFields()`.

**Rationale**:
- Bounds memory at the boundary (constitution Security: validate at boundaries) and
  rejects malformed/oversized payloads with a clean `validation_error` rather than
  an unbounded read. Unknown-field rejection catches client mistakes early.

---

## R8. Routing & middleware composition (building on 001)

**Decision**: Keep the global chain from 001 (RequestLogger → Recoverer →
RateLimit → mux). Apply `AuthRequired` (and `AdminRequired` after it) as per-route
wrappers on protected handlers only.

**Rationale**:
- Public routes (`/login`, `/register`, `/healthz`) stay unauthenticated but remain
  rate-limited by the existing global limiter (FR-021). Protected routes (`/me`,
  `/admin/invites`) opt into auth by wrapping. This composes cleanly with the
  stdlib method-pattern mux and needs no router framework.
- `api.NewRouter` Options grows to carry the dependencies handlers need
  (`*store.Store`, `*auth.JWTManager`, `*slog.Logger`); `app.Run` constructs them.

---

## R9. What is deliberately deferred (scope guard)

- **No** account-level lockout / failed-attempt counting — v1.1 (global limiter only).
- **No** invite expiry, revocation, or multi-use — not planned (DESIGN §7.1).
- **No** open / email-verified registration, **no** new admins via registration.
- **No** token refresh or server-side revocation list — DESIGN §7.3.
- **No** password-change / account-management endpoints — later/other features.
- **No** WireGuard, node, zone, or IPAM behavior — features 003–005.
