# Feature Specification: per-user-limits

**Feature Branch**: `023-per-user-limits`

**Created**: 2026-06-07

**Status**: Draft

**Input**: User description: "023 per-user-limits"

## Overview

The server currently places no ceiling on how many devices a single account may
register, nor on how many zones a single account may create. One account can
register devices without bound (consuming the address pool) and create zones
without bound (consuming the global zone-name space). This feature adds two
configurable, server-wide caps — a maximum number of **devices per user** and a
maximum number of **owned zones per user** — each defaulting to 10. The caps
apply uniformly to every regular account; the administrator account is exempt.
An operator may raise, lower, or disable each cap through server configuration.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Device cap protects the server from one account exhausting it (Priority: P1)

A regular user keeps adding devices to their account. Once they reach the
configured device cap, the next device registration is refused with a clear
message telling them they have reached their device limit. Removing an existing
device frees a slot, after which they may register again.

**Why this priority**: Unbounded device registration is the highest-impact abuse
vector — each device consumes a scarce VPN address, so a single account could
drain the whole pool and deny service to everyone. Enforcing the device cap is
the minimum viable slice and delivers standalone protection even if the zone cap
ships later.

**Independent Test**: Configure a device cap, register devices up to the cap,
confirm the next registration is refused with a device-limit message, delete one
device, confirm registration succeeds again.

**Acceptance Scenarios**:

1. **Given** a user who owns a number of devices equal to the device cap, **When** they attempt to register one more device, **Then** the registration is refused and they are told they have reached their device limit, and no new device is created.
2. **Given** a user who owns one fewer device than the cap, **When** they register a device, **Then** it succeeds and they are now at the cap.
3. **Given** a user at the device cap, **When** they delete one of their devices and then register a new one, **Then** the new registration succeeds (the deletion freed a slot).
4. **Given** two device-registration attempts arriving at nearly the same moment while the user is one device below the cap, **When** both are processed, **Then** at most one succeeds and the user never ends up holding more devices than the cap.

---

### User Story 2 - Owned-zone cap limits how many zones one account creates (Priority: P2)

A regular user keeps creating zones. Once the number of zones they own reaches
the configured zone cap, the next zone creation is refused with a clear message
telling them they have reached their zone limit. Deleting a zone they own frees a
slot. Joining a zone that someone else owns is never counted against this cap.

**Why this priority**: Zone names occupy a global, server-wide namespace, so an
account creating unbounded zones can squat names and bloat server state. Valuable
but secondary to the device cap, and independently shippable.

**Independent Test**: Configure a zone cap, have a user create zones up to the
cap, confirm the next creation is refused with a zone-limit message, delete one
owned zone, confirm creation succeeds again; separately confirm that joining
another user's zone while at the cap still succeeds.

**Acceptance Scenarios**:

1. **Given** a user who owns a number of zones equal to the zone cap, **When** they attempt to create another zone, **Then** the creation is refused and they are told they have reached their zone limit, and no new zone is created.
2. **Given** a user at the zone cap, **When** they delete one of their owned zones and then create a new one, **Then** the new creation succeeds.
3. **Given** a user who is at the zone cap on the zones they own, **When** they join a zone owned by a different user, **Then** the join succeeds — joining does not count against the owned-zone cap.
4. **Given** a user below the zone cap, **When** they create a zone, **Then** it succeeds and the owner count for that user increases by one.

---

### User Story 3 - Operator tunes the caps; admin and "unlimited" bypass them (Priority: P3)

An operator sets each cap in server configuration. If a cap is left unset, it
defaults to 10. Setting a cap to zero means "unlimited" for that resource. A
negative cap is a configuration error that stops the server from starting with a
clear message. The administrator account is never subject to either cap. Lowering
a cap below what some users already hold does not remove anything from those
users — it only prevents them from creating more until they drop below the new
cap.

**Why this priority**: Operability and escape hatches. The defaults already give
a working system (US1/US2), so configurability, the unlimited sentinel, and admin
exemption refine rather than gate the feature.

**Independent Test**: Start the server with each cap unset (verify default 10),
with a cap of zero (verify unlimited), with a negative cap (verify startup fails),
and confirm the admin account can exceed any positive cap.

**Acceptance Scenarios**:

1. **Given** a configuration that does not mention the device or zone cap, **When** the server starts, **Then** both caps take effect at the default value of 10.
2. **Given** a cap configured as zero, **When** a user creates many of that resource, **Then** they are never refused on account of the cap (zero means unlimited).
3. **Given** a cap configured as a negative number, **When** the server is started, **Then** it refuses to start and reports the invalid cap.
4. **Given** the administrator account already holding more of a resource than a positive cap, **When** the admin creates another, **Then** it succeeds — the admin is exempt.
5. **Given** a positive cap that an operator lowers below a user's current count, **When** the server runs with the new cap, **Then** the user keeps everything they already had and continues using it, but cannot create more until their count drops below the new cap.

---

### Edge Cases

- A user sitting exactly at the cap is blocked from creating the next item; a user one below the cap is allowed exactly one more.
- Deleting a device or an owned zone frees a slot that becomes usable immediately within the same session.
- Lowering a cap below a user's existing count never evicts existing devices/zones; it only blocks new creation (grandfathering).
- A cap of zero is treated as unlimited, not as "block everything."
- An unset cap is treated as the default (10), distinct from an explicit zero.
- A negative cap is rejected at startup rather than silently clamped.
- The administrator account is exempt from both caps regardless of how many devices/zones it holds.
- Joining a zone owned by another user is unaffected by the owned-zone cap, even when the joining user is at their own cap.
- Concurrent creation attempts near the cap must never let a user exceed the cap.
- The device-limit refusal and the zone-limit refusal are distinguishable from each other and from unrelated failures (e.g. duplicate name, address pool exhausted).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST support a server-wide **maximum devices per user** cap and a server-wide **maximum owned zones per user** cap, each set in server configuration and applied identically to every regular account.
- **FR-002**: When a cap is not specified in configuration, the system MUST apply a default value of **10** for that cap.
- **FR-003**: When a regular user attempts to register a device and already owns a number of devices greater than or equal to the device cap, the system MUST refuse the registration and create no new device.
- **FR-004**: When a regular user attempts to create a zone and already owns a number of zones greater than or equal to the zone cap, the system MUST refuse the creation and create no new zone.
- **FR-005**: The device cap MUST count only the devices the user currently owns; deleting a device MUST free a slot so a subsequent registration can succeed.
- **FR-006**: The zone cap MUST count only the zones the user currently **owns** (i.e. created); deleting an owned zone MUST free a slot. Zones the user merely joined (owned by others) MUST NOT count toward this cap, and joining another user's zone MUST NOT be limited by it.
- **FR-007**: A cap value of **zero** MUST be treated as **unlimited** for that resource (no refusal on account of the cap).
- **FR-008**: A **negative** cap value MUST be rejected as invalid configuration, preventing the server from starting and reporting the offending setting.
- **FR-009**: The **administrator** account MUST be exempt from both caps and never refused a device registration or zone creation on account of a cap.
- **FR-010**: Lowering a cap below a user's existing count MUST NOT remove, disable, or evict any of that user's existing devices or zones; it MUST only prevent new creation until the user's count drops below the cap (grandfathering).
- **FR-011**: A device-cap refusal and a zone-cap refusal MUST each be conveyed as a distinct, recognizable outcome that the client can tell apart from one another and from unrelated failures.
- **FR-012**: The client MUST present a device-cap refusal to the user as a clear, localized message at the point of device setup, and a zone-cap refusal as a clear, localized message at the point of zone creation, in each supported interface language.
- **FR-013**: Cap enforcement MUST be atomic with respect to concurrent creation requests: simultaneous attempts MUST NOT allow a user to exceed the cap.

### Key Entities *(include if feature involves data)*

- **Device allowance**: For a given regular user, the relationship between the number of devices they currently own and the configured device cap. Determines whether a new device registration is permitted.
- **Owned-zone allowance**: For a given regular user, the relationship between the number of zones they currently own (created) and the configured zone cap. Joined-but-not-owned zones are excluded. Determines whether a new zone creation is permitted.
- **Limit configuration**: The two server-wide caps (max devices per user, max owned zones per user), each with the semantics: unset → default 10, zero → unlimited, negative → invalid, positive → that ceiling.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A regular user holding devices equal to the device cap is prevented from registering an additional device 100% of the time, and is shown a message that identifies the device limit specifically.
- **SC-002**: A regular user owning zones equal to the zone cap is prevented from creating an additional zone 100% of the time, and is shown a message that identifies the zone limit specifically.
- **SC-003**: After a user at a cap deletes one device (or one owned zone), they can immediately create exactly one replacement and no more — confirming deletion frees exactly one slot.
- **SC-004**: A user at their owned-zone cap can still successfully join a zone owned by another user.
- **SC-005**: With a cap configured as zero, a user can create well beyond the default of 10 of that resource without ever being refused on account of the cap.
- **SC-006**: With a cap unset, the effective cap is exactly 10 (the 11th creation is refused).
- **SC-007**: A server configured with a negative cap fails to start and reports the invalid setting; it never starts with a silently altered value.
- **SC-008**: The administrator account can create devices and zones beyond any positive cap with zero refusals.
- **SC-009**: After an operator lowers a cap below a user's current count, that user retains and keeps using all pre-existing devices/zones, and is refused only on new creation until their count falls below the new cap.
- **SC-010**: Under concurrent creation attempts at the boundary, the number of devices/zones a user ends up owning never exceeds the cap.

## Assumptions

- "Device" corresponds to a registered client node, and "owned zone" to a zone the user created (is owner of); these are the existing domain concepts and are not redefined by this feature.
- The caps are global (one value each, applied to all regular users). Per-user individual overrides are explicitly out of scope for this feature and deferred to a later iteration.
- The administrator identity is already established and recognizable to the server (an existing account attribute), so exemption requires no new identity mechanism.
- Configuration is read at server startup; changing a cap takes effect on the next start (no live reload), consistent with how other server settings behave.
- Counting is by distinct current resources owned by the user; historical/cumulative creation counts are not tracked (deletion always frees a slot).
- The client surfaces the two new refusals in the existing onboarding (device setup) and main-panel (zone creation) flows, in both supported languages, reusing the established error-to-message presentation.

## Dependencies

- Device registration must exist (the device-cap enforcement point).
- Zone creation with an owner concept must exist (the zone-cap enforcement point).
- A recognizable administrator account attribute must exist (for exemption).
- The client localization mechanism must exist (for the two new localized messages).

## Out of Scope

- Per-user individually configurable caps (only a single global value per resource this iteration).
- Limiting how many zones a user may **join** (only owned/created zones are capped).
- Cumulative "lifetime registrations" style anti-abuse (deletion always frees a slot).
- Live/hot reload of cap configuration without a restart.
- Any change to address allocation, zone isolation, or other unrelated server behavior.
