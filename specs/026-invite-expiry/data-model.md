# Phase 1 Data Model: Invite Code Expiry

## Entity: Invite code (existing — `invites` table, slice 002)

The existing one-time, admin-issued credential gains one optional column.

| Column | Type | Nullable | Notes |
|--------|------|----------|-------|
| id | INTEGER PK | no | unchanged |
| code | TEXT | no | unique; 20-byte base64url; MUST NOT be logged (unchanged) |
| created_by_user_id | INTEGER | no | admin who issued it (unchanged) |
| used_by_user_id | INTEGER | yes | NULL until redeemed (unchanged) |
| used_at | TEXT (RFC3339) | yes | NULL until redeemed (unchanged) |
| created_at | TEXT (RFC3339) | no | issuance time (unchanged) |
| **expires_at** | **TEXT (RFC3339)** | **yes** | **NEW. NULL = never expires.** |

### `expires_at` semantics

- **NULL** = never expires. This is the value for:
  - rows created before this feature (the `ADD COLUMN` migration leaves them NULL → grandfathered, FR-006/FR-007);
  - codes created while `invite_ttl` is `0`/empty (global disable, FR-005).
- **Non-NULL** = the RFC3339 UTC instant, truncated to the second, at which the code
  stops being redeemable. Computed at creation as `created_at + invite_ttl` (FR-001).

### Migration (goose `0006_invite_expires.sql`)

```sql
-- +goose Up
ALTER TABLE invites ADD COLUMN expires_at TEXT;

-- +goose Down
ALTER TABLE invites DROP COLUMN expires_at;
```

Additive, nullable, no backfill — existing rows are NULL (never-expire) automatically.

### State / validity transitions

A code is redeemable iff **all** hold (evaluated atomically in the register `UPDATE`):

1. `used_at IS NULL` (not already redeemed) — existing rule, FR-008.
2. `expires_at IS NULL OR expires_at > now()` (not expired) — new rule, FR-002.

Precedence is irrelevant to the registrant: a code failing either predicate yields the
same generic rejection (FR-003). "Used" and "expired" are not distinguished.

```
created (used_at=NULL, expires_at=X|NULL)
   │
   ├── now() > X (X non-NULL) ──▶ expired  ─┐
   ├── register succeeds ───────▶ used      ─┼─▶ all reject with generic ErrInviteInvalid
   └── unknown code ────────────▶ (no row)  ─┘
```

Expired rows are retained, never auto-deleted (FR-010).

## Entity: Invite expiry setting (config — `AuthConfig.InviteTTL`)

A single global value in the `[auth]` config section.

| Property | Value |
|----------|-------|
| Key | `invite_ttl` (TOML, under `[auth]`) |
| Type | duration string (e.g. `"24h"`, `"720h"`) |
| Empty / absent | parses to zero duration → never expire (write NULL); **no code-level default** |
| `config.toml.example` ships | `invite_ttl = "24h"` (开箱启用) |
| Validation | non-empty MUST parse via duration parsing; a negative duration MUST fail startup (FR-011); empty is valid (= disabled) |
| Lifecycle | read once at startup, resolved to `time.Duration`, carried in `api.Options.InviteTTL`; no hot reload |

### Resolution rule (startup, in `app.go`)

```
raw := cfg.Auth.InviteTTL          // string
if raw == "" { d = 0 }             // never expire
else         { d = ParseDuration(raw) }   // validated earlier; negative already rejected
Options.InviteTTL = d
```

### Application rule (creation, in store `Invites().Create`)

```
if ttl <= 0 { expires_at = NULL }
else        { expires_at = created_at.Add(ttl) }   // RFC3339 UTC, truncated to second
```

The created `expires_at` (or nil) is returned up the call chain so the admin surface
can report it (FR-009).
