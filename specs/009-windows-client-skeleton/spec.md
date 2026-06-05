# Feature Specification: Windows Client Skeleton & First-Run Wizard

**Feature Branch**: `009-windows-client-skeleton`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "继续完成ROADMAP.md 中的009" (ROADMAP feature 009: windows-client-skeleton-and-wizard)

Scope drawn from ROADMAP.md feature 009 and DESIGN.md §9.1–9.3: the first end-user
application. On a fresh machine it walks the user through a guided first-run setup —
choose the server, sign in or create an account, name this device — then generates the
device's key locally, registers the device with the server, stores the private key in
the operating system's secure store, and remembers the device so future launches skip
straight past setup. The VPN tunnel itself and the full management panel are later
features; this one ends at a registered, remembered device and a placeholder home area.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 新用户在新机器上完成首次设置 (Priority: P1)

A person installs the app on a new machine and opens it. A guided wizard asks for the
server address, then lets them either sign in to an existing account or create a new
account with an invite code, then asks them to name this device. The app generates the
device's identity locally, registers the device with the server, securely stores the
private key, and records the device's details. The user lands in the app, set up and
ready.

**Why this priority**: This is the entire purpose of the feature — turning a fresh
install into a registered, remembered device. Nothing else in the client can happen
until a user can get through setup. Independently testable by running setup end to end
against a real server and confirming the device is registered and remembered.

**Independent Test**: On a machine with no prior setup, complete the wizard (server →
account → device name) against a running server; confirm the server now has the device
registered to the user with an assigned address, the private key is in the OS secure
store, and the local record holds the device's address and the server's connection
details.

**Acceptance Scenarios**:

1. **Given** a fresh machine and a valid invite code, **When** the user enters the server address, creates an account, and names the device, **Then** the device is registered with the server and the user reaches the home area.
2. **Given** a fresh machine and an existing account, **When** the user enters the server address, signs in, and names the device, **Then** the device is registered and the user reaches the home area.
3. **Given** setup has completed, **When** it finishes, **Then** the private key is held in the OS secure credential store (never in a plain file) and the local record contains the device's assigned address, the server's public identity, the server endpoint, and the network range.

---

### User Story 2 — 重启后直接进入，不再要求设置 (Priority: P2)

A user who has already completed setup on this machine reopens the app. It recognizes
the machine is already set up and goes straight to the home area, without asking them to
register or sign in again.

**Why this priority**: A tool that re-runs setup every launch is unusable. This makes
the first run a one-time event. Hardens US1 by making its result durable.

**Independent Test**: After completing US1, close and reopen the app; confirm it bypasses
the wizard and shows the home area directly.

**Acceptance Scenarios**:

1. **Given** a machine that has completed setup, **When** the app is reopened, **Then** the wizard is not shown and the home area appears directly.
2. **Given** a machine that has never completed setup (or whose setup was cancelled), **When** the app is opened, **Then** the wizard is shown.

---

### User Story 3 — 向导安全、可控、可恢复 (Priority: P2)

The wizard behaves like a trustworthy part of one application: the user can go back or
cancel at any step, every network action shows immediate progress, every failure is
explained in plain language with a way to recover, the private key never leaves the
machine, and there is no way to weaken security from the UI. An interrupted setup leaves
no half-configured machine.

**Why this priority**: This is a security-relevant tool; users who don't trust or
understand the setup will abandon it or mis-handle their keys. These guarantees (drawn
from the project's UX and security principles) make the core flow safe and usable.

**Independent Test**: Drive each failure (unreachable server, untrusted certificate,
wrong password, used/invalid invite, duplicate device name) and confirm a clear message
and a recoverable state; cancel mid-wizard and confirm no key or local record is left;
confirm the UI never offers a "skip certificate check" control.

**Acceptance Scenarios**:

1. **Given** any wizard step, **When** the user chooses to go back or cancel, **Then** they can, and cancelling leaves no stored private key and no local setup record.
2. **Given** a network action (sign in, create account, register device), **When** it is in progress, **Then** the app shows immediate visible progress and never appears frozen.
3. **Given** a failure (wrong password, invalid or used invite, duplicate device name, unreachable or untrusted server), **When** it occurs, **Then** the user sees a specific, plain-language message and can correct and retry.
4. **Given** the wizard is on screen, **When** the user looks for a way to disable certificate verification, **Then** no such control exists in the UI.
5. **Given** the device was registered with the server but local saving then fails, **When** the error is shown, **Then** the user is not left stranded — they can retry or cancel cleanly, and the machine does not end up half-set-up.

---

### Edge Cases

- **Wrong server address / server unreachable**: clear message; the user stays on the step and can correct it.
- **Untrusted certificate**: a clear message explaining the server's certificate isn't trusted and that an operator must install the root certificate; no UI bypass.
- **Invalid or already-used invite code**: clear message; the user can re-enter.
- **Username already taken** (account creation): clear message; the user can choose another.
- **Wrong username or password** (sign in): a single clear "sign-in failed" message (no hint about which field was wrong).
- **Device name already used in the account**: clear message; the user picks a different name.
- **Server registers the device but local saving fails**: the user is told and can retry or cancel; retrying does not silently create a duplicate device, and the machine is never left half-configured.
- **App closed mid-wizard**: the next launch starts setup fresh (no partial secret or record left behind).
- **Already-set-up machine whose server-side device was later removed** (e.g., by an administrator): out of scope here — the app still opens to the home area; reconciling a missing device is a later feature.
- **Home area in this feature is a placeholder**: the full management panel (devices, zones, actions) and the tunnel on/off are later features.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: On launch with no existing local setup, the app MUST present a guided first-run wizard.
- **FR-002**: The wizard MUST collect the server address.
- **FR-003**: The wizard MUST let an existing user sign in with username and password, OR let a new user create an account using an invite code, username, and password.
- **FR-004**: The wizard MUST let the user name this device; a name already used within the account MUST be rejected with a clear message so the user can choose another.
- **FR-005**: The app MUST generate the device's key pair locally; the private key MUST never leave the machine and MUST be stored in the operating system's secure credential store, never in a plain file.
- **FR-006**: The app MUST register the device with the server by sending only the public key.
- **FR-007**: On successful registration, the app MUST persist locally — in a known per-user location, excluding any secret — the device's assigned address, the server's public identity, the server endpoint, the network range, the device name, and the server address.
- **FR-008**: Setup MUST be considered complete only when both server registration and local persistence (secure store + record) succeed; if either fails, the app MUST show a clear, actionable error and let the user retry or cancel, and MUST NOT leave the machine half-configured.
- **FR-009**: On a later launch where local setup already exists, the app MUST skip the wizard and go directly to the home area.
- **FR-010**: Every wizard step MUST let the user go back to the previous step and cancel the whole wizard; cancelling MUST NOT leave a stored private key or local setup record behind.
- **FR-011**: Every action that may exceed a short delay (sign in, create account, register device) MUST show immediate visible progress; the app MUST never appear frozen during these.
- **FR-012**: All user-facing errors MUST be human-readable and actionable (e.g., wrong credentials, invalid or used invite, duplicate device name, server unreachable, certificate not trusted) — never raw technical traces.
- **FR-013**: The wizard MUST support keyboard navigation: the primary/confirm action on Enter and cancel/back on Escape.
- **FR-014**: The option to skip server certificate verification MUST NOT appear in the UI; it MAY exist only as an advanced command-line option for troubleshooting.
- **FR-015**: The app MUST verify the server's certificate by default using the system trust store; a connection to an untrusted server MUST fail with a clear message unless the advanced command-line override is in effect.

### Key Entities

- **Device (node)**: This machine's registered node — its name, server-assigned address, key pair (public sent to the server; private kept local), owned by the signed-in user. One machine maps to one device.
- **Local setup record**: The persisted, non-secret marker that this machine is set up — device name and address, server address, server public identity, server endpoint, and network range. Its presence means "skip the wizard."
- **Device secret**: The private key, stored only in the operating system's secure credential store.
- **Account session**: The authenticated session obtained by signing in or creating an account, used to register the device. Short-lived; not the focus of this feature.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new user on a fresh machine completes setup (server → account → device name → done) in under 2 minutes.
- **SC-002**: After setup, the device's private key is present in the OS secure store and is never written to a plain file (verified 100% of runs).
- **SC-003**: After setup, the local record contains the device's address, the server's public identity, endpoint, and network range — enough to operate later without redoing setup.
- **SC-004**: On every subsequent launch, the app reaches the home area without asking to set up again (100%).
- **SC-005**: Each onboarding failure (wrong password, invalid or used invite, duplicate device name, unreachable or untrusted server) produces a specific, human-readable message and leaves the user able to retry — 100% of these cases.
- **SC-006**: At no point in the wizard is a "skip certificate verification" control shown to the user.
- **SC-007**: A cancelled or interrupted setup leaves no private key in the secure store and no local setup record (the next launch starts fresh) — no machine is ever left half-set-up.
- **SC-008**: Every network step shows visible progress within a fraction of a second of starting, so the window never appears frozen.

---

## Assumptions

- Builds on features 002 (invite, account creation, sign-in, session token) and 004
  (device registration, address assignment, server connection details). The wizard
  consumes those existing server capabilities; this feature adds the client-side
  onboarding, not new server endpoints.
- The primary (and, in this version, only) end-user surface is the Windows desktop app.
  The private key is kept in the operating system's secure credential store. The non-UI
  onboarding logic (talking to the server, persisting the local record, the step flow)
  is platform-neutral so it can be exercised on the build host against a real server;
  the secure-store binding is validated on the target operating system.
- One machine equals one device. Moving to a different machine means running the wizard
  again there; multi-device-per-machine and machine migration are out of scope.
- The VPN tunnel (bringing the connection up or down) is **out of scope** — a later
  feature. This feature ends at a registered, remembered device and a placeholder home
  area.
- The full management panel (device and zone views and actions) is **out of scope** — a
  later feature. Once set up, this feature shows a placeholder home area.
- Certificate trust uses the system trust store by default; self-signed deployments
  require operators to pre-install the root certificate. An advanced command-line
  override to skip verification exists for troubleshooting only and is never surfaced in
  the UI.
- Reconciling local setup against the server (e.g., a device an administrator later
  removed) is out of scope; this feature trusts the presence of the local record to mean
  "already set up."
- Standard desktop UX expectations apply: sub-second feedback on actions and setup
  completion within a couple of minutes.
