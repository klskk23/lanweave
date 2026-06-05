# Contract: Zone Endpoints (create / join / leave / list)

All require `Authorization: Bearer <jwt>`. Shared error envelope + global rate limiter.

---

## `POST /api/v1/zones`

Create a password-protected zone; the caller becomes owner.

**Request** (`protocol.CreateZoneRequest`):
```json
{ "name": "devteam", "password": "a-strong-zone-password" }
```

**Responses**:
| Status | Code | When |
|--------|------|------|
| `201`  | —    | Body: `{ "id": 1, "name": "devteam", "is_owner": true }` |
| `400`  | `validation_error` | Empty/oversized name, short password, malformed body |
| `401`  | `unauthorized` | No/invalid token |
| `409`  | `zone_name_taken` | A zone with that name exists |

Creating a zone adds no members (FR-004). **Acceptance**: US1, SC-006.

---

## `POST /api/v1/zones/{name}/join`

Join one of the caller's nodes to the zone.

**Request** (`protocol.JoinZoneRequest`):
```json
{ "node_id": 7, "password": "a-strong-zone-password" }
```

**Behavior**: verify password (dummy-verify on unknown zone for timing parity) →
verify node ownership → idempotent membership insert → add the node's address to the
zone's set (compensate by removing the membership if the set update fails).

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Joined (or already a member — idempotent) |
| `400`  | `validation_error` | Missing node_id/password |
| `401`  | `unauthorized` | No/invalid token |
| `403`  | `invalid_zone_or_password` | Wrong password OR no such zone (identical, no enumeration) |
| `404`  | `not_found` | The node is not owned by the caller |

**Acceptance**: US2-1/4/5/6, SC-001/005.

---

## `POST /api/v1/zones/{name}/leave`

Remove one of the caller's nodes from the zone.

**Request** (`protocol.LeaveZoneRequest`): `{ "node_id": 7 }`

**Responses**:
| Status | Code | When |
|--------|------|------|
| `204`  | —    | Removed; the node's address leaves the zone's set |
| `401`  | `unauthorized` | No/invalid token |
| `404`  | `not_found` | No such zone, node not owned, or node not a member |

**Acceptance**: US3, SC-008.

---

## `GET /api/v1/zones`

List the zones the caller participates in (owns or has a member node in).

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `{ "zones": [ { "id", "name", "is_owner" }, ... ] }` (empty if none) |
| `401`  | `unauthorized` | No/invalid token |

**Acceptance**: US4-1.
