# Phase 1 Data Model: Invites and User Auth

Introduces one new table (`invites`) and reuses the `users` table from feature 001
(registration inserts non-admin rows). Adds no columns to `users`.

---

## Entity: Invite

A one-time onboarding token minted by an admin and consumed by exactly one
registration.

### Table `invites`

| Column             | Type    | Constraints                                              | Notes |
|--------------------|---------|----------------------------------------------------------|-------|
| `id`               | INTEGER | PRIMARY KEY AUTOINCREMENT                                 | Surrogate key. |
| `code`             | TEXT    | NOT NULL, UNIQUE                                          | 160-bit base64url random string (research.md R5). |
| `created_by_user_id` | INTEGER | REFERENCES users(id) ON DELETE SET NULL                | The admin who issued it. Nullable so listing survives creator deletion (feature 008). |
| `used_by_user_id`  | INTEGER | NULL, REFERENCES users(id) ON DELETE SET NULL            | The account that redeemed it; NULL until used. |
| `used_at`          | TEXT    | NULL                                                     | RFC3339 UTC consumption time; NULL until used. `used_at IS NULL` is the canonical "unused" predicate. |
| `created_at`       | TEXT    | NOT NULL                                                 | RFC3339 UTC issue time. |

**Indexes**: `UNIQUE(code)` provides the lookup index. `used_at` is read via the
code lookup, so no separate index is needed at this scale.

**Status is derived**, not stored: an invite is *unused* when `used_at IS NULL`,
otherwise *used*. There is no separate status column to keep consistent.

### Validation / rules

| Rule | Source | Enforced where |
|------|--------|----------------|
| `code` unique and unguessable | FR-008 | `crypto/rand` generation + `UNIQUE` index |
| redeemable at most once | FR-018 | conditional UPDATE `WHERE used_at IS NULL` + `RowsAffected==1` (research.md R2) |
| no timer expiry | FR-019 | absence of any expiry column/logic |
| listing survives creator deletion | edge case | `ON DELETE SET NULL` + LEFT JOIN on read |

### Lifecycle

```
        admin issues                      registration redeems (atomic)
 (none) ─────────────▶ unused ───────────────────────────────────────▶ used
                       used_at IS NULL        used_at, used_by set       (terminal)
```

- `used` is terminal: a used code can never return to unused (no revoke/reset in v1).

---

## Entity: User account (reused from feature 001)

No schema change. Registration inserts rows with `is_admin = 0`. The case-insensitive
`UNIQUE` username constraint from 001 enforces FR-015.

| Operation added this feature | Effect |
|------------------------------|--------|
| register | INSERT a `is_admin=0` user **inside** the redemption transaction |

The bootstrap admin (`is_admin=1`) remains the only admin; registration never sets
the admin flag.

---

## Entity: Session token (not persisted)

A signed JWT (HS256). Defined here for completeness; it is **not** a table.

| Claim        | Source | Notes |
|--------------|--------|-------|
| `sub`        | user id | Subject = account id as string. |
| `username`   | user | Private claim. |
| `is_admin`   | user | Private claim; drives `AdminRequired`. |
| `iat`        | issue time | Registered claim. |
| `exp`        | iat + `jwt_ttl` | Registered claim; the sole invalidation mechanism (FR-004). |

Signed with `cfg.Auth.JWTSecret`. Verification pins HS256 and checks `exp`
(research.md R1, R4).

---

## Migration: `0002_invites.sql`

```sql
-- +goose Up
CREATE TABLE invites (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    code               TEXT    NOT NULL UNIQUE,
    created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    used_by_user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    used_at            TEXT,
    created_at         TEXT    NOT NULL
);

-- +goose Down
DROP TABLE invites;
```

- Foreign keys are enforced (the connection enables `foreign_keys=ON` from 001).
- `ON DELETE SET NULL` keeps invites (and the ability to list them) intact when a
  referenced user is hard-deleted by feature 008.

---

## Repository surface

### `internal/server/store/invites.go` — `InviteRepo`

```go
type Invite struct {
    ID            int64
    Code          string
    CreatedByID   *int64     // nil if creator deleted
    CreatedByName *string    // joined username, nil if creator deleted
    UsedByID      *int64     // nil while unused
    UsedByName    *string    // joined username, nil while unused
    UsedAt        *time.Time // nil while unused
    CreatedAt     time.Time
}

func (s *Store) Invites() *InviteRepo
func (r *InviteRepo) Create(ctx, createdByUserID int64) (code string, err error)
func (r *InviteRepo) List(ctx) ([]Invite, error)   // newest first, LEFT JOINs for names
```

### `internal/server/store/register.go` — transactional redemption

```go
var (
    ErrInviteInvalid = errors.New("invite code invalid or already used")
    // ErrUserExists is reused from users.go
)

// Register inserts a non-admin user and consumes the invite atomically.
// Returns ErrInviteInvalid (bad/used code) or ErrUserExists (username taken),
// leaving the database unchanged on either.
func (s *Store) Register(ctx, username, passwordHash, code string) (*User, error)
```

`users.go` is unchanged except that `GetByUsername` (already present) is reused by
login. No standalone `CreateUser` is added — registration is the only insert path
and it must be transactional with redemption.

---

## Relationship view (after this feature)

```
users (001)
  │ 1                      ┌──────────────┐
  ├──< invites.created_by  │  invites     │  (this feature)
  └──< invites.used_by ───▶│  one-time    │
                           └──────────────┘
```

`nodes`, `zones`, `zone_members` are still absent — introduced by features 004–005.
