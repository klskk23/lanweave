# Feature Specification: Zones and nftables Isolation

**Feature Branch**: `005-zones-and-nftables-isolation`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "zones-and-nftables-isolation"

Scope drawn from ROADMAP.md feature 005 and DESIGN.md §4.1, §4.2, §5, §6, §7: a zone
is a password-protected group of nodes; any logged-in user can create a zone and
join one of their nodes to a zone by name + password; nodes in the same zone can
reach each other over the tunnel while everything else stays denied; a node may
belong to multiple zones; the isolation rules are rebuilt from the database at
startup. Owner-only management (change password / kick / delete zone) is feature 006.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 创建受密码保护的 zone (Priority: P1)

A logged-in user creates a zone by choosing a globally unique name and a password.
They become the zone's owner. Creating a zone does not automatically place any of
their nodes in it — joining is a separate, explicit step.

**Why this priority**: A zone must exist before anyone can join it. Independently
testable with only an authenticated user (features 001–002).

**Independent Test**: Create a zone with a fresh name + password → success. Create
again with the same name → refused as a conflict.

**Acceptance Scenarios**:

1. **Given** an authenticated user, **When** they create a zone with an unused name and an acceptable password, **Then** the zone is created and the user is recorded as its owner.
2. **Given** an existing zone name, **When** any user tries to create a zone with that name, **Then** it is refused with a name-conflict error.
3. **Given** a creation request with an empty/oversized name or too-weak password, **When** submitted, **Then** it is refused with a validation error.
4. **Given** an unauthenticated request, **When** it targets any zone operation, **Then** it is refused with 401.

---

### User Story 2 — 把节点加入 zone 并获得同区可达性 (Priority: P1)

A user joins one of their nodes to a zone by supplying the zone name, the password,
and which of their nodes to add. Once joined, that node can exchange traffic over
the tunnel with the other nodes in the same zone. Nodes that do not share a zone
remain unable to reach each other (default-deny from feature 003).

**Why this priority**: This is the product's core value — controlled, isolated
connectivity. Independently testable: two nodes that join the same zone can reach
each other; two nodes in different zones cannot.

**Independent Test**: Two nodes join the same zone → they can exchange tunnel
traffic. Two nodes in different (or no) shared zone → they cannot.

**Acceptance Scenarios**:

1. **Given** a user's own node and a zone, **When** the user joins the node with the correct name + password, **Then** the node becomes a member and is admitted to the zone's reachability.
2. **Given** two nodes that are members of the same zone, **When** one sends tunnel traffic to the other, **Then** it is delivered.
3. **Given** two nodes that share no zone, **When** one sends tunnel traffic to the other, **Then** it is denied.
4. **Given** a join attempt with a wrong password OR a zone name that does not exist, **When** submitted, **Then** it is refused with the SAME generic error (no disclosure of whether the zone exists).
5. **Given** a join naming a node the caller does not own, **When** submitted, **Then** it is refused (the node is treated as not found).
6. **Given** a node already a member of the zone, **When** the user joins it again, **Then** the result is a success no-op (membership is idempotent).

---

### User Story 3 — 离开 zone（撤销可达性） (Priority: P1)

A user removes one of their nodes from a zone. The node loses reachability to that
zone's other members; its membership of any other zones is unaffected.

**Why this priority**: Members must be able to withdraw a node. Independently
testable after US2.

**Independent Test**: A member node leaves → it can no longer exchange traffic with
that zone's members, but still reaches members of any other zone it remains in.

**Acceptance Scenarios**:

1. **Given** a user's node that is a member of a zone, **When** the user removes it from the zone, **Then** the node is no longer a member and loses reachability to that zone.
2. **Given** a node that is a member of two zones, **When** it leaves one, **Then** it retains reachability within the other.
3. **Given** a node that is not a member of the zone (or not owned by the caller), **When** a leave is attempted, **Then** it is refused as not found and nothing changes.

---

### User Story 4 — 查看我的 zone 与成员（透明可见） (Priority: P1)

A user lists the zones they participate in (own or have a node in) and views the
members of a zone they belong to. Within a zone, every member can see every member
node's name, address, and owning user — full transparency so people can find each
other's services.

**Why this priority**: Members need to discover peers' addresses to actually use
the connectivity. Independently testable after US2.

**Independent Test**: After two users join nodes to one zone, each can list the
zone and see both members (names, addresses, owners). A user who is not in the zone
cannot view its members.

**Acceptance Scenarios**:

1. **Given** a user with nodes in zones (and/or zones they own), **When** they list their zones, **Then** they see each such zone (with whether they own it).
2. **Given** a member of a zone, **When** they view that zone's members, **Then** they see every member node's name, address, and owning username — including other users' nodes.
3. **Given** a user with no node in a zone and who does not own it, **When** they try to view its members, **Then** the request is refused as not found (the zone is not disclosed).

---

### User Story 5 — 多区归属、一致性与重建 (Priority: P2)

Memberships and isolation rules stay correct as nodes join multiple zones, as the
relay restarts, and as nodes are deleted. A node can belong to several zones at
once (reachable within each). After a restart the isolation rules exactly match the
recorded memberships. Deleting a node removes it from every zone it belonged to —
so a recycled address never inherits a deleted node's zone membership.

**Why this priority**: These guarantees protect the integrity of the isolation
model. They harden US2/US3 and are independently testable via multi-zone, restart,
and deletion scenarios.

**Independent Test**: A node joins two zones → reachable within each. Restart the
relay → memberships and reachability are unchanged. Delete a member node → it is
gone from all zones, and a new node that reuses its address is not in any of them.

**Acceptance Scenarios**:

1. **Given** a node joined to two zones, **When** traffic is sent from peers in each zone, **Then** both reach it (its address is admitted in both zones).
2. **Given** zones with members, **When** the relay restarts, **Then** the isolation rules are rebuilt to exactly match the recorded memberships (no member gains or loses reachability across the restart).
3. **Given** a node that is a member of one or more zones, **When** that node is deleted, **Then** it is removed from all of them and from the corresponding isolation rules.
4. **Given** a deleted member node whose address is later reused by a new node, **When** the new node is registered, **Then** it is NOT a member of the deleted node's zones (no inherited reachability).

---

### Edge Cases

- **Reachability is per shared zone, not transitive**: if A shares zone 1 with B and zone 2 with C, B and C still cannot reach each other (they share no zone).
- **Node ↔ relay traffic** is unaffected by zones (a node always reaches the relay; zones only govern node-to-node forwarding).
- **Joining a node that is not yours** → refused as not found (no enumeration of others' nodes).
- **Wrong password vs nonexistent zone** → identical generic error (no zone-name enumeration).
- **Leaving / viewing a zone you have no node in** → not found.
- **Creating a zone never auto-joins the owner's nodes**; an owner with no joined node still has reachability to nobody until they join.
- **Stale rules after an unclean shutdown** → reconciled at startup from the database.
- **Empty zone** (no members) has no admitted reachability; its isolation rule admits nothing.
- **Same node id reused in join/leave concurrently** → the membership set stays consistent (idempotent add, exact remove).

---

## Requirements *(mandatory)*

### Functional Requirements

**Zone creation**

- **FR-001**: An authenticated user MUST be able to create a zone with a globally unique name and a password; the creator is recorded as the zone's owner.
- **FR-002**: Zone names MUST be globally unique; creating a zone with an existing name MUST be refused with a conflict.
- **FR-003**: The system MUST validate the zone name (non-empty, within a length limit) and the password (meeting a documented minimum), refusing violations with a validation error.
- **FR-004**: Creating a zone MUST NOT automatically add any node to it.

**Join & reachability**

- **FR-005**: An authenticated user MUST be able to join one of their own nodes to a zone by supplying the zone name, the password, and the node.
- **FR-006**: A join MUST verify the password; a wrong password OR a nonexistent zone MUST return the SAME generic error (no disclosure of zone existence).
- **FR-007**: A join naming a node the caller does not own MUST be refused as not found.
- **FR-008**: Joining a node that is already a member MUST be an idempotent success (no duplicate membership).
- **FR-009**: Nodes that are members of the same zone MUST be able to exchange tunnel traffic with each other; nodes that share no zone MUST NOT be able to reach each other (default-deny preserved from feature 003).
- **FR-010**: A node MUST be able to belong to multiple zones simultaneously and be reachable within each.

**Leave**

- **FR-011**: An authenticated user MUST be able to remove one of their own nodes from a zone, after which the node loses reachability to that zone's members.
- **FR-012**: Leaving a zone MUST NOT affect the node's membership of, or reachability within, any other zone.
- **FR-013**: A leave for a node that is not a member (or not owned by the caller) MUST be refused as not found, changing nothing.

**Visibility**

- **FR-014**: An authenticated user MUST be able to list the zones they participate in (own, or have a member node in), indicating which they own.
- **FR-015**: A member (or owner) of a zone MUST be able to view all of that zone's members, each shown with node name, address, and owning username — including other users' nodes (full transparency).
- **FR-016**: A user who neither owns nor has a node in a zone MUST NOT be able to view its members (refused as not found, no zone disclosure).

**Consistency & lifecycle**

- **FR-017**: The database MUST be the source of truth for zones and memberships; at startup the relay MUST rebuild all isolation rules to exactly match the recorded memberships.
- **FR-018**: Deleting a node (feature 004) MUST remove it from every zone it belonged to and from the corresponding isolation rules, so a later node reusing its address does NOT inherit any membership.
- **FR-019**: All zone operations MUST require authentication and MUST use the shared error envelope and global rate limiting established in earlier features; zone passwords MUST be stored hashed and MUST NOT appear in logs.

### Key Entities

- **Zone**: A password-protected group. Attributes: id, globally unique name, password (stored hashed), owner (the creating user), creation time.
- **Zone membership**: An association of one node with one zone (with a join time). A node may have many; a zone may have many. The owner of a zone and the owner of a member node may be different users.
- **Isolation rule set**: The relay-side representation of a zone — the set of member node addresses plus the rule that admits same-zone traffic. Derived from memberships; rebuilt from the database at startup. Cross-zone traffic remains denied by default.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Two nodes that join the same zone can exchange tunnel traffic; two nodes that share no zone cannot — verified 100% of the time.
- **SC-002**: A node that joins two zones is reachable from peers in each; a peer in zone A and a peer in zone B (sharing only that node) still cannot reach each other.
- **SC-003**: After a relay restart, the isolation rules exactly match the recorded memberships — no member gains or loses reachability across the restart (100%).
- **SC-004**: Deleting a member node removes it from all its zones and rules; a new node that reuses the freed address is in zero of those zones (no inherited reachability), verified 100%.
- **SC-005**: A join with a wrong password and a join for a nonexistent zone are indistinguishable in outcome (no zone-name enumeration), verified across sampled attempts.
- **SC-006**: Zone-name uniqueness holds: a second creation with the same name is rejected 100% of the time.
- **SC-007**: Within a zone, every member can see every member node's name, address, and owner; a non-member is refused — verified for both member and non-member callers.
- **SC-008**: Leaving a zone removes only that zone's reachability; a node in two zones that leaves one retains the other (100%).

---

## Assumptions

- Builds on features 001–004: authenticated users + JWT (002), nodes with assigned
  addresses (004), the running WireGuard interface and the default-deny nftables
  forward chain (003), argon2id hashing, structured logging, the shared error
  envelope, and the global rate limiter — all reused.
- A zone maps to one isolation set of member node addresses; same-set traffic
  (source and destination both in the set) is admitted, everything else stays denied
  (DESIGN §6). Reachability is governed only for node-to-node forwarded traffic.
- Zone names are globally unique and are the address by which users join (the
  "name + password" model, DESIGN §7); the join endpoint does not disclose whether a
  given name exists (no-enumeration), mirroring the login no-enumeration choice.
- Membership is at node granularity: a user picks which of their nodes joins
  (DESIGN §6); the same node may be in many zones.
- Zone passwords use the same minimum strength as user passwords (≥ 8 characters);
  zone names follow the same length limit as node/user names (≤ 64 characters).
- Full member transparency within a zone is intentional (DESIGN §5.3): any member
  sees all member nodes' name, address, and owning username.
- This feature establishes the owner (creator) but does NOT implement owner-only
  management — changing the zone password, kicking a member, or deleting a zone are
  feature 006 (zone-owner-controls).
- This feature does NOT implement full user-deletion cascade (feature 008); it only
  ensures node deletion (the path that exists in feature 004) cleans up memberships
  and rules.
- IPv4 only; the relay runs with the privilege required to manage the isolation
  rules (feature 003).
