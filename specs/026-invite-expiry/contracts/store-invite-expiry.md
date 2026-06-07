# Contract: Store layer — invite expiry

Behavioral contract for the SQLite store (`internal/server/store`). Constitution II:
these are exercised by real-SQLite integration tests, no mocking.

## `Invites().Create`

```go
// Was:  Create(ctx, createdByUserID int64) (code string, err error)
// Now:  Create(ctx, createdByUserID int64, ttl time.Duration) (code string, expiresAt *time.Time, err error)
```

- Generates the code as before (20-byte base64url; never logged).
- Computes `created_at = now()` (UTC, truncated to second), as before.
- If `ttl <= 0`: inserts `expires_at = NULL`, returns `expiresAt == nil`.
- If `ttl > 0`: inserts `expires_at = created_at.Add(ttl)` (RFC3339), returns a
  pointer to that instant.
- INSERT now includes the `expires_at` column.

## `Register` (redeem)

- Extends the claim `UPDATE` with `AND (expires_at IS NULL OR expires_at > ?)`,
  binding `now()` (RFC3339, same value used for `used_at`).
- On `RowsAffected != 1`, returns the **existing** `ErrInviteInvalid` — no new error
  type. Expired, unknown, and used all collapse to this one error.

## Test obligations (deterministic, no sleeps)

| Test | Setup | Assert |
|------|-------|--------|
| Unexpired redeems | insert invite with `expires_at = now()+24h` (or Create with ttl=24h) | Register succeeds; row now `used` |
| Expired rejected | insert invite with `expires_at = now()-1h` | Register → `ErrInviteInvalid`; row stays unused |
| Never-expire (NULL) redeems | insert invite with `expires_at = NULL` (or Create with ttl=0) | Register succeeds |
| Grandfathered old code | row created by 0002-shape insert with no `expires_at` (NULL after migration) | Register succeeds |
| Used beats expiry indistinguishable | redeem a valid code, redeem again | second attempt → `ErrInviteInvalid` (same as expired) |
| Create stamps expiry | `Create(ctx, admin, 24h)` | returned `expiresAt ≈ created_at+24h`; DB row matches |
| Create ttl=0 → NULL | `Create(ctx, admin, 0)` | returned `expiresAt == nil`; DB `expires_at` NULL |

All "past"/"future" expiries are set as concrete timestamps relative to `now()`; no
test waits on the wall clock.
