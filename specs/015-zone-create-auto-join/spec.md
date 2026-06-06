# Feature Specification: Zone Create Auto-Join

**Feature Branch**: `015-zone-create-auto-join`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "完善客户端逻辑，创建zone时应该将自己直接默认加入该zone 而不是还需要手动再次加入"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Creating a zone makes me a member immediately (Priority: P1)

A user opens the desktop main panel and creates a new zone by entering a name and
password. As soon as the zone is created, the user's own device is already a member
of that zone — it appears in the zone's member list, and the device can communicate
with other members that later join. The user does NOT have to perform a second
"join" action or re-enter the zone password.

**Why this priority**: This is the entire feature. The current two-step flow
(create, then separately join) is counter-intuitive — a creator expects to be inside
the zone they just created. Removing the redundant second step is the whole user
value.

**Independent Test**: From the main panel, create a zone and then inspect the zone's
member list without taking any further action; the creator's device is present. End
to end this is the only flow that must work for the feature to deliver value.

**Acceptance Scenarios**:

1. **Given** a logged-in user whose device is registered as a node, **When** the user
   creates a zone named "alpha", **Then** the response indicates the user is the owner
   AND the user's device is already a member of "alpha" (no second action required).
2. **Given** the user just created zone "alpha" from the main panel, **When** the panel
   refreshes the member view, **Then** the creator's device (name + assigned IP) is
   listed as a member.
3. **Given** the creator's device and a second member device are both in zone "alpha",
   **When** traffic flows between them, **Then** they can reach each other (the creator
   is a real, traffic-eligible member, not just a database row).

---

### User Story 2 - Joining someone else's zone still works (Priority: P2)

A user can still join a zone they did not create by supplying that zone's name and
password through the existing "join zone" action. Auto-join on create must not remove
or break the ability to join other people's zones.

**Why this priority**: The create-auto-join change must not regress the existing
join-others flow, which remains the only way to enter a zone you don't own.

**Independent Test**: With a zone owned by user A, have user B join it via the join
action and confirm B becomes a member — unchanged from today.

**Acceptance Scenarios**:

1. **Given** zone "alpha" owned by another user, **When** the current user joins it with
   the correct password, **Then** the current user's device becomes a member.
2. **Given** zone "alpha" owned by another user, **When** the current user joins with a
   wrong password, **Then** the join is rejected and no membership is created.

---

### Edge Cases

- **Create with no usable device identity**: If a create request arrives without a
  device node identifier (e.g., an older client or a script that only wants to create
  the zone), the system MUST still create the zone with the caller as owner and simply
  add no member — preserving the prior create-only behavior (backward compatible).
- **Create naming a device the caller does not own**: If the create request names a
  device that does not belong to the requesting user, the request MUST be rejected and
  NO zone is created (no partial state, no orphaned zone).
- **Partial failure during create+join**: If the zone is created but the membership
  step cannot be completed, the system MUST leave nothing behind — the half-created
  zone is removed so the user sees a clean error rather than a zone they are not in.
- **Duplicate zone name**: Unchanged from existing behavior — creating a zone whose
  name is already taken fails before any membership is attempted.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When creating a zone, the system MUST be able to add the creator's own
  device as a member of that zone within the same create operation, with no separate
  join step required from the user.
- **FR-002**: The create operation MUST accept an optional identifier for the creator's
  device. When supplied, the device is auto-joined; when omitted, the zone is created
  with the caller as owner and no member (backward-compatible create-only behavior).
- **FR-003**: Before auto-joining, the system MUST verify the named device belongs to
  the requesting user. A device the caller does not own MUST cause the request to fail
  and MUST NOT create the zone.
- **FR-004**: The create-with-auto-join operation MUST be atomic from the user's
  perspective: the outcome is either {zone created AND creator's device is a member AND
  network isolation state is consistent} OR {nothing created AND an error returned}.
  No state where the zone exists but the creator is not a member, or where the member
  exists without matching isolation state, may persist.
- **FR-005**: An auto-joined creator MUST be a full, traffic-eligible member — able to
  communicate with other members of the zone exactly as if they had joined manually.
- **FR-006**: The desktop client MUST, when a user creates a zone, automatically use the
  current device as the joined device; the user is not asked to choose or to opt out.
- **FR-007**: After a successful create, the desktop client MUST refresh so the creator
  sees their own device in the zone's member list without further action.
- **FR-008**: The existing flow for joining a zone the user does not own (name +
  password) MUST remain available and unchanged.

### Key Entities *(include if feature involves data)*

- **Zone**: A named, password-protected group with one owner (the creating user). Has
  zero or more member devices and an associated network-isolation grouping.
- **Membership**: The relationship that places a specific device inside a zone, making
  it eligible to communicate with the zone's other members.
- **Device (node)**: A user-registered endpoint with an assigned address. A create
  request may name the caller's device to be auto-joined; the device must belong to the
  requesting user.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Creating a zone from the desktop panel results in the creator being a
  member in a single user action (down from two: create + join).
- **SC-002**: 100% of zones created via the desktop client have the creator's device as
  a member immediately after creation, verified by the member list.
- **SC-003**: A creator can communicate with a second member of a zone they created
  without performing any manual join step.
- **SC-004**: Create requests that name a device not owned by the caller are rejected
  100% of the time, leaving no zone behind.
- **SC-005**: Existing create requests that do not name a device continue to succeed
  with the prior create-only result (no regression for callers that omit the device).

## Assumptions

- The creator's device is already registered (a node exists for it) before they create
  a zone — true in the main-panel flow, which only appears after onboarding registers
  the device.
- "Auto-join the creator" means joining the single device running the desktop client at
  create time, not every device the user owns.
- The zone password the user types on the create form is the one used for the zone;
  auto-joining the creator does not require re-entering or re-verifying that password,
  because the creator just set it.
- The existing network-isolation mechanism that gives same-zone members reachability is
  reused unchanged; this feature only ensures the creator's device is placed into it at
  create time.
- Backward compatibility matters: callers (including existing tests) that create a zone
  without naming a device must keep working with the create-only result.
