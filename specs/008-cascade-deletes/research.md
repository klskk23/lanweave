# Research: Cascade Deletes (Admin User Removal)

**Feature**: 008-cascade-deletes | **Date**: 2026-06-05

Technical Context had no open unknowns: the cascade composes existing 002/004/005/006
behaviors. Decisions below were settled from the existing schema, the store DSN, and
the established consistency pattern.

## Decision 1 — Rely on declared FK cascade for the DB removal

- **Decision**: Perform the database cascade with a single `DELETE FROM users WHERE
  id=?`. SQLite foreign keys are enabled for every pooled connection via the store DSN
  (`_pragma=foreign_keys(ON)` in `store.go`), so the existing actions fire: `nodes`,
  `zones`, `zone_members` are `ON DELETE CASCADE`; `invites.created_by_user_id` /
  `used_by_user_id` are `ON DELETE SET NULL`.
- **Rationale**: The schema already encodes the exact cascade ROADMAP 008 asks for.
  Re-implementing it as a hand-written multi-statement delete would duplicate the
  declared invariants and risk drifting from them. A single statement is also
  inherently atomic. SQLite (the source of truth) owns the relationships; the data
  plane is derived from it.
- **Alternatives considered**:
  - *Explicit multi-table DELETEs in a transaction*: more code, must be kept in lockstep
    with the FK declarations, and offers no atomicity benefit over the single cascading
    delete. Rejected.
  - *ORM/library cascade*: no ORM in this project. N/A.

## Decision 2 — Gather the data-plane footprint inside the same transaction, before the delete

- **Decision**: In the same transaction, **before** the `DELETE`, read the user's nodes'
  public keys, the addresses + surviving-zone ids those nodes belonged to, and the ids
  of zones the user owns. Return them to the handler as a `DeletionResult`.
- **Rationale**: The FK cascade cleans the *database* but cannot touch the *kernel* (WG
  peers, nft set elements). The handler needs the public keys to remove peers and the
  (ip, zone) pairs to clear set elements — and these must be read before the rows
  vanish. Reading inside the transaction (with the delete) guarantees the gathered
  snapshot matches exactly what is removed, even under concurrent changes (FR-007).
- **Alternatives considered**:
  - *Rebuild the entire data plane from the DB after the delete* (`ReplacePeers` +
    `netfw.Rebuild`): simplest and fully correct, but `ReplacePeers` rewrites the whole
    peer set on every user deletion, which can disturb other users' live tunnels mid-
    operation. The incremental remove-only approach matches how 004/006 already handle
    deletes and leaves untouched tunnels alone. Rejected for live operation (still used
    at startup for reconciliation).
  - *Gather after delete*: impossible — the rows are gone.

## Decision 3 — Surviving zones vs owned zones for set-element cleanup

- **Decision**: For each removed node, clear its address only from **surviving** zones
  (zones not owned by the deleted user) via `netfw.RemoveMember`. For each **owned**
  zone, call `netfw.DeleteZone`, which destroys the whole set + accept rule (and thereby
  every element, including foreign members).
- **Rationale**: Destroying an owned zone's set is a single operation that removes all
  its elements at once, so per-element removal there would be redundant. Surviving zones
  must keep their other members, so only the removed node's element is taken out.
- **Alternatives considered**:
  - *RemoveMember for every (node, zone) then DeleteZone*: double work and ordering
    hazards for owned zones. Rejected.

## Decision 4 — Best-effort data-plane sync, DB authoritative, startup reconcile

- **Decision**: After the committed delete, apply the data-plane changes best-effort:
  log each failure but still return `204`. The existing startup `rebuildNodePeers` +
  `rebuildZoneRules` reconcile any gap.
- **Rationale**: This is the project-wide consistency pattern (004/005/006): the DB is
  the source of truth and the kernel is derivative and reconstructible. A transient nft/
  wg failure must not leave the DB and the API disagreeing or surface a 5xx for an
  operation that already succeeded in the source of truth (FR-009, edge case
  "partial data-plane failure").
- **Alternatives considered**:
  - *Fail the request if any kernel op fails*: would report failure for a delete that
    actually happened (DB committed), and invites a confusing retry. Rejected.

## Decision 5 — Safety guards: last admin and self-deletion

- **Decision**: Reject deleting the last remaining administrator (`ErrLastAdmin`, 409)
  and reject an admin deleting their own account (`cannot_delete_self`, 403). Deleting
  any *other* admin is allowed.
- **Rationale**: Removing the last admin would lock administration out of the running
  system; self-deletion through this endpoint is a footgun (the caller invalidates the
  identity they are acting as mid-request). Both are cheap guards with reasonable
  defaults. The last-admin check lives in the store transaction (it needs the admin
  count atomically); the self-delete check lives in the handler (it needs the caller's
  identity).
- **Known interaction**: `EnsureAdmin` recreates the *configured* bootstrap admin on the
  next startup if it is missing. So deleting the configured admin (when not the last
  admin) is not permanent across a restart. This is existing bootstrap behavior and is
  acceptable; the last-admin guard still prevents a live lock-out.
- **Alternatives considered**:
  - *No guards*: a single fat-finger could brick administration. Rejected.
  - *Only last-admin guard (allow self-delete when not last)*: defensible, but self-
    delete remains a surprising footgun; we forbid it for clarity. Revisable.

## Status code mapping (for contracts)

| Outcome | Status | Error code |
|---------|--------|------------|
| Success | 204 No Content | — |
| Caller not authenticated | 401 | `unauthorized` |
| Caller not an admin | 403 | (AdminRequired) |
| Target is the caller | 403 | `cannot_delete_self` |
| Target is the last admin | 409 | `last_admin` |
| Target does not exist | 404 | `not_found` |
