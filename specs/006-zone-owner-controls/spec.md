# Feature Specification: Zone Owner Controls

**Feature Branch**: `006-zone-owner-controls`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "完成ROADMAP.md 的 006 zone-owner-controls"

Scope drawn from ROADMAP.md feature 006 and DESIGN.md §5.4: the owner of a zone can
change the zone password (without ejecting existing members), kick a specific member
node, and delete the entire zone (releasing the name and removing all members and
isolation rules). Non-owners are forbidden from these operations. Viewing a zone's
members is already available to any participant (feature 005) and is reused here.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — owner 修改 zone 密码（不踢老人） (Priority: P1)

The owner of a zone changes its password. Existing member nodes keep their
membership and their reachability — the change only affects who can join in the
future: the old password stops working and the new one starts working.

**Why this priority**: Owners need to rotate a leaked or shared password without
disrupting everyone already connected. Independently testable: after a change, an
existing member is unaffected, a new join with the old password fails, and one with
the new password succeeds.

**Independent Test**: As the owner, change the password. Confirm an already-joined
node is still a member. Attempt a fresh join with the old password → refused; with
the new password → admitted.

**Acceptance Scenarios**:

1. **Given** a zone with members, **When** the owner changes its password, **Then** all existing members remain members (none are ejected) and retain reachability.
2. **Given** a zone whose password was just changed, **When** someone attempts to join using the OLD password, **Then** the join is refused.
3. **Given** the same zone, **When** someone attempts to join using the NEW password, **Then** the join is admitted.
4. **Given** a request with a too-weak new password, **When** submitted, **Then** it is refused with a validation error and the password is unchanged.
5. **Given** a non-owner (any authenticated user who does not own the zone), **When** they attempt to change the password, **Then** the request is refused as forbidden.

---

### User Story 2 — owner 踢出某个成员节点 (Priority: P1)

The owner removes a specific member node from the zone — including a node belonging
to another user. That node immediately loses reachability within the zone; its
membership of other zones and its existence as a node are unaffected.

**Why this priority**: Owners must be able to revoke a member's access. Independently
testable: a kicked node is no longer a member and can no longer reach the zone's other
members.

**Independent Test**: As the owner, kick a member node (it may be another user's).
Confirm it is no longer a member and has lost reachability to that zone, while its
membership of any other zone and its node record remain intact.

**Acceptance Scenarios**:

1. **Given** a zone with a member node (possibly owned by a different user), **When** the owner kicks that node, **Then** it is removed from the zone and loses reachability to the zone's other members.
2. **Given** a node that is a member of two zones, **When** the owner of one zone kicks it, **Then** it retains its membership of and reachability within the other zone, and the node itself still exists.
3. **Given** a node that is not a member of the zone (or does not exist), **When** the owner attempts to kick it, **Then** the request is refused as not found and nothing changes.
4. **Given** a non-owner, **When** they attempt to kick a member, **Then** the request is refused as forbidden.

---

### User Story 3 — owner 删除整个 zone (Priority: P1)

The owner deletes a zone. All memberships are removed, the zone's isolation rules are
destroyed, and the name is released so it can be created again. Member nodes
themselves are not deleted; they simply lose this zone.

**Why this priority**: Owners must be able to dismantle a zone they created.
Independently testable: after deletion, the name is reusable and no member retains
reachability via the deleted zone.

**Independent Test**: As the owner, delete a zone with members. Confirm the name can
be created again, the former members no longer have reachability through it, and the
member nodes still exist (and keep any other zone memberships).

**Acceptance Scenarios**:

1. **Given** a zone with members, **When** the owner deletes it, **Then** the zone is gone, all its memberships are removed, its isolation rules are destroyed, and the member nodes themselves still exist.
2. **Given** a deleted zone's name, **When** any user creates a zone with that name, **Then** it succeeds (the name was released).
3. **Given** a member node that was in the deleted zone and also in another zone, **When** the zone is deleted, **Then** the node retains reachability within the other zone.
4. **Given** a non-owner, **When** they attempt to delete the zone, **Then** the request is refused as forbidden.

---

### User Story 4 — 权限与一致性 (Priority: P2)

The owner-only controls are consistently authorized and survive a restart. Every
owner operation refuses non-owners; an owner can still view the zone's members; and
after a relay restart the effects (changed password, kicked members, deleted zones)
are reflected in the isolation rules.

**Why this priority**: Hardens US1–US3 — the authorization gate and the
database-as-source-of-truth guarantee. Independently testable via the authz matrix
and a restart.

**Independent Test**: Exercise each owner operation as a non-owner → all forbidden.
As the owner, view members → allowed. Change a password, kick a member, delete a
zone, then restart the relay → the changes hold (deleted zones have no rules, the
kicked member is absent, the new password is in effect).

**Acceptance Scenarios**:

1. **Given** any of the three owner operations, **When** attempted by an authenticated non-owner, **Then** it is refused as forbidden (none of them succeed).
2. **Given** the owner (or any participant), **When** they view the zone's members, **Then** they see all member nodes (inherited from feature 005).
3. **Given** a changed password, a kicked member, and a deleted zone, **When** the relay restarts, **Then** the isolation rules match the database: the deleted zone has no rules, the kicked node is absent from its set, and the new password is in force for future joins.

---

### Edge Cases

- **Changing the password to the same value** is allowed (a no-op rotation) and ejects no one.
- **Kicking a node that is also the owner's own node** is allowed (owners can remove their own node like any member).
- **Deleting a zone with no members** succeeds and releases the name.
- **Operating on a zone that does not exist** → not found.
- **A non-owner who IS a member** still cannot change the password, kick, or delete (membership ≠ ownership).
- **Concurrent kick and the member leaving on their own** → the membership ends up removed exactly once; a second removal reports not found.
- **Stale isolation rules after an unclean shutdown** → reconciled to the database at startup (a deleted zone's rules do not reappear).
- **The relay's own address / node-to-relay traffic** is unaffected by any of these operations.

---

## Requirements *(mandatory)*

### Functional Requirements

**Change password**

- **FR-001**: The owner of a zone MUST be able to change its password.
- **FR-002**: Changing the password MUST NOT eject any existing member or alter any member's reachability.
- **FR-003**: After a password change, a join with the OLD password MUST be refused and a join with the NEW password MUST be admitted.
- **FR-004**: A new password MUST meet the documented minimum strength; a violation is refused with a validation error and leaves the password unchanged.

**Kick member**

- **FR-005**: The owner of a zone MUST be able to remove a specific member node from it, including a node owned by another user.
- **FR-006**: Kicking a node MUST remove its membership and its reachability within that zone, while leaving the node's record and its membership of other zones unaffected.
- **FR-007**: Kicking a node that is not a member of the zone, or that does not exist, MUST be refused as not found, changing nothing.

**Delete zone**

- **FR-008**: The owner of a zone MUST be able to delete the entire zone.
- **FR-009**: Deleting a zone MUST remove all of its memberships and destroy its isolation rules, while NOT deleting the member nodes themselves.
- **FR-010**: Deleting a zone MUST release its name so the name can be created again.

**Authorization & consistency**

- **FR-011**: Each owner operation (change password, kick, delete) MUST be restricted to the zone's owner; an authenticated non-owner MUST be refused as forbidden, and an operation on a nonexistent zone MUST be refused as not found.
- **FR-012**: Being a member of a zone MUST NOT grant any owner operation (membership is not ownership).
- **FR-013**: The database MUST remain the source of truth; at startup the isolation rules MUST be rebuilt to match it, so a changed password, a kicked member, and a deleted zone are all reflected after a restart.
- **FR-014**: All owner operations MUST require authentication, MUST use the shared error envelope and global rate limiting, and the zone password MUST be stored hashed and MUST NOT appear in logs.
- **FR-015**: Viewing a zone's members remains available to any participant (owner or a user with a member node), as established in feature 005; this feature adds no new restriction to it.

### Key Entities

- **Zone**: Reused from feature 005. This feature mutates its password and deletes it; the owner field determines who may perform these operations.
- **Zone membership**: Reused from feature 005. This feature removes individual memberships (kick) and all memberships of a zone (delete).
- **Isolation rule set**: Reused from feature 005. A kick removes one member address; a delete destroys the zone's set and rule; the startup rebuild reconciles all of it from the database.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After an owner changes the password, every pre-existing member retains reachability (0 members ejected), a join with the old password fails, and a join with the new password succeeds — verified 100% of the time.
- **SC-002**: After an owner kicks a member node, that node has lost reachability within the zone (its address is no longer admitted) while keeping any other zone's reachability — verified 100%.
- **SC-003**: After an owner deletes a zone, the name can be re-created and no former member retains reachability through it; the member nodes still exist — verified 100%.
- **SC-004**: 100% of attempts at the three owner operations by an authenticated non-owner are refused (forbidden).
- **SC-005**: After a restart following a password change, a kick, and a delete, the isolation rules match the database (deleted zone absent, kicked member absent, new password in force) — verified 100%.
- **SC-006**: Operating on a nonexistent zone returns not found in 100% of attempts; the zone password never appears in logs.

---

## Assumptions

- Builds on features 001–005: authenticated users + JWT (002), zones + memberships +
  the nftables isolation sets/rules (005), argon2id hashing, structured logging, the
  shared error envelope, the global rate limiter, and the startup rebuild — all reused.
- "Owner" is the user who created the zone (the owner field on the zone, feature 005).
  System administrators are NOT given blanket zone control here; only the owner may
  manage a zone (admin-driven cleanup of a user's zones is feature 008).
- Authorization choice (per ROADMAP): an authenticated non-owner acting on an existing
  zone is refused as **forbidden (403)**; an operation on a zone that does not exist is
  **not found (404)**. Owner endpoints therefore reveal a zone's existence to
  authenticated users, which is acceptable since zone names are globally unique and
  already discoverable by attempting to join.
- New password minimum strength matches zone creation (≥ 8 characters); changing to the
  same value is permitted.
- Kicking and deleting are database-authoritative with the isolation-rule update applied
  immediately (best-effort) and reconciled by the startup rebuild — the same consistency
  pattern as features 004/005.
- Member visibility is unchanged from feature 005 (any participant sees all members);
  this feature adds no new viewing endpoint.
- This feature does NOT implement ownership transfer (DESIGN lists it as v1.1) nor full
  user-deletion cascade (feature 008).
- IPv4 only; the relay runs with the privilege required to manage the isolation rules.
