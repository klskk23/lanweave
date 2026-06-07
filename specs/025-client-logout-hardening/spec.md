# Feature Specification: Client Logout Hardening

**Feature Branch**: `025-client-logout-hardening`

**Created**: 2026-06-07

**Status**: Draft

**Input**: User description: "退出登录加固：服务器 API 网络不可达（3 次 1s 重试仍失败）时阻止退出登录并弹两键窗（取消 / 强制退出逃生口），避免在服务端留下无人可清的孤儿 node；登出额外吊销本设备 refresh token。完整设计见 docs/ROADMAP.md 行 586-616。依赖 017（退出编排、DeleteNode、confirmLogout）、024（refresh-token 吊销端点 + 惰性刷新）、018（防火墙拆除在登出路径）。"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Block logout when the server is unreachable (Priority: P1)

A signed-in user clicks "Log out" while their device cannot reach the server's
control API (the home/office network is down, the server is offline, or the
public endpoint is temporarily unroutable). Today the app would still wipe the
local session and device identity, leaving a registered node on the server that
nobody can ever remove. Instead, the app now attempts to remove the remote node
first; if every attempt fails purely because the server is unreachable, the app
**stops** the logout, changes nothing locally (the user stays connected and
signed in), and shows a clear prompt explaining that logout was blocked to avoid
leaving an orphaned device on the server.

**Why this priority**: This is the core of the slice — preventing the orphaned
("zombie") node that the current always-clear-local behavior creates. Without it
the feature delivers no value. It is independently demonstrable on its own.

**Independent Test**: With the device signed in and connected, make the control
API unreachable, click "Log out", and confirm: (a) the app retries the remote
removal a fixed number of times over a few seconds, (b) it then shows the
two-button blocked prompt, (c) choosing "Cancel" leaves the device fully
connected and signed in with no local change, and (d) the server still lists the
node.

**Acceptance Scenarios**:

1. **Given** a signed-in, connected device and an unreachable control API,
   **When** the user confirms log out, **Then** the app attempts to remove the
   remote node, retries on failure up to the retry limit at a fixed interval
   showing an in-progress indicator, and after the final failure shows a
   two-button prompt ("Cancel" / "Force log out anyway") without touching the
   tunnel, firewall, or any local credential.
2. **Given** the blocked prompt is showing, **When** the user chooses "Cancel",
   **Then** nothing changes: the tunnel stays up, the firewall rules stay in
   place, the session and device key remain stored, and the user is still on the
   main panel — re-opening the menu and logging out again is possible.
3. **Given** the control API is reachable again, **When** the user retries log
   out, **Then** the remote node is removed and logout completes normally (see
   User Story 2).

---

### User Story 2 - Clean logout removes the remote node and revokes this device (Priority: P1)

When the server **is** reachable, logging out removes this device's node on the
server first (while the tunnel is still up — the control API is reached over the
public network independently of the tunnel), then tears down the local side and
revokes this device's refresh token so no renewable session credential is left
behind. The user lands back on the setup wizard, ready to onboard again.

**Why this priority**: This is the success path the blocking logic guards. It
must leave **no** residue on either side: no node on the server, no renewable
refresh token, no local secrets. Equal P1 with US1 because the feature's promise
is "logout leaves nothing behind, on either end."

**Independent Test**: With a reachable server, log out and confirm the node
disappears from the server's device list, this device's stored refresh token is
revoked server-side, all local credentials are cleared, and the app returns to
the wizard.

**Acceptance Scenarios**:

1. **Given** a reachable server, **When** the user confirms log out, **Then**
   the remote node is removed, the tunnel and firewall rules are torn down, this
   device's refresh token is revoked on the server, all locally stored
   credentials (session token, refresh token, device key) and local state are
   cleared, and the app returns to the wizard.
2. **Given** the device's node was already removed on the server (e.g. an admin
   deleted it, or a prior interrupted logout already removed it), **When** the
   user logs out, **Then** the app treats "node not present" as success and
   proceeds with the local teardown and refresh-token revocation rather than
   blocking.
3. **Given** the server is reachable but the stored access session has expired,
   **When** the user logs out, **Then** the app silently renews the session and
   completes the removal; if renewal also fails it asks the user to sign in again
   and retries removal after a successful sign-in.

---

### User Story 3 - Force-logout escape hatch (Priority: P2)

A user whose server is permanently gone (decommissioned, lost, or unreachable
indefinitely) must not be trapped in a signed-in state forever. From the blocked
prompt they can choose "Force log out anyway", which performs the full local
teardown — disconnect tunnel, remove firewall rules, clear all local
credentials and state — and returns to the wizard, knowingly accepting that an
orphaned node remains on the server.

**Why this priority**: Without an escape hatch the blocking behavior could
permanently strand a user. It is P2 (not P1) because it is the deliberate-loss
fallback, only reached after the P1 block fires.

**Independent Test**: With an unreachable server, log out, and from the blocked
prompt choose "Force log out anyway"; confirm the tunnel goes down, firewall
rules are removed, all local credentials/state are cleared, and the app returns
to the wizard (accepting the server-side orphan).

**Acceptance Scenarios**:

1. **Given** the blocked prompt is showing, **When** the user chooses "Force log
   out anyway", **Then** the app disconnects the tunnel, removes firewall rules,
   clears all local credentials and state, and returns to the wizard.
2. **Given** a forced logout completed, **When** the user inspects the server,
   **Then** the orphaned node may still be listed (this is the accepted
   trade-off of forcing) and is left for an administrator to clean up.

---

### Edge Cases

- **Server reachable but returns a server-side error (5xx) or its certificate
  changed** (not a network-layer failure): logout is **not** blocked. The app
  falls back to the prior behavior — clear local state and warn the user that the
  remote node may still be registered. Blocking is reserved strictly for
  network-layer unreachability.
- **Session expired (401) during logout**: lazy session renewal normally renews
  it transparently; only if renewal itself fails does the app prompt for a fresh
  sign-in. If the user cancels that sign-in, logout is aborted and nothing
  changes locally.
- **Partial progress on retry**: if an earlier attempt actually removed the node
  but its response was lost, a later attempt sees "node not present" and treats
  it as success (idempotent) rather than an error.
- **User dismisses the in-progress indicator / closes the window mid-retry**:
  the logout attempt is abandoned with no local change; the device remains
  connected and signed in.
- **Refresh-token revocation call fails after the node was removed**: the node
  removal (the residue that matters most) already succeeded; local teardown
  still completes so the user is logged out. Revocation is best-effort at this
  point and does not re-block the logout.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: On logout confirmation, the system MUST attempt to remove this
  device's node on the server **before** tearing down any local state, while the
  tunnel is still connected.
- **FR-002**: The system MUST retry the remote node removal on failure up to a
  fixed maximum of **3 attempts** at a fixed **1-second** interval, showing an
  indeterminate in-progress indicator during the attempts.
- **FR-003**: When all 3 attempts fail due to **network-layer unreachability**
  (timeout or connection refused), the system MUST **block** the logout: it MUST
  NOT disconnect the tunnel, MUST NOT remove firewall rules, and MUST NOT clear
  any local credential or state.
- **FR-004**: On a blocked logout, the system MUST present a two-option prompt
  whose choices are "Cancel" (default, keep everything as-is) and "Force log out
  anyway" (proceed with local-only teardown). No "Retry" button is offered;
  retrying is done by closing the prompt and choosing log out again.
- **FR-005**: Choosing "Cancel" on the blocked prompt MUST leave the device in
  exactly its pre-logout state — connected, signed in, all credentials and
  firewall rules intact.
- **FR-006**: Choosing "Force log out anyway" MUST perform the full local
  teardown (disconnect tunnel, remove firewall rules, clear all local
  credentials and state, return to wizard), accepting that the server-side node
  remains.
- **FR-007**: On a successful remote removal — or when the server reports the
  node is **already absent** — the system MUST proceed to disconnect the tunnel,
  remove firewall rules, revoke this device's refresh token on the server, clear
  all local credentials (session token, refresh token, device key) and local
  state, and return to the wizard.
- **FR-008**: During logout the system MUST revoke this device's refresh token
  on the server so no renewable session credential remains after logout.
- **FR-009**: If the stored access session has expired during logout, the system
  MUST attempt silent renewal; if renewal fails it MUST prompt for a fresh
  sign-in and, on success, retry the removal. If the user cancels the sign-in,
  the logout MUST be aborted with no local change.
- **FR-010**: For server-reachable failures that are **not** network-layer
  unreachable (server-side errors, changed certificate), the system MUST NOT
  block; it MUST clear local state and warn the user that the remote node may
  still be registered.
- **FR-011**: All new user-facing text introduced by this feature (blocked-prompt
  title, body, both button labels, and the force-logout wording) MUST be
  available in both Simplified Chinese and English, following the existing
  localization mechanism.

### Key Entities

- **Device node**: the server-side registration representing this device. The
  residue this feature prevents is an orphaned node — one whose owning device has
  logged out locally but which was never removed server-side.
- **Refresh token (this device)**: the renewable session credential issued at
  login. Logout must revoke it server-side so a logged-out device leaves no
  usable session credential.
- **Local session material**: the access session token, the refresh token, the
  device private key, and the local state record — all cleared together on a
  completed (or forced) logout, all preserved together on a blocked/cancelled
  logout.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: When the server is reachable, 100% of logouts remove the device's
  node server-side and revoke its refresh token before any local credential is
  cleared (zero orphaned nodes, zero live refresh tokens after logout).
- **SC-002**: When the server is network-unreachable, 100% of logout attempts
  are blocked after at most 3 retries (completing within ~3 seconds) and leave
  the device fully connected and signed in unless the user explicitly forces.
- **SC-003**: A user facing a permanently unreachable server can still complete a
  forced logout in a single additional action from the blocked prompt (never
  permanently trapped signed-in).
- **SC-004**: After any completed logout (clean or forced), the device retains
  zero local credentials and the app returns to the onboarding wizard; after any
  cancelled/blocked logout, 100% of local state is unchanged.
- **SC-005**: The blocked prompt and force-logout wording render correctly in
  both Simplified Chinese and English.

## Assumptions

- The control API is reached over the public network (HTTPS) independently of the
  VPN tunnel, so the remote node can be removed while the tunnel is still up — and
  conversely the tunnel being up does not imply the control API is reachable.
- "Network-layer unreachable" means transport-level failures (timeout, connection
  refused / no route); any HTTP response from the server (including 4xx/5xx) is
  treated as "reachable" and does not trigger blocking.
- The refresh-token revocation endpoint and lazy session renewal from slice 024
  are available and used here; this slice does not redefine them.
- The remote node removal and "already absent" handling reuse the existing logout
  orchestration and node-deletion behavior from slice 017.
- Firewall teardown on the logout path reuses the mechanism introduced in slice
  018.
- A forced logout deliberately accepts a server-side orphaned node; cleaning it up
  is an administrator/operations task and is out of scope here.
- Server-side tooling to detect or reap orphaned nodes is out of scope for this
  slice.

## Dependencies

- **Slice 017** (client logout orchestration, node deletion, logout
  confirmation): the flow being hardened.
- **Slice 024** (refresh-token revocation endpoint + lazy session renewal):
  provides revocation on logout and the 401 renewal path.
- **Slice 018** (client firewall control on the logout path): firewall teardown
  step.
