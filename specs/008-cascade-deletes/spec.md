# Feature Specification: Cascade Deletes (Admin User Removal)

**Feature Branch**: `008-cascade-deletes`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "完成ROADMAP.md 中的 008" (ROADMAP feature 008: cascade-deletes)

Scope drawn from ROADMAP.md feature 008 and DESIGN.md: an administrator can remove a
user account, and the removal cascades through everything attributable to that user —
their nodes (addresses freed, tunnel peers removed, memberships cleared) and the zones
they own (isolation rules destroyed, all memberships cleared) — atomically in the
database with the live tunnel and isolation rules synced to match.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 管理员删除用户并清除其全部足迹 (Priority: P1)

An administrator removes a user account. Afterwards the user, every node they owned,
and every zone they owned are gone from the system: the removed nodes' addresses are
released for reuse, their tunnel peers are removed, they are gone from every zone they
belonged to, and the owned zones' isolation rules are destroyed. No trace of the user
remains in the system's records or in the live tunnel/isolation rules.

**Why this priority**: This is the feature — a clean, complete removal. Without it,
deleting a user leaves dangling nodes, stranded addresses, orphaned peers, and live
firewall rules. Independently testable by deleting a user with nodes and owned zones
and verifying nothing is left behind.

**Independent Test**: Create a user with two nodes (one in a zone the user owns, one in
a zone owned by someone else), then delete the user. Verify the account, both nodes,
and the owned zone are gone; the nodes' addresses are reusable; the tunnel has no peer
for either node; and the isolation rules carry no element for either node and no
set/rule for the owned zone.

**Acceptance Scenarios**:

1. **Given** a user who owns several nodes, **When** an administrator deletes the user, **Then** the user record and all their nodes are removed and the tunnel shows no peer for any of those nodes.
2. **Given** a removed node had an address from the pool, **When** the deletion completes, **Then** that address is free and is taken by the next newly registered node.
3. **Given** the user owns a zone, **When** the user is deleted, **Then** the zone is deleted entirely — its isolation set and accept rule are gone and all of its memberships are cleared.
4. **Given** a removed node belonged to one or more zones, **When** the user is deleted, **Then** that node's address is absent from every one of those zones' isolation sets.
5. **Given** a non-administrator (or an unauthenticated caller), **When** they attempt to delete a user, **Then** the request is refused and nothing is removed.

---

### User Story 2 — 删除是原子且安全的 (Priority: P2)

The removal is all-or-nothing in the database and cannot be used to break the system.
If any part of the database cascade fails, nothing is removed. The live tunnel and
isolation rules are brought into agreement with the post-deletion records, and a
restart leaves no residue. The operation cannot remove the last administrator and
cannot be used by an administrator to remove their own account.

**Why this priority**: A half-finished cascade (some rows gone, others left; a peer
removed but its address still allocated) is worse than no deletion. The admin-safety
guards prevent locking the whole system out. Hardens US1.

**Independent Test**: Force a failure partway through a deletion and confirm the
database is unchanged. Attempt to delete the only administrator and confirm rejection.
Attempt self-deletion and confirm rejection. Restart after a deletion and confirm no
stale rows, peers, or rules reappear.

**Acceptance Scenarios**:

1. **Given** a deletion that fails partway through the database cascade, **When** the operation aborts, **Then** the database is exactly as it was before (no node, zone, membership, or account partially removed).
2. **Given** the system has exactly one administrator, **When** an attempt is made to delete that administrator, **Then** the request is rejected and nothing changes.
3. **Given** an administrator is signed in, **When** they attempt to delete their own account through this operation, **Then** the request is rejected and nothing changes.
4. **Given** a user was deleted, **When** the server is restarted, **Then** no peer, isolation-set element, or rule for the removed user's nodes or owned zones reappears.
5. **Given** the live tunnel or isolation update is briefly unavailable during a deletion, **When** the records are already committed, **Then** the system still reports success and the live state is reconciled at the next startup (records are the source of truth).

---

### User Story 3 — 不波及其他用户 (Priority: P3)

Removing one user does not damage anyone else. Other users and their nodes survive
intact. The only cross-user effects are the intended ones: a removed user's node that
belonged to someone else's zone leaves that zone (the zone and its owner remain), and a
surviving user's node that belonged to a removed user's zone loses that membership
(the node itself remains).

**Why this priority**: Cross-user isolation correctness is what makes the cascade safe
to run in a shared system. It is a refinement of US1's completeness, focused on what
must NOT be deleted.

**Independent Test**: Two users share zones in both directions; delete one and verify
the other's account, nodes, and unrelated memberships are intact, while exactly the
shared memberships tied to the deleted user are cleared.

**Acceptance Scenarios**:

1. **Given** another user's node is a member of a zone owned by the deleted user, **When** the user is deleted, **Then** that node survives but is no longer a member of the (now-deleted) zone, and its address is gone from that zone's former set.
2. **Given** a deleted user's node was a member of a zone owned by a surviving user, **When** the user is deleted, **Then** that surviving zone loses the node as a member (its set element removed) but the zone itself and its other members remain.
3. **Given** an unrelated user with their own nodes and zones, **When** a different user is deleted, **Then** the unrelated user's records, peers, and isolation rules are completely unchanged.

---

### Edge Cases

- **User with no nodes and no owned zones**: deletion removes just the account and succeeds.
- **Node in multiple zones**: removed from all of them; every corresponding isolation-set element is cleared.
- **Owned zone with foreign members**: the zone is deleted even though other users' nodes were members; those nodes survive, only their membership is cleared.
- **Removed node in a surviving zone**: the surviving zone loses that member; the zone and its owner are untouched.
- **Deleting the last administrator**: rejected; system unchanged.
- **Self-deletion**: rejected; system unchanged.
- **Deleting a non-existent user**: reported as not found; nothing changes.
- **Invites the user created or consumed**: invite history is preserved as audit, but no invite continues to reference the deleted user (its creator/consumer reference is cleared); no dangling reference remains.
- **Concurrent change during deletion** (e.g., one of the user's nodes is being added to a zone as the user is deleted): the final committed state is consistent, and any best-effort live-update gap is reconciled at the next startup.
- **Partial data-plane failure** (a peer or set element cannot be removed live): the records are authoritative and already committed; the live state is reconciled at startup; the operation still reports success.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Only an authenticated administrator MAY delete a user account; a non-administrator MUST be refused (forbidden) and an unauthenticated caller refused (unauthorized), with no change to system state.
- **FR-002**: Deleting a user MUST remove the user account and every node owned by that user.
- **FR-003**: Each removed node's pool address MUST be released so it is available for reuse.
- **FR-004**: Each removed node's tunnel peer MUST be removed from the live tunnel.
- **FR-005**: Each removed node MUST be removed from every zone it belonged to — including zones owned by other users — and the corresponding isolation-set element cleared.
- **FR-006**: Every zone OWNED by the deleted user MUST be deleted entirely: its isolation set and accept rule destroyed and all of its memberships cleared, including members owned by other users. Those other users' nodes MUST NOT themselves be deleted.
- **FR-007**: The database portion of the cascade MUST be atomic — it either fully completes or leaves the database unchanged; a partially-applied deletion MUST NOT be observable.
- **FR-008**: After a successful deletion, no database row may continue to reference the deleted user (account, nodes, owned zones, and memberships are removed; any audit references such as invites are cleared so none point at the removed user).
- **FR-009**: The live tunnel and isolation rules MUST be updated to match the post-deletion records; any best-effort live-update gap MUST be reconciled at the next startup rebuild (the records are the source of truth, tunnel/isolation state is derivative).
- **FR-010**: Deleting a non-existent user MUST be reported as not found, with system state unchanged.
- **FR-011**: The system MUST always retain at least one administrator; an attempt to delete the last remaining administrator MUST be rejected with system state unchanged.
- **FR-012**: An administrator MUST NOT delete their own account through this operation; such a request MUST be rejected with system state unchanged.
- **FR-013**: Users other than the one deleted, and their nodes, MUST remain intact except for the intended membership changes in FR-005/FR-006.

### Key Entities

- **User account**: An administrator or normal user. Deleting one triggers the cascade. At least one administrator must always remain.
- **Node**: A device owned by a user, holding a pool address and a tunnel peer, and possibly a member of one or more zones. Deleted with its owner; its address is freed and its peer removed.
- **Owned zone**: A zone whose owner is the deleted user. Deleted entirely, with its isolation set/rule and all memberships removed.
- **Membership**: The relation placing a node in a zone. Cleared whenever either the node or the zone is removed; surfaced in the isolation rules as a set element.
- **Invite (audit)**: A record of how a user was invited or who they invited. Preserved as history, but cleared of any reference to the deleted user.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After deleting a user, 100% of that user's nodes, owned zones, and memberships are absent from the system records.
- **SC-002**: After deletion, the tunnel has no peer for any of the user's former nodes (100% of cases).
- **SC-003**: After deletion, the isolation ruleset contains no set element for any removed node and no set or rule for any deleted zone (100% of cases).
- **SC-004**: An address freed by deletion is reused by the next newly registered node.
- **SC-005**: A deletion that fails partway through leaves the database byte-for-byte unchanged (zero rows altered) in 100% of induced-failure cases.
- **SC-006**: An attempt to delete the last administrator, or an administrator's own account, is rejected 100% of the time and changes nothing.
- **SC-007**: Deleting a typical account (up to 20 nodes across up to 10 zones) completes within 1 second end to end (records committed and live state synced).
- **SC-008**: After deleting one user, every other user's account, nodes, peers, and isolation rules are unchanged except for the intended loss of membership tied to the deleted user.

---

## Assumptions

- Builds directly on features 002 (users, admin, invites), 004 (nodes, addressing,
  tunnel peers), 005 (zones, memberships, isolation rules), and 006 (zone deletion and
  member-removal semantics). This feature composes those existing removal behaviors
  into one administrator-driven cascade; it adds no new node/zone concepts.
- The records database is the single source of truth; the tunnel peer table and
  isolation rules are derivative and are reconciled from the records at startup — the
  same best-effort-live-update-plus-startup-rebuild pattern used in 004/005/006.
- "No residual rows" means no row continues to *reference* the deleted user. Nodes,
  owned zones, and memberships are deleted outright; invite history is retained but its
  references to the deleted user are cleared (so the audit trail survives without a
  dangling reference). This matches the existing data model's on-delete behavior.
- Two admin-safety guards are deliberate defaults (revisable): the last remaining
  administrator cannot be deleted, and an administrator cannot delete their own account
  through this operation. Deleting any *other* administrator is allowed (ROADMAP: "删
  admin/普通用户").
- Confirmation UX (naming the specific user before deletion) is a client concern
  (Windows client, Principle III) and is out of scope here; the server performs the
  delete when asked by an authorized administrator.
- IPv4 only; single relay instance.
