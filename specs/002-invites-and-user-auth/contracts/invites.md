# Contract: Invite Endpoints (admin-only)

Both require `Authorization: Bearer <jwt>` for an account whose `is_admin` claim is
true. Non-admin → 403; unauthenticated → 401. Behind the global rate limiter.

---

## `POST /api/v1/admin/invites`

Generate a new one-time invite code.

**Request**: empty body.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `201`  | —    | Body: `protocol.CreateInviteResponse` `{ "code": "<base64url>" }` |
| `401`  | `unauthorized` | No/invalid token |
| `403`  | `forbidden` | Authenticated but not an admin |

The returned `code` is a secret to hand to the invitee; it MUST NOT be logged
(FR-020).

**Acceptance**: US2-1, US2-3, US2-4.

---

## `GET /api/v1/admin/invites`

List invite codes, newest first.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `{ "invites": [ InviteListItem, ... ] }` |
| `401`  | `unauthorized` | No/invalid token |
| `403`  | `forbidden` | Not an admin |

**`InviteListItem`**:
```json
{
  "code": "string",            // present so an admin can re-hand-out an unused code
  "status": "unused|used",
  "created_by": "string|null", // creator username; null if creator deleted
  "created_at": "RFC3339",
  "used_by": "string|null",    // consumer username; null if unused
  "used_at": "RFC3339|null"
}
```

Listing MUST succeed even when `created_by`/`used_by` reference a deleted user
(values become `null`), per the edge case (FR-009).

**Acceptance**: US2-2.
