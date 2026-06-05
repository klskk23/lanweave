# Feature Specification: Windows Client Tunnel

**Feature Branch**: `010-windows-client-tunnel`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "ROADMAP.md 完成010" (ROADMAP feature 010: windows-client-tunnel)

Scope drawn from ROADMAP.md feature 010 and DESIGN.md §6/§9: give the already-set-up
desktop client a Connect / Disconnect control that brings the VPN tunnel up and down. On
Connect, the device's assigned address becomes active on a virtual adapter and the device
can reach the server (and same-zone devices); on Disconnect, the adapter is removed. The
tunnel is assembled entirely from the locally stored device key and the recorded server
details — no credentials are re-entered — routes only the VPN range (split tunnel), and
stays alive while idle.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 一键连接，加入网络 (Priority: P1)

From the home area of a set-up device, the user clicks Connect. The device joins the VPN:
its assigned address becomes active on the machine, and the user can reach the server and
(when they share a zone) other devices. The connection is built from the device's stored
key and recorded server details — the user does not re-enter anything.

**Why this priority**: This is the point of the whole product — actually being on the
private network. Everything before it only prepared for this moment. Independently
testable by connecting and confirming the device's address is active and the server is
reachable.

**Independent Test**: On a set-up device, click Connect → the assigned `100.127.x.y`
address becomes active and the server's VPN address responds; with a second device in the
same zone, each can reach the other's VPN address.

**Acceptance Scenarios**:

1. **Given** a set-up device, **When** the user clicks Connect, **Then** the device's assigned VPN address becomes active on a virtual adapter and the server's VPN address is reachable.
2. **Given** two devices that share a zone and are both connected, **When** one addresses the other's VPN address, **Then** it is reachable.
3. **Given** a connected device, **When** the user uses ordinary (non-VPN) internet, **Then** that traffic is unaffected — only VPN-range traffic goes through the tunnel.
4. **Given** a set-up device, **When** the user connects, **Then** no credentials or keys are re-entered; the connection uses the locally stored key and recorded server details.

---

### User Story 2 — 一键断开，干净退出 (Priority: P1)

The user clicks Disconnect and the tunnel goes down: the virtual adapter and the VPN
address are removed and no VPN traffic flows. Closing the app also tears the tunnel down
cleanly, leaving no orphaned adapter.

**Why this priority**: A connection the user can't cleanly end (or that lingers after the
app closes) is unsafe and confusing. Disconnect is the necessary counterpart to Connect.

**Independent Test**: From a connected state, click Disconnect → the adapter/address is
gone and VPN addresses no longer respond. Separately, close the app while connected →
confirm the adapter is removed.

**Acceptance Scenarios**:

1. **Given** a connected device, **When** the user clicks Disconnect, **Then** the virtual adapter and VPN address are removed and VPN addresses no longer respond.
2. **Given** a connected device, **When** the user closes the app, **Then** the tunnel is torn down and no virtual adapter is left behind.
3. **Given** a disconnected device, **When** the user clicks Disconnect again, **Then** nothing happens (no error).

---

### User Story 3 — 连接状态清晰、提权可控、失败可恢复 (Priority: P2)

The connection state (connected / connecting / disconnected) is visible at all times.
Because creating the virtual adapter needs administrator privilege, the app obtains
elevation when connecting and, if elevation is denied, stays disconnected with a clear
explanation. Connection failures are shown in plain language and leave the device cleanly
disconnected and retryable. An idle connection stays up on its own.

**Why this priority**: This is a security-relevant action that touches OS networking; the
user must always know whether they are connected, understand the admin prompt, and recover
cleanly from failures. Hardens US1/US2.

**Independent Test**: Trigger each failure (deny elevation, server unreachable) → a clear
message and a clean disconnected state; leave a connection idle → it stays connected; check
the status indicator always matches the real tunnel state.

**Acceptance Scenarios**:

1. **Given** the user clicks Connect, **When** administrator elevation is required, **Then** the app requests it, and if the user denies it the device stays disconnected with a clear message.
2. **Given** a connection attempt, **When** the server is unreachable, **Then** the app shows a specific message and remains cleanly disconnected and retryable.
3. **Given** a connected device left idle, **When** time passes with no activity, **Then** the connection stays up (keepalive) and the device continues to show online.
4. **Given** any state, **When** the user looks at the home area, **Then** the current connection state is clearly shown and matches the actual tunnel state.

---

### Edge Cases

- **Elevation denied**: the device stays disconnected with a clear message; the user can retry.
- **Server unreachable at connect**: connecting fails with a specific message; the device returns to disconnected.
- **Network drops mid-session**: the connection re-establishes on its own when connectivity returns (keepalive); the status reflects the transient loss.
- **Leftover adapter from a previous crash**: it is cleaned up (or reused) on the next Connect, not duplicated.
- **Reaching another device when not in a shared zone**: that device is not reachable — zone membership (feature 005) governs reachability; this is expected, not a tunnel failure.
- **Disconnect while already disconnected**: a no-op.
- **App crash while connected**: the in-process tunnel ends and the adapter is removed; any residue is cleaned on the next launch.
- **Local key or recorded server details missing** (should not happen after setup): the user is guided back to setup rather than shown a tunnel error.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: From the home area of a set-up device, the user MUST be able to start (connect) the tunnel with a single action.
- **FR-002**: From the home area, the user MUST be able to stop (disconnect) the tunnel with a single action.
- **FR-003**: When connected, the device's assigned VPN address MUST become active on the machine via a virtual network adapter.
- **FR-004**: When connected, only VPN-range traffic MUST be routed through the tunnel; the user's other internet traffic MUST be unaffected (split tunnel).
- **FR-005**: When connected, the device MUST be able to reach the server's VPN address, and — when it shares a zone with another device — that device's VPN address.
- **FR-006**: The tunnel MUST stay alive while idle (periodic keepalive) so an unused-but-connected device remains reachable and continues to show as online.
- **FR-007**: Creating the virtual adapter requires administrator privilege; the app MUST obtain elevation when needed, and if elevation is denied MUST remain disconnected with a clear message.
- **FR-008**: The connection state (connected / connecting / disconnected) MUST be visible at all times from the home area and MUST match the actual tunnel state.
- **FR-009**: When disconnected, the virtual adapter and VPN address MUST be removed and no VPN traffic MUST flow.
- **FR-010**: The tunnel MUST be assembled from the locally stored device key and the recorded server details (server identity, endpoint, network range, device address); no secret leaves the machine and no credentials are re-entered to connect.
- **FR-011**: Connection failures (server unreachable, elevation denied, adapter creation failed) MUST be shown as human-readable, actionable messages, leaving the device cleanly disconnected and retryable.
- **FR-012**: Closing the app MUST tear the tunnel down cleanly, leaving no orphaned virtual adapter.
- **FR-013**: Only one tunnel (this machine's single device) MUST be active at a time.

### Key Entities

- **Tunnel connection**: The active VPN connection, with a state of disconnected, connecting, or connected. Brought up by Connect and torn down by Disconnect or app exit.
- **Virtual network adapter**: The OS-level adapter that carries the device's VPN address while connected and is removed when disconnected.
- **Connection profile**: The assembled settings used to bring the tunnel up — the device's address and key, the server's identity and endpoint, the routed VPN range, and the keepalive interval — derived from the locally stored key and the setup record. Holds no re-entered input.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After clicking Connect, the device's VPN address is active and the server's VPN address is reachable within 5 seconds on a low-latency network.
- **SC-002**: While connected, the user's normal (non-VPN) internet continues to work — only VPN-range traffic uses the tunnel.
- **SC-003**: An idle connected device stays connected and shows online for at least 10 minutes with no manual action.
- **SC-004**: After clicking Disconnect, the virtual adapter and VPN address are gone and VPN addresses are unreachable within a couple of seconds.
- **SC-005**: Two devices in the same zone, both connected, can reach each other's VPN address 100% of the time.
- **SC-006**: If elevation is denied or the server is unreachable, the app shows a specific message and remains cleanly disconnected and retryable in 100% of these cases.
- **SC-007**: Closing the app removes the virtual adapter in 100% of runs (no orphan left behind).
- **SC-008**: The connection state shown in the home area always matches the actual tunnel state.

---

## Assumptions

- Builds on feature 009 (a registered device with its private key in the OS secure store
  and a local record holding the server's identity, endpoint, network range, and the
  device's assigned address) and on features 003/004 (the server's VPN interface and this
  device's server-side peer). Connecting assembles the tunnel from those; it adds no new
  server work.
- Windows desktop is the surface. The user-space VPN engine and the virtual-adapter driver
  are packaged with the app; creating the adapter requires administrator privilege
  (elevation prompt).
- Split tunnel: only the VPN range (`100.127.0.0/16`) is routed through the tunnel; the
  keepalive interval is 25 seconds (DESIGN §6), keeping an idle connection alive and
  visible as online.
- Reaching another device requires shared-zone membership (feature 005); this feature
  delivers the tunnel, not zone membership. The "ping another node" outcome therefore
  assumes both devices are in the same zone.
- The tunnel runs while the app runs (an in-process engine); closing the app ends the
  tunnel. A persistent background/service mode and auto-connect-on-launch are out of scope
  for this version (the user connects manually).
- The full management panel is feature 011; here the home area gains only a Connect /
  Disconnect control and a connection-status indicator.
- Testing: the connection-profile assembly (from the stored key + setup record) is
  validated automatically and host-agnostically; a real device↔server handshake and
  reachability to the server's VPN address can be validated where a user-space tunnel can
  be brought up against a real server; the Windows virtual-adapter, driver, and elevation
  path is validated manually on Windows (a documented, unavoidable exception for an
  OS-driver-and-UAC feature, consistent with feature 009).
