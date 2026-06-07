# Data Model: session-refresh-tokens (024)

## New table: `refresh_tokens`

Migration `internal/server/store/migrations/0005_refresh_tokens.sql`.

| Column       | Type    | Constraints                                              | Meaning |
|--------------|---------|---------------------------------------------------------|---------|
| `id`         | INTEGER | PRIMARY KEY AUTOINCREMENT                                | Surrogate key. |
| `user_id`    | INTEGER | NOT NULL, REFERENCES `users(id)` ON DELETE CASCADE      | Owning user; cascade deletes RTs when the user is removed. |
| `token_hash` | TEXT    | NOT NULL UNIQUE                                          | Lowercase-hex SHA-256 of the opaque plaintext RT. The plaintext is **never** stored. |
| `expires_at` | TEXT    | NOT NULL                                                | RFC 3339 UTC. Sliding: set to `now+30d` at issue and on each successful refresh. |
| `revoked_at` | TEXT    | NULL                                                    | RFC 3339 UTC when revoked; NULL = active. Revocation is idempotent. |
| `created_at` | TEXT    | NOT NULL                                                | RFC 3339 UTC at issuance. |

Index: `UNIQUE` on `token_hash` (the lookup key for validate/revoke) is sufficient; `user_id`
lookups during cascade are handled by the FK. Timestamps stored as RFC 3339 text to match the
existing convention (`users.created_at`, `nodes.created_at`).

### Why these columns

- **Hash, not plaintext** (D2): a DB dump must not yield usable tokens.
- **`revoked_at` nullable, not a boolean** (D8): records *when* for audit and keeps revoke
  idempotent (`UPDATE … SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`).
- **`expires_at` as a stored value, not derived** (D3): sliding requires mutating it per refresh.

## RT lifecycle (states)

```
            Issue(userID)                     Validate(rt) on /refresh
  (none) ───────────────▶ ACTIVE ───────────────────────────────────▶ ACTIVE
                            │  (revoked_at NULL,        slide expires_at = now+30d
                            │   expires_at > now)
                            │
            ┌───────────────┼────────────────────┐
            │ Revoke(rt) / logout                 │ time passes, expires_at ≤ now
            ▼                                     ▼
         REVOKED                               EXPIRED
   (revoked_at set;                      (revoked_at NULL but
    Validate → invalid)                   expires_at ≤ now; Validate → invalid)

  Delete user ──▶ row removed via FK cascade (terminal, for every state)
```

`Validate` returns the owning `userID` only for an ACTIVE token; REVOKED, EXPIRED, and unknown
all return `ErrRefreshInvalid` (the handler maps that to 401). Validation is read + conditional
update in one path under SQLite's single-writer lock.

## Store API (`RefreshTokenRepo`, new `store/refresh_tokens.go`)

Mirrors the `NodeRepo`/`ZoneRepo` shape; obtained via `store.RefreshTokens()`.

| Method | Signature (conceptual) | Behavior |
|--------|------------------------|----------|
| `Issue` | `Issue(ctx, userID int64) (plaintextRT string, err error)` | Generate `crypto/rand` 32B → base64url; insert `(user_id, sha256(plaintext), now+30d, NULL, now)`; return the **plaintext** (caller hands it to the client; it is never returned again). |
| `Validate` | `Validate(ctx, plaintextRT string) (userID int64, err error)` | Look up by `token_hash`; if missing/`revoked_at` set/`expires_at ≤ now` → `ErrRefreshInvalid`; else slide `expires_at = now+30d` and return `user_id`. |
| `Revoke` | `Revoke(ctx, plaintextRT string) error` | `UPDATE … SET revoked_at = now WHERE token_hash = ? AND revoked_at IS NULL`; idempotent (unknown/already-revoked → no error). |

- Time source is an injectable `now func() time.Time` (default `time.Now().UTC`) so expiry/slide
  are tested deterministically without sleeps (Constitution II: no flaky tests).
- Token generation + hashing live in `internal/server/auth/refreshtoken.go`
  (`GenerateRefreshToken`, `HashRefreshToken`) next to `jwt.go`, keeping token crypto in `auth`.
- New sentinel `ErrRefreshInvalid` (store) — the only error the handler needs to distinguish
  (→ 401). Infra failures return wrapped errors (→ 500), as elsewhere.

## Wire entities (`pkg/protocol/auth.go`)

| Type | Change |
|------|--------|
| `LoginResponse` | **add** `RefreshToken string \`json:"refresh_token"\`` (alongside existing `Token`). |
| `RefreshRequest` | **new**: `{ RefreshToken string \`json:"refresh_token"\` }` |
| `RefreshResponse` | **new**: `{ Token string \`json:"token"\` }` (fresh access token only; no new RT — D4). |
| `LogoutRequest` | **new**: `{ RefreshToken string \`json:"refresh_token"\` }` |

`RegisterResponse` is **unchanged** (D7). No new error envelope codes are required: `/refresh`
failures use the existing 401 path (`invalid_credentials`/`unauthorized`-style envelope already
mapped to `ErrSessionExpired` on the client); `/logout` always returns 204.

## Client-held entities

| Where | Change |
|-------|--------|
| `keyring` | **new** `RefreshTokenName = "lanweave-refresh-token"` (DPAPI-protected on Windows, same backend as `SessionTokenName`/`DeviceKeyName`). |
| `apiclient.Client` | **new** unexported `refreshToken string` field + `SetRefreshToken`/`RefreshToken` accessors (mirrors `token`/`SetToken`/`Token`). |
| state file | **no change** — the RT is a secret and lives only in the keyring, never in `state.json`. |

## Relationships & invariants

- `refresh_tokens (N) ── (1) users` via `user_id` FK with `ON DELETE CASCADE` (same pattern as
  `nodes.user_id`, `zones.owner_user_id`), so `users.DeleteCascade` removes a user's RTs with no
  new code in that method — the FK does it inside the existing transaction.
- One device may hold one active RT at a time in practice (the client overwrites its stored RT on
  each login/refresh), but the schema does not cap rows per user — multiple logins simply create
  multiple ACTIVE rows, each independently revocable. This is intentional and harmless.
- **Invariant**: the plaintext RT exists in exactly two places — the client keyring and, transiently,
  the issuing HTTP response. The server persists only its hash.
- **Invariant**: access JWT issuance/verification (`auth.JWTManager`) is unchanged; an access token
  obtained via `/refresh` is byte-for-byte the same kind of token as one from `/login`.
