# Contract: Zone Owner Operations

All require `Authorization: Bearer <jwt>` and the caller to be the zone's **owner**.
Shared error envelope + global rate limiter. Non-owner → 403; missing zone → 404.

---

## `PATCH /api/v1/zones/{name}` — change password

**Request** (`protocol.ChangeZonePasswordRequest`):
```json
{ "password": "a-new-strong-zone-password" }
```

**Behavior**: owner gate → validate new password → update the stored hash. Members are
NOT ejected; only future joins are governed by the new password.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Password changed (existing members keep membership) |
| `400`  | `validation_error` | Password too short / malformed body |
| `401`  | `unauthorized` | No/invalid token |
| `403`  | `forbidden` | Authenticated, but not the zone owner |
| `404`  | `not_found` | No such zone |

**Acceptance**: US1, SC-001/004.

---

## `DELETE /api/v1/zones/{name}/members/{node_id}` — kick member

**Behavior**: owner gate → resolve the member node's address (any owner's node) →
remove the membership → remove the node's address from the zone's set. The node record
and its other zones are unaffected.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `204`  | —    | Kicked; the node lost reachability within this zone |
| `401`  | `unauthorized` | No/invalid token |
| `403`  | `forbidden` | Not the zone owner |
| `404`  | `not_found` | No such zone, no such node, OR the node is not a member |

**Acceptance**: US2, SC-002/004.

---

## `DELETE /api/v1/zones/{name}` — delete zone

**Behavior**: owner gate → delete the zone (cascades all memberships) → destroy the
zone's set + accept rule → the name is released. Member nodes are NOT deleted.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `204`  | —    | Zone deleted; name released; isolation rules destroyed |
| `401`  | `unauthorized` | No/invalid token |
| `403`  | `forbidden` | Not the zone owner |
| `404`  | `not_found` | No such zone |

**Acceptance**: US3, SC-003/004.

---

## Inherited (feature 005, unchanged)

`GET /api/v1/zones/{name}/members` — any participant (owner or a user with a member
node) views all members. This feature adds no new restriction (FR-015).
