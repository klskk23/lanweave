# Data Model: Cascade Deletes (Admin User Removal)

**Feature**: 008-cascade-deletes | **Date**: 2026-06-05

**No schema migration.** This feature adds no tables or columns; it exercises the
delete-time foreign-key actions already declared in migrations 0001–0004.

## Existing relationships exercised (the cascade)

| Child row | References | On user delete |
|-----------|------------|----------------|
| `nodes.user_id` | `users(id)` | **CASCADE** — the user's nodes are deleted |
| `zones.owner_user_id` | `users(id)` | **CASCADE** — zones the user owns are deleted |
| `zone_members.node_id` | `nodes(id)` | **CASCADE** — when a node is deleted, its memberships go |
| `zone_members.zone_id` | `zones(id)` | **CASCADE** — when an owned zone is deleted, its memberships go |
| `invites.created_by_user_id` | `users(id)` | **SET NULL** — invite audit row kept, creator reference cleared |
| `invites.used_by_user_id` | `users(id)` | **SET NULL** — invite audit row kept, consumer reference cleared |

Result of one `DELETE FROM users WHERE id=?` (FK enforcement on): the user, all their
nodes, all zones they own, and every membership of those nodes or zones are removed in a
single atomic statement; invite history survives with references to the user nulled.

## New runtime type (not persisted)

### DeletionResult (`internal/server/store`)

Returned by `UserRepo.DeleteCascade` so the handler can sync the kernel data plane to
match the now-deleted records.

| Field | Type | Purpose |
|-------|------|---------|
| `NodePubKeys` | `[]string` | public keys of the user's removed nodes → `wg.RemovePeer` each |
| `SurvivingMemberships` | `[]{IP netip.Addr; ZoneID int64}` | removed nodes' addresses in zones **not** owned by the user → `netfw.RemoveMember` each |
| `OwnedZoneIDs` | `[]int64` | zones the user owned → `netfw.DeleteZone` each (destroys set+rule and all elements) |

- Gathered inside the deletion transaction, before the `DELETE`, so it is a consistent
  snapshot of exactly what is removed.
- Carries no secret (public keys and addresses are not secrets).

## Errors (typed, in `store`)

| Error | Condition | Handler maps to |
|-------|-----------|-----------------|
| `ErrUserNotFound` | target id has no user row | 404 `not_found` |
| `ErrLastAdmin` | target is an admin and is the only admin | 409 `last_admin` |

(Self-deletion is guarded in the handler from the caller's identity, not in the store.)

## Invariants

- **Atomicity**: the gather-reads + `DELETE` run in one transaction; a guard rejection
  rolls back having changed nothing (FR-007, SC-005).
- **At least one admin**: the store refuses to delete the last `is_admin=1` row
  (FR-011).
- **Source of truth**: after commit the records are authoritative; the kernel sync is
  best-effort and reconciled by the existing startup rebuild (FR-009).
- **Cross-user safety**: only the deleted user's nodes/owned-zones/memberships change;
  other users' nodes are never deleted — they only lose membership in a removed zone, or
  a surviving zone loses a removed node as a member (FR-013).
