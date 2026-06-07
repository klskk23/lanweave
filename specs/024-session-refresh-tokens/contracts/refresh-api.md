# API Contract: session-refresh-tokens (024)

Base: `https://<server>:8443/api/v1`. Content-Type: `application/json`. Access-token routes use
`Authorization: Bearer <jwt>`. The two **new** routes are public (no bearer): they authenticate
via the refresh token (RT) in the body, because the access token is expired at refresh time.

---

## CHANGED — `POST /api/v1/login`

Authenticates and issues a session. **Response now also carries a refresh token.**

**Request** (unchanged):
```json
{ "username": "alice", "password": "correct horse battery staple" }
```

**200 OK** (changed body):
```json
{
  "token": "<access-jwt, 2h TTL>",
  "refresh_token": "<opaque base64url, ~43 chars>"
}
```

- `token` — unchanged short-lived access JWT (2h).
- `refresh_token` — opaque, high-entropy. Returned **once**; the server stores only its SHA-256
  hash. The client persists it in the keyring under `lanweave-refresh-token`.

**401 `invalid_credentials`** — unchanged (wrong username/password; constant-time, no enumeration).

**Backward compatibility**: existing clients that ignore the new field keep working (they just
won't refresh). New clients require the field to enable silent refresh.

---

## NEW — `POST /api/v1/refresh`

Exchanges a valid RT for a fresh access token and slides the RT's 30-day expiry. **Public route**
(no `Authorization` header).

**Request**:
```json
{ "refresh_token": "<opaque base64url>" }
```

**200 OK**:
```json
{ "token": "<fresh access-jwt, 2h TTL>" }
```
Side effect: the RT's `expires_at` is slid to `now + 30d`. The **same** RT remains valid (no
rotation — D4); the response does not include a new RT.

**401** (RT not ACTIVE — unknown, expired, or revoked). Body is the standard error envelope; the
client maps any 401 here to "refresh failed → clear session → password sign-in". The message MUST
NOT distinguish unknown vs expired vs revoked (no token-state oracle).

**400 `validation_error`** — missing/empty `refresh_token`, body too large, or malformed JSON
(boundary validation via the shared `decodeJSON`).

**Idempotency / concurrency**: safe to call repeatedly and concurrently with the same RT — each
call returns a fresh access token and re-slides expiry; no call invalidates another (D4).

---

## NEW — `POST /api/v1/logout`

Revokes one RT. **Public route**. Used by the client on sign-out (and by slice 025's hardened
logout) to leave no server-side session residue.

**Request**:
```json
{ "refresh_token": "<opaque base64url>" }
```

**204 No Content** — always, when the request is well-formed. Revocation is **idempotent**:
an unknown or already-revoked RT still returns 204 (no token-state oracle, and the caller can
always "log out"). After this, the RT is no longer ACTIVE and `POST /refresh` with it returns 401.

**400 `validation_error`** — missing/empty `refresh_token` or malformed/oversized body.

Note: `/logout` revokes **only** the presented RT (this device's session). It does not touch the
user's other devices' RTs and does not delete the user or any nodes.

---

## UNCHANGED — `POST /api/v1/register`

Still returns only account info; **no session, no RT**:
```json
{ "username": "alice", "is_admin": false }
```
The client calls `POST /login` immediately after to obtain `{token, refresh_token}` (D7).

---

## Cascade behavior (no new endpoint)

`DELETE /api/v1/admin/users/{id}` (existing) now also removes that user's `refresh_tokens` rows
via the `ON DELETE CASCADE` FK — every device session for the deleted user becomes unusable. No
change to the endpoint's request/response; the cascade is a schema-level effect.

---

## Client-side behavior contract (apiclient)

Not an HTTP contract, but the observable behavior every authenticated call must follow, centralized
in `apiclient.do()`:

1. Authenticated request returns **401** → if an RT is held, `POST /refresh` once (silently).
2. `/refresh` **200** → store new access token in keyring, set on client, **retry the original
   request exactly once**, return its result.
3. `/refresh` **non-200** (or no RT held) → surface `ErrSessionExpired` to the caller (which clears
   the session and routes to password sign-in). Never loop.
4. Sign-out → `POST /logout` with the held RT (best-effort), then delete `lanweave-refresh-token`
   from the keyring.
