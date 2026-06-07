# Contract: `POST /api/v1/register` with password policy

Extends the existing register endpoint (slice 002). Only the password-validation
behavior changes; invite handling, username rules, and account creation are unchanged.

## Request (unchanged shape)

```json
{ "invite_code": "…", "username": "…", "password": "…" }
```

## Validation order (server)

The handler validates in this order; the **first** failure wins and short-circuits:

1. Body decodes → else `400 validation_error` "Invalid request body."
2. `username` trimmed, 1–64 chars → else `400 validation_error` "Username must be 1-64 characters."
3. **Password policy** (`passwordpolicy.Validate`) → else `400 validation_error` with
   the reason-specific English message below. *(Replaces the old `len < 8` check.)*
4. `invite_code` non-empty → else `400 validation_error` "An invite code is required."
5. Store `Register` (invite redemption, uniqueness) → existing `422 invite_invalid` /
   `409 username_taken`.

> The password check sits where the old length-only check sat (before the invite
> presence check), preserving existing ordering for the other fields.

## Password rejection messages (English, hardcoded)

All use HTTP `400` and error code `validation_error`:

| Reason | Message |
|--------|---------|
| `ReasonCharset` | `Password may only contain ASCII letters, digits, and symbols (no spaces).` |
| `ReasonTooShort` | `Password must be 8-64 characters.` |
| `ReasonTooLong` | `Password must be 8-64 characters.` |
| `ReasonNoUpper` | `Password must include an uppercase letter, a lowercase letter, and a digit.` |
| `ReasonNoLower` | `Password must include an uppercase letter, a lowercase letter, and a digit.` |
| `ReasonNoDigit` | `Password must include an uppercase letter, a lowercase letter, and a digit.` |

Messages MUST NOT echo the submitted password. (Short/long collapse to one message;
the three class reasons collapse to one — the server need not be as granular as the
client, which uses the typed reason for precise localized feedback.)

## Success

Unchanged: compliant password → account created → existing success response.

## Out of scope (unchanged behavior)

- `POST /api/v1/login` performs **no** policy check — pre-policy passwords still
  authenticate.
- Zone create / change-zone-password (`minZonePasswordLen`) unchanged.
- Bootstrap admin password (config TOML) unchanged.

## Test obligations

- Acceptance (real SQLite, `unshare -rUn`): each rejection reason → `400`
  `validation_error`, **no account created**, invite **not** consumed; a compliant
  password → account created.
- A login with a known weak pre-existing password still succeeds (guards FR-009).
- Secrets-in-logs assertion: rejection path logs no password material.
