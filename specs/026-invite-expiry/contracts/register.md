# Contract: Register (redeem invite)

**Endpoint**: `POST /api/v1/register` (unchanged path/auth)

## Request

Unchanged: presents an invite `code` plus the registration fields.

## Behavior change

Redemption is the existing atomic single-row claim, with one added predicate:

```sql
UPDATE invites
   SET used_by_user_id = ?, used_at = ?
 WHERE code = ?
   AND used_at IS NULL
   AND (expires_at IS NULL OR expires_at > ?)   -- NEW: ? = now() RFC3339
```

- `RowsAffected == 1` → success (unchanged).
- `RowsAffected != 1` → `ErrInviteInvalid` (unchanged error), regardless of whether
  the code was unknown, already used, **or expired**.

## Response

| Case | HTTP | Error code | Body message |
|------|------|------------|--------------|
| Success | 200 (unchanged) | — | registration result (unchanged) |
| Unknown / used / **expired** code | 422 | `invite_invalid` | `Invite code is invalid or already used.` |
| User already exists | 409 | (unchanged) | (unchanged) |

**Security invariant (FR-003 / SC-005)**: the expired case is byte-for-byte identical
to the unknown and used cases. No field, status, timing branch, or log line reveals
that a code was specifically *expired*. No new error code is introduced.

## Edge cases

- **Boundary**: strict `>` means a code is valid up to and including instants `<= expires_at`
  and invalid only once `now() > expires_at` (spec Edge Cases / FR-002).
- **Clock**: evaluated against the server clock at registration time.
- **NULL expires_at**: never matches the expiry predicate → always passes the time
  check (grandfathered + globally-disabled codes).
