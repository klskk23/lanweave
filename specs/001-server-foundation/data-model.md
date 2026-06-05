# Phase 1 Data Model: Server Foundation

This feature introduces exactly one application table (`users`) plus the
migration-framework's own bookkeeping table. Every later entity (invites, nodes,
zones, zone_members) is introduced by its own feature's migration, never here.

---

## Entity: User

The authenticated principal. This feature only ever writes one row (the bootstrap
admin), but the schema is the final shape used by feature 002+ for invited users.

### Table `users`

| Column          | Type    | Constraints                              | Notes |
|-----------------|---------|------------------------------------------|-------|
| `id`            | INTEGER | PRIMARY KEY AUTOINCREMENT                | Surrogate key. |
| `username`      | TEXT    | NOT NULL, UNIQUE, COLLATE NOCASE         | Login handle. Case-insensitive uniqueness prevents `Alice`/`alice` collisions. |
| `password_hash` | TEXT    | NOT NULL                                 | argon2id PHC string (see research.md R3). Never the plaintext. |
| `is_admin`      | INTEGER | NOT NULL DEFAULT 0                       | Boolean 0/1. Bootstrap admin = 1. |
| `created_at`    | TEXT    | NOT NULL                                 | RFC3339 UTC timestamp, e.g. `2026-06-05T12:30:00Z`. Stored as text for sqlite-CLI readability (Principle I: obviousness). |

**Indexes**: the `UNIQUE` on `username` creates the only index needed this feature.

### Validation rules

| Rule | Source | Enforced where |
|------|--------|----------------|
| `username` non-empty, ≤ 64 chars | derived | `auth.EnsureAdmin` before insert; later, registration handler (feature 002) |
| `username` unique (case-insensitive) | FR-008 | DB `UNIQUE COLLATE NOCASE` + handled insert error |
| `password_hash` is a valid argon2id PHC string | FR-014 | produced only by `auth.HashPassword`; never raw input |
| plaintext password never persisted or logged | FR-014, FR-019, constitution Security | code review + log-scan test |

### State / lifecycle (this feature)

```
            first start, admin absent          subsequent starts, admin present
  (no row) ───────────────────────────▶ (admin row) ──────────────────────────▶ (unchanged)
                 INSERT is_admin=1                       SELECT → skip, no write
```

- No update or delete paths exist in this feature. `EnsureAdmin` only ever does
  `SELECT by username` then conditionally `INSERT`. It MUST NOT `UPDATE` an existing
  row (FR-015): the stored hash is immutable across restarts even if the TOML
  plaintext changes. (SC-007 asserts byte-identical hash across 10 restarts.)

### Repository surface (`internal/server/store/users.go`)

```go
type User struct {
    ID           int64
    Username     string
    PasswordHash string
    IsAdmin      bool
    CreatedAt    time.Time
}

// GetByUsername returns (nil, nil) when no row matches — absence is not an error.
func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*User, error)

// CreateAdmin inserts a new admin row. Returns a typed "already exists" error if
// the unique constraint trips (race-safe: SELECT-then-INSERT may race; the INSERT
// is the authority).
func (r *UserRepo) CreateAdmin(ctx context.Context, username, passwordHash string) (*User, error)
```

Only these two methods exist this feature. No `Update`, `Delete`, `List` — they are
added by the features that need them (Principle I: no speculative surface).

---

## Framework table: goose version tracking

### Table `goose_db_version` (managed by goose, not by application code)

| Column       | Type    | Notes |
|--------------|---------|-------|
| `id`         | INTEGER | PK, goose-managed |
| `version_id` | INTEGER | Migration number applied |
| `is_applied` | INTEGER | goose bookkeeping |
| `tstamp`     | TIMESTAMP | When applied |

- Created automatically on first `goose.Up`. The application never reads or writes
  it directly; it exists so migrations run exactly once and in order (FR-006).

---

## Migration: `0001_users.sql`

```sql
-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL
);

-- +goose Down
DROP TABLE users;
```

- A `Down` is provided for completeness/dev resets; production never auto-downgrades.

---

## Relationship to future features (informational, not built here)

```
users (this feature)
  │  1
  │
  ├──< nodes          (feature 004: user_id FK, (user_id,name) unique, ip unique)
  ├──< zones          (feature 005: owner_user_id FK, name globally unique)
  └──< invites        (feature 002: created_by_user_id, used_by_user_id FKs)

zones 1 ──< zone_members >── nodes  (feature 005: M:N, PK (zone_id,node_id))
```

The foreign keys above are **not** added in this feature. They arrive with their
owning feature's migration so each slice stays independently testable and the
`users` table here carries no forward references.
