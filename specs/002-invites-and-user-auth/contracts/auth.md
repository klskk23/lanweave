# Contract: Auth Endpoints

All responses use the feature-001 JSON error envelope on failure. All routes are
behind the global rate limiter.

---

## `POST /api/v1/register` (public)

Invite-gated account creation.

**Request** (`protocol.RegisterRequest`):
```json
{ "invite_code": "string", "username": "string", "password": "string" }
```

**Behavior**: validates inputs → in one transaction, inserts a non-admin user and
consumes the code (`used_at IS NULL` guard). No token is issued (the user logs in
separately).

**Responses**:
| Status | Code | When |
|--------|------|------|
| `201`  | —    | Account created. Body: `{ "username": "...", "is_admin": false }` |
| `400`  | `validation_error` | Missing/short password, empty/oversized username, missing code, malformed/oversized body |
| `409`  | `username_taken` | Username already exists (case-insensitive); code left unused |
| `422`  | `invite_invalid` | Code does not exist or already used; no account created |

**Acceptance**: US3-1..6, SC-002.

---

## `POST /api/v1/login` (public)

**Request** (`protocol.LoginRequest`):
```json
{ "username": "string", "password": "string" }
```

**Behavior**: looks up the user; verifies the password (argon2id). On unknown user,
performs a dummy verify to equalize timing (research.md R3). On success issues a
JWT (see [token.md](./token.md)).

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `{ "token": "<jwt>" }` |
| `400`  | `validation_error` | Missing username/password or malformed body |
| `401`  | `invalid_credentials` | Wrong password OR unknown username — **identical** response either way (FR-002, SC-005) |

The token value MUST NOT be logged (FR-020).

**Acceptance**: US1-1..3, SC-005.

---

## `GET /api/v1/me` (protected)

**Auth**: `Authorization: Bearer <jwt>` required.

**Behavior**: answers from the verified token claims only — no DB read (FR-007).

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `{ "user_id": 1, "username": "...", "is_admin": true }` |
| `401`  | `unauthorized` | Missing/malformed/expired/tampered token (SC-004) |

**Acceptance**: US1-4..6.
