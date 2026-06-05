# Feature Specification: Windows Client Main Panel

**Feature Branch**: `011-windows-client-main-panel`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "ROADMAP.md 011" (ROADMAP feature 011: windows-client-main-panel)

Scope drawn from ROADMAP.md feature 011 and DESIGN.md §9.4: the full management panel for
the desktop client. It shows this device's status, all of the user's devices (marking this
machine), and the zones this device belongs to (with their members), and lets the user
create and join zones, leave zones, and — on zones they own — change the password, remove a
member, or delete the zone, all without touching a command line. Online status refreshes on
its own.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 一眼看清我的网络 (Priority: P1)

When a set-up user opens the app, the panel shows this device's status (its address, its
connection switch, and when it was last seen), a list of all their devices with this
machine marked and each device's online state, and the zones this device is in. Expanding a
zone reveals its members — each member's device name, owner, and address. Online status
refreshes by itself.

**Why this priority**: Seeing the network — who's online, which zones exist, who's in them —
is the panel's core value and the prerequisite for every management action. Independently
testable by opening the panel against a real account and confirming the displayed devices,
zones, and members match the server.

**Independent Test**: With an account that has devices and zone memberships, open the panel
and confirm: this machine is marked among the device list; each device shows online/offline;
the zones list matches the device's memberships; expanding a zone shows every member's name,
owner, and address.

**Acceptance Scenarios**:

1. **Given** a set-up user with several devices, **When** they open the panel, **Then** all their devices are listed, this machine is clearly marked, and each device shows its address and online state.
2. **Given** the device is in one or more zones, **When** the user expands a zone, **Then** every member is shown with its device name, owner's username, and address.
3. **Given** the panel is open, **When** time passes, **Then** online status (and last-seen) refresh automatically without the user doing anything.
4. **Given** the panel is open, **When** the user looks at the top, **Then** this device's address, connection switch, and last-seen time are visible.

---

### User Story 2 — 创建与加入隔离区 (Priority: P1)

The user can create a new zone by entering a name and password, and can join someone else's
zone by entering its name and password (choosing which of their devices joins). They can
also leave a zone they are in. Each action updates the panel to reflect the new state.

**Why this priority**: Joining and creating zones is how users actually form private groups —
the main thing the product is for, driven entirely from the UI (no command line).

**Independent Test**: Create a zone → it appears in the user's zones. Join another user's
zone by name + password → it appears with this device as a member. Leave it → it disappears.

**Acceptance Scenarios**:

1. **Given** the panel is open, **When** the user creates a zone with a name and password, **Then** the zone appears in their zones list and they are its owner.
2. **Given** another user's zone exists, **When** the user joins it by name + password (selecting a device), **Then** the zone appears with this device as a member.
3. **Given** the user is in a zone, **When** they leave it, **Then** the zone (or this device's membership) is removed from the panel.
4. **Given** a wrong zone password or a duplicate zone name, **When** the user submits, **Then** a clear message explains the problem and the panel stays usable.

---

### User Story 3 — 隔离区拥有者管理 (Priority: P2)

On zones the user owns, the panel offers owner-only controls: change the zone password,
remove (kick) a member, and delete the whole zone. These controls appear only on owned
zones, and every destructive action asks for confirmation naming the specific zone or
member.

**Why this priority**: Owners need to curate their zones (rotate passwords, remove people,
tear a zone down). It builds on US1/US2 and is gated to owners. Confirmation prevents costly
mistakes.

**Independent Test**: On an owned zone, change the password (an outsider can no longer join
with the old one), kick a member (they leave the member list), and delete the zone (it
disappears). On a zone the user does not own, confirm none of these controls are shown.

**Acceptance Scenarios**:

1. **Given** a zone the user owns, **When** they change its password, **Then** the change takes effect (existing members keep access; the old password no longer lets new devices join).
2. **Given** a zone the user owns with another member, **When** they kick that member (after confirming), **Then** the member is removed from the zone.
3. **Given** a zone the user owns, **When** they delete it (after confirming), **Then** the zone disappears for everyone and its name becomes available again.
4. **Given** a zone the user does NOT own, **When** they view it, **Then** no change-password, kick, or delete controls are shown.
5. **Given** any destructive action, **When** the confirmation is shown, **Then** it names the specific zone or member; cancelling makes no change.

---

### User Story 4 — 会话与一致的体验 (Priority: P2)

The panel obtains the user's session without making them redo setup — reusing a still-valid
saved session and prompting a sign-in only when none is available or it has expired. Long
operations show progress, the panel never appears frozen, errors are written for humans, and
what the panel shows stays consistent with the server after every action.

**Why this priority**: A management surface that silently fails, freezes, or forgets the user
every launch is unusable. These guarantees (from the project's UX and security principles)
make the panel trustworthy. Hardens US1–US3.

**Independent Test**: Open the panel with a valid saved session → no sign-in needed; with an
expired/absent session → a sign-in prompt, then the panel loads. Drive each error (wrong
password, duplicate name, server unreachable) → a clear message, panel stays usable. After
each successful action, the relevant list reflects the new state.

**Acceptance Scenarios**:

1. **Given** a valid saved session, **When** the user opens the panel, **Then** it loads without asking for credentials.
2. **Given** no valid session, **When** the user opens the panel, **Then** they are prompted to sign in (username + password), and on success the panel loads.
3. **Given** any network action, **When** it is in progress, **Then** the panel shows immediate progress and never appears frozen.
4. **Given** a successful create/join/leave/owner action, **When** it completes, **Then** the affected list refreshes to match the server.
5. **Given** any failure, **When** it occurs, **Then** a specific, human-readable message is shown and the panel remains usable.

---

### Edge Cases

- **Wrong zone password on join**: clear message; the user can retry.
- **Duplicate zone name on create**: clear message; the user picks another name.
- **Not-owner action**: owner controls are hidden on un-owned zones; an attempted owner action is refused with a clear message.
- **Session expired mid-use**: the user is prompted to sign in again, then the action resumes.
- **Server unreachable**: a clear message; the panel stays open (last-known data shown) and actions fail gracefully rather than crashing.
- **Leaving the zone vs. removing self**: a user removes themselves by leaving; kick is for owners removing others.
- **Owner deletes a zone with other members**: every membership is cleared; affected users' panels reflect the zone's disappearance on refresh.
- **This device's record removed server-side** (e.g., by an administrator): the panel surfaces this clearly and guides the user rather than showing stale data forever.
- **Empty states**: a user with only this device and no zones sees friendly "nothing yet" views, not blank screens.
- **Offline devices**: shown as offline in the device and member lists, not hidden.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The panel MUST show this device's status: its VPN address, its connection switch (connect/disconnect), and its last-seen (last handshake) time.
- **FR-002**: The panel MUST list all of the signed-in user's devices, clearly marking which one is this machine, each with its address and online state.
- **FR-003**: The panel MUST list the zones this device belongs to; expanding a zone MUST show its members, each with the member's device name, owner's username, and address.
- **FR-004**: Online status and last-seen MUST refresh automatically on a regular interval, without user action.
- **FR-005**: The user MUST be able to create a zone by entering a name and password; on success it appears in their zones with them as owner.
- **FR-006**: The user MUST be able to join another user's zone by entering its name and password and selecting which of their devices joins; on success the zone appears with that device as a member.
- **FR-007**: The user MUST be able to leave a zone they are in; on success the membership is removed from the panel.
- **FR-008**: On zones the user owns, the panel MUST offer change-password, remove-member (kick), and delete-zone controls; these MUST NOT appear on zones the user does not own.
- **FR-009**: Every destructive action (leave zone, kick member, delete zone) MUST require explicit confirmation that names the specific zone or member; cancelling makes no change.
- **FR-010**: Every operation that may exceed a short delay MUST show immediate visible progress; the panel MUST never appear frozen.
- **FR-011**: All user-facing errors MUST be human-readable and actionable (wrong zone password, duplicate zone name, not-owner, server unreachable, session expired) and MUST leave the panel usable.
- **FR-012**: Management operations MUST use the signed-in user's session; the panel MUST reuse a still-valid saved session and prompt a sign-in only when none is available or it has expired, without redoing device setup.
- **FR-013**: A member viewing a shared zone MUST see every member's device name, owner's username, and address, regardless of who owns the zone.
- **FR-014**: After any successful operation, the panel MUST refresh the affected lists so the displayed data stays consistent with the server.
- **FR-015**: Field rendering MUST be uniform: addresses as `100.127.x.y`, a device shown with its owner's username, and times shown in the user's local zone.

### Key Entities

- **Device (node)**: One of the user's machines — name, VPN address, owner, online state, last-seen. Exactly one is "this machine".
- **Zone**: A private group — name, whether the signed-in user owns it, and its members. Owned zones expose owner controls.
- **Member**: A device that belongs to a zone, shown with its device name, the owner's username, and its address (the transparency view). It also carries the identifier an owner uses to remove it.
- **Session**: The signed-in user's authenticated session used for all management actions; reused while valid, re-established by a sign-in prompt when needed. Stored securely, never in a plain file.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can create a zone, have another user join it, and see that member (name + address) in the panel — entirely through the UI, with no command line.
- **SC-002**: A user can join another user's zone by name + password and see this device listed as a member within a couple of seconds.
- **SC-003**: A user can leave a zone and the panel reflects the change immediately.
- **SC-004**: A zone owner can change the password, kick a member, and delete the zone through the UI; non-owners never see those controls (verified 100%).
- **SC-005**: A member sees every other member's name and address in a shared zone (100% transparency).
- **SC-006**: Online status shown in the panel matches reality within the refresh interval (≤ 30 seconds).
- **SC-007**: Every destructive action shows a confirmation naming the specific entity; cancelling it changes nothing (100%).
- **SC-008**: Wrong zone password, duplicate zone name, and not-owner attempts each produce a specific, human-readable message and leave the panel usable (100% of these cases).
- **SC-009**: A user reaches the panel without redoing setup, and within a valid session is not asked to re-enter their password.

---

## Assumptions

- Builds on features 002 (account/session), 004 (devices + server info), 005 (zones:
  create/join/leave/members), 006 (owner controls: change password, kick, delete), 007
  (online status), 009 (the setup record + secure store), and 010 (the connection switch).
  Every operation the panel performs is already provided by the server; this feature is the
  management UI that consumes them.
- The signed-in user's session token is cached in the OS secure store (DESIGN §8) and reused
  until it expires; when it is absent or expired the panel prompts a sign-in (username +
  password) — it does not redo device setup. No secret is written to a plain file.
- The panel replaces the placeholder home area from features 009/010 and incorporates the
  connection switch from 010; the tunnel mechanics themselves are feature 010's concern.
- Online status refreshes on roughly the server's cadence (≤ 30 s, feature 007).
- Field rendering is uniform per the project's UX principle: addresses as `100.127.x.y`,
  devices shown with their owner's username, and timestamps in the user's local zone.
- Windows desktop is the surface. The management logic (the operations and the assembling of
  devices/zones/members for display) is validated automatically against a real running
  server; the visual rendering of the panel is validated manually on Windows (a documented,
  unavoidable GUI exception, consistent with features 009 and 010).
- Single user per machine; one device per machine (features 002/009). Cross-device sync of
  zone config and ownership transfer remain out of scope (v1.1).
