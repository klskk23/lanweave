# Contract: Admin deletes a user (cascade)

**Feature**: 008-cascade-deletes | **Date**: 2026-06-05

One new endpoint. No request body; no new protocol response type (success is empty,
failures use the existing error envelope).

## `DELETE /api/v1/admin/users/{id}` (authenticated, admin only)

Removes the user `{id}` and cascades: their nodes (addresses freed, tunnel peers
removed, memberships cleared), the zones they own (isolation set+rule destroyed, all
memberships cleared). Other users' nodes are not deleted; they only lose membership in a
removed zone or lose a removed node from a surviving zone.

### Path parameter

| Name | Type | Notes |
|------|------|-------|
| `id` | integer | target user id; non-integer → 404 `not_found` |

### Responses

| Status | When | Body |
|--------|------|------|
| `204 No Content` | user removed (and data-plane sync attempted) | empty |
| `401 Unauthorized` | no/invalid token | error envelope `unauthorized` |
| `403 Forbidden` | caller is not an admin | error envelope (AdminRequired) |
| `403 Forbidden` | caller targets their own id | error envelope `cannot_delete_self` |
| `404 Not Found` | no user with that id | error envelope `not_found` |
| `409 Conflict` | target is the last remaining admin | error envelope `last_admin` |

### Behavioral guarantees (from spec)

- **Completeness (US1)**: on 204, the user, all their nodes, and all zones they own are
  absent; freed addresses are reusable; the tunnel has no peer for the former nodes; the
  isolation ruleset has no element for them and no set/rule for the owned zones (FR-002…
  FR-006; SC-001/002/003/004).
- **Atomicity (US2)**: the DB cascade is all-or-nothing; a rejected delete (404, 409)
  changes nothing (FR-007; SC-005/006).
- **Safety (US2)**: the last admin cannot be deleted (409); an admin cannot delete
  themselves (403) (FR-011/FR-012; SC-006).
- **Data-plane sync (US2)**: a transient tunnel/isolation failure does not fail the 204;
  the records are authoritative and reconciled at the next startup (FR-009).
- **Cross-user (US3)**: other users and their nodes survive; only the intended
  membership changes occur (FR-013; SC-008).

## Unchanged contracts

- All other endpoints are unchanged. Node and zone deletion by their owners (004/006)
  remain available; this endpoint is the administrator-level whole-user cascade.
