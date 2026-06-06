# Feature Specification: Windows Client Administrator Elevation

**Feature Branch**: `013-windows-client-elevation`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "Fix the Windows client so it runs with administrator rights and can create the virtual network adapter without the user manually choosing 'Run as administrator'."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Connect after a normal launch (Priority: P1)

A Windows user installs the client, then starts it the ordinary way — double-clicking the
desktop or Start-menu shortcut. The operating system shows a single standard consent prompt
asking to allow the app to make changes. The user accepts, the app opens, and connecting the
tunnel succeeds: the virtual network adapter is created and traffic flows.

**Why this priority**: This is the whole point of the fix. Today a normal shortcut launch
runs without administrator rights, so creating the network adapter fails and connecting
reports "couldn't set up the network adapter." Without this story the client is unusable for
anyone who doesn't already know the manual "Run as administrator" workaround.

**Independent Test**: On a clean Windows machine, sign in as a standard desktop user, launch
the client from the shortcut (not via right-click), accept the consent prompt, and verify the
tunnel connects and the adapter appears — with no manual "Run as administrator" step.

**Acceptance Scenarios**:

1. **Given** the client is installed and not running, **When** the user launches it from the
   shortcut without elevated rights, **Then** the OS presents one elevation consent prompt.
2. **Given** the elevation prompt is shown, **When** the user accepts it, **Then** the client
   opens with the privileges it needs and connecting the tunnel creates the adapter
   successfully.
3. **Given** the client is connected after a normal launch, **When** the user checks their
   network adapters, **Then** the VPN adapter is present and the server is reachable.

---

### User Story 2 - Declining elevation is honest (Priority: P2)

A user launches the client, sees the consent prompt, and declines it (or is not permitted to
elevate). The client must not leave the user in a confusing or misleading state — it must not
appear to be connected, and it must not silently do nothing that looks like success.

**Why this priority**: A safe, understandable failure path protects users from believing they
are protected by the VPN when they are not. It is secondary to the happy path but required for
the feature to be trustworthy.

**Independent Test**: Launch the client, decline the consent prompt, and confirm the resulting
state is unambiguous — the user can tell the app is not running with the rights it needs and
is not connected.

**Acceptance Scenarios**:

1. **Given** the elevation prompt is shown, **When** the user declines it, **Then** the client
   does not present itself as connected.
2. **Given** the user declined elevation, **When** they look at the app's state, **Then** the
   outcome is understandable (the app did not pretend to succeed).

---

### User Story 3 - Already-elevated launch is clean (Priority: P3)

A user who already runs the client with administrator rights (for example via right-click
"Run as administrator", or in an environment where the app is launched elevated) should see no
second prompt and no relaunch — the app simply starts and works.

**Why this priority**: Avoids a regression where the elevation behavior double-prompts or
restarts itself for users who are already elevated. Lower priority because the happy path (US1)
already covers the common case.

**Independent Test**: Launch the client elevated (right-click → Run as administrator) and
confirm there is no additional prompt, no visible relaunch, and the app works normally.

**Acceptance Scenarios**:

1. **Given** the client is started with administrator rights, **When** it launches, **Then** it
   does not show an additional elevation prompt.
2. **Given** the client is started with administrator rights, **When** it launches, **Then** it
   does not relaunch itself.

---

### Edge Cases

- The user supplied a command-line option (e.g., the advanced certificate-bypass flag): the
  option must still take effect when the app proceeds with elevated rights.
- The user declines elevation: the app must not enter a loop of repeated prompts.
- The app is run on a non-Windows desktop build: its behavior is unchanged (no elevation step).
- The user double-clicks the shortcut several times: each unelevated launch behaves
  consistently (it does not spawn multiple stuck/zombie windows).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: On Windows, the desktop client MUST run with the administrator rights required to
  create the virtual network adapter.
- **FR-002**: When the client is launched without administrator rights, it MUST automatically
  request elevation through the operating system's standard consent mechanism, without the user
  having to manually choose "Run as administrator".
- **FR-003**: When the user grants elevation, the client MUST continue into its normal
  experience (first-run setup or main panel) with the rights needed to create the adapter.
- **FR-004**: When the user denies elevation, the client MUST NOT present a misleading state —
  in particular it MUST NOT appear connected and MUST NOT silently behave in a way the user
  could mistake for success.
- **FR-005**: When the client is already running with administrator rights, it MUST NOT prompt
  for elevation again and MUST NOT relaunch itself (no duplicate prompts, no relaunch loop).
- **FR-006**: Command-line options the user supplied MUST be preserved when the client proceeds
  or relaunches with elevated rights.
- **FR-007**: The elevation behavior MUST be limited to Windows; on other desktop platforms the
  client's startup behavior is unchanged.
- **FR-008**: Project documentation and any in-product guidance MUST accurately describe how the
  client obtains administrator rights, correcting the prior inaccurate statement that the
  installer grants the running app its elevation.

### Key Entities

This feature introduces no new data entities; it changes how the existing client process is
started on Windows.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A standard (non-admin) user launching the client from the shortcut reaches a
  connected tunnel after exactly one consent prompt, with zero manual "Run as administrator"
  steps.
- **SC-002**: 100% of shortcut launches that are not already elevated surface the elevation
  prompt.
- **SC-003**: Declining elevation results in zero cases where the user could mistake the app for
  "connected".
- **SC-004**: Launching the client already elevated produces zero additional prompts and zero
  relaunches.
- **SC-005**: On a clean Windows machine, the previously observed "couldn't set up the network
  adapter" failure no longer occurs for a normal shortcut launch when the user consents to
  elevation.

## Assumptions

- Creating the virtual network adapter inherently requires administrator rights on Windows; the
  requirement cannot be removed by reducing the app's privileges, so obtaining elevation is the
  correct approach.
- The user is in an interactive desktop session and is able to respond to the operating
  system's elevation consent prompt (the account can elevate, or an administrator approval is
  available).
- This feature builds on the existing Windows tunnel/adapter behavior (feature 010) and the
  installer/packaging (feature 012); it does not change the server.
- Non-Windows desktop builds exist for development/testing only and are unaffected.

## Out of Scope

- Creating the network adapter without administrator rights (not possible with the chosen
  virtual-network driver).
- Splitting the client into a privileged background helper plus an unprivileged UI (deferred to
  a later version).
- Enterprise silent-elevation / managed-deployment policies for environments where standard
  users cannot elevate interactively.
