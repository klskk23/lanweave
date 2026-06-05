# Quickstart: Cascade Deletes (Admin User Removal)

**Feature**: 008-cascade-deletes | **Date**: 2026-06-05

Validates that an admin deleting a user removes their entire footprint from the records
and the live data plane. Builds on the 002/004/005/006 setup (admin account, node
registration, zones).

## Prerequisites

- Server running as root (or under `unshare -rUn`), WG interface up, nft table present.
- An admin JWT (`ADMIN`). A second user to be deleted, with at least one node and one
  owned zone, plus a node belonging to another user's zone.

## Automated checks (CI)

```bash
# Store cascade over real SQLite (non-privileged): cleanup, plan, guards, address reuse.
go test ./internal/server/store/... -run TestUserDeleteCascade

# Privileged acceptance: real WG peers + nft sets removed end to end.
unshare -rUn go test ./internal/server/api/... -run TestDeleteUser
```

Unprivileged hosts skip the kernel acceptance via `testutil.RequireNetAdmin`.

## Scenario A — full cleanup (US1)

1. As the target user: register two nodes; create a zone and join node 1; also join
   node 2 to a zone owned by a *different* user.
2. As admin: `DELETE /api/v1/admin/users/{targetId}` → expect `204`.
3. Verify:
   - `GET /api/v1/nodes` as the target user is no longer possible (account gone); the
     records contain no node, owned zone, or membership for the user.
   - `wg show` (or device read) lists no peer for either former node's public key.
   - `nft list table inet lanweave` has no set/rule for the deleted owned zone and no
     element for either former node in any set.
   - Registering a new node reuses the freed address.

## Scenario B — guards (US2)

```bash
# Self-delete → 403 cannot_delete_self
curl -skX DELETE -H "Authorization: Bearer $ADMIN" https://127.0.0.1:8443/api/v1/admin/users/<own_id>
# Last admin → 409 last_admin (when only one admin exists)
# Non-admin caller → 403 ; unknown id → 404
```

**Expect**: each guard returns the mapped status and changes nothing.

## Scenario C — cross-user integrity (US3)

After Scenario A, confirm the *other* user (whose zone the target's node 2 had joined)
still exists with their nodes and zones intact; only the target's node 2 is gone from
that zone's set.

## Scenario D — restart leaves no residue (US2)

Restart the server after a deletion and confirm the startup rebuild reproduces no peer,
set element, or rule for the removed user's nodes or owned zones.

## Success

- Scenario A and B (guards) pass in CI (store unit + privileged acceptance).
- Scenarios A/C/D's data-plane assertions pass under `unshare -rUn` with the real kernel.
