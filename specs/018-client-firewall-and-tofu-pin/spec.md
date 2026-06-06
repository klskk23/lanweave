# Feature Specification: Client Firewall Control and TOFU Certificate Pinning

**Feature Branch**: `018-client-firewall-and-tofu-pin`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "017 添加客户端控制 防火墙规则允许VPN网段流量 功能，默认关，然后就是更改上一个功能中的允许不安全连接的功能，改成持久化模式而不是每次开关客户端都要询问。"（grill 阶段定案：不安全连接改为 TOFU 证书钉扎——首次信任后持久，取代 017 的会话级 insecure opt-in；防火墙为入站放行 VPN 网段的开关，默认关，规则只在「开关 ON ∧ 已连接」时存在；两件事共用一次客户端状态 schema 迁移。）

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Trust a self-signed server once, not every time (Priority: P1)

A user runs the desktop client against a server whose TLS certificate the client cannot verify
through the system's trusted authorities — typically a self-signed certificate or an internal
lab/CA setup. The first time they connect, the client tells them plainly that the certificate
cannot be verified, shows a fingerprint that identifies it, and asks whether they want to trust
*this* certificate on *this* device. If they agree, the client remembers that decision: every
later connection to the same server — including after restarting the app — proceeds quietly
without asking again, while the system still rejects any *other* unverifiable certificate. If the
server's certificate later changes to one the client doesn't recognize, the user gets a clearly
heavier "the certificate changed" warning before anything is sent, and must deliberately accept
the new one.

**Why this priority**: Feature 017 made the certificate opt-in reactive but session-only — every
restart re-prompted, which is the opposite of what long-lived self-signed/internal deployments
need. Trust-on-first-use (TOFU) replaces that with a decision the user makes once per server,
turning a recurring annoyance into a one-time, auditable act of trust while keeping the system
from silently accepting a *different* certificate. This is the core change the user asked for and
the reason the client-state schema is revised.

**Independent Test**: Point the client at a self-signed server; on first connect, confirm the
trust prompt appears (naming the server and showing a fingerprint), accept it, and connect
successfully. Restart the client and connect again — confirm there is **no** prompt and the
connection succeeds. Then present a different certificate for the same server and confirm a
distinctly heavier "certificate changed" warning blocks the connection until explicitly accepted.

**Acceptance Scenarios**:

1. **Given** the client is configured for a server whose certificate fails system verification and
   no trust has been recorded for it, **When** the user attempts to connect, **Then** the client
   shows a first-trust prompt that identifies the server and displays the certificate's fingerprint
   and does not connect until the user decides.
2. **Given** the first-trust prompt is shown, **When** the user accepts it, **Then** the connection
   proceeds and the client records trust for that server's certificate so the prompt does not
   reappear on later connections, including after an app restart.
3. **Given** a server whose certificate has been trusted, **When** the user restarts the client and
   connects again, **Then** the connection succeeds silently with no certificate prompt.
4. **Given** the first-trust prompt is shown, **When** the user declines it, **Then** the client
   does not connect and returns the user to a point where they can review or change the server
   address.
5. **Given** a server previously trusted via its certificate, **When** that server later presents a
   *different* certificate that also fails system verification, **Then** the client shows a
   visibly heavier "certificate changed" warning, does not exchange data, and connects only if the
   user explicitly accepts — after which the newly accepted certificate replaces the remembered one.
6. **Given** the user has changed the configured server address to a different server, **When** they
   connect, **Then** trust is evaluated fresh for the new server (a new first-trust prompt if it,
   too, is unverifiable) rather than silently reusing the previous server's trust.
7. **Given** a server is connected and trusted only via a remembered self-signed certificate (not a
   system authority), **When** the user is on the main panel, **Then** a neutral, persistent
   indicator states the certificate is self-signed but trusted on this device.

---

### User Story 2 - Allow VPN peers to reach this device, on purpose (Priority: P2)

A user wants peers inside their VPN subnet to be able to reach services running on this machine
(for example, a shared tool or a game server) while connected. By default the device stays closed
to unsolicited inbound traffic. The user flips a clearly labelled control to allow inbound traffic
from the VPN subnet; a warning next to it spells out that turning it on lets anyone in the same VPN
network reach all local services. The allowance is active only while the user both has it enabled
and is connected — disconnecting, turning it back off, logging out, or quitting the app all close
the device again. The choice itself is remembered across restarts so the user does not have to set
it every time.

**Why this priority**: This is net-new capability (the client never touched the host firewall
before) and is opt-in/off-by-default, so it is valuable but secondary to fixing the recurring
certificate prompt. Tying the open window to "enabled AND connected" keeps exposure minimal and
predictable, and persisting the preference avoids re-toggling on every launch.

**Independent Test**: With the toggle off and connected, confirm a VPN peer cannot reach a local
service. Turn the toggle on while connected and confirm the peer can now reach it. Turn the toggle
off (still connected) and confirm access is blocked again. Re-enable, then disconnect, and confirm
access is blocked. Restart the client and confirm the toggle's last state is remembered.

**Acceptance Scenarios**:

1. **Given** a freshly upgraded client with no prior preference, **When** the user views the main
   panel, **Then** the inbound-allow control is present and **off** by default, with an adjacent
   warning that enabling it exposes all local services to peers in the VPN subnet.
2. **Given** the client is connected and the toggle is off, **When** the user switches the toggle
   on, **Then** inbound traffic from the VPN subnet is allowed to reach this device immediately,
   without a separate confirmation dialog.
3. **Given** the client is connected with the toggle on, **When** the user switches the toggle off,
   **Then** inbound traffic from the VPN subnet is blocked again immediately.
4. **Given** the toggle is on and inbound is allowed, **When** the tunnel disconnects (manually,
   by logout, or by app exit), **Then** the inbound allowance is removed and the device is closed
   to unsolicited inbound again.
5. **Given** the toggle is on but the client is **not** connected, **When** the user inspects the
   device, **Then** no inbound allowance is in effect (the preference is recorded but only takes
   effect once connected).
6. **Given** the toggle was on and the app terminated abnormally while an inbound allowance was in
   effect, **When** the client is started again, **Then** any leftover allowance from the previous
   run is cleared on startup and the device's inbound state matches the current connection and
   toggle state, with no duplicate or orphaned allowance after repeated connect/disconnect cycles.
7. **Given** the user has set the toggle to a particular state, **When** they restart the client,
   **Then** the toggle returns to that remembered state.

---

### Edge Cases

- **Decline first-trust**: declining the trust prompt leaves the client disconnected and able to
  re-enter or change the server address; it does not silently proceed.
- **Server gains a real certificate**: if a previously self-signed server later presents a
  certificate that passes system verification, the connection succeeds via the system-authority
  path even though a remembered self-signed fingerprint also exists; the remembered pin simply
  becomes dormant.
- **Benign certificate rotation looks like a change**: renewing a self-signed certificate with a
  new key produces a different fingerprint and therefore triggers the heavier "certificate changed"
  warning even though the operator intended it. This friction is deliberate; the user re-accepts to
  update the remembered certificate.
- **Logout resets both new preferences**: logging out clears all local client state, so the
  remembered certificate trust and the firewall preference are both discarded along with sign-in
  and device identity; re-onboarding to the same server starts from defaults (no pin, firewall
  off).
- **Toggle on while disconnected, then connect**: the allowance is applied at connect time, not
  at toggle time.
- **Rapid reconnects**: repeatedly connecting and disconnecting with the toggle on must not
  accumulate duplicate inbound allowances.
- **Bypass flag still available**: the advanced command-line option that skips all certificate
  verification continues to work and continues to show the existing severe "certificate not
  verified" warning; it is distinct from TOFU trust and is never offered through the UI.
- **Non-Windows platforms**: firewall enforcement applies only on Windows; elsewhere the toggle's
  enforcement is a no-op while the recorded preference is still preserved.

## Requirements *(mandatory)*

### Functional Requirements

#### Certificate trust (TOFU) — User Story 1

- **FR-001**: When the user attempts to connect to a server whose certificate does not pass system
  verification and for which no trust is recorded, the client MUST present a first-trust prompt
  that identifies the server and displays the certificate's fingerprint, and MUST NOT connect
  until the user decides.
- **FR-002**: When the user accepts the first-trust prompt, the client MUST persist the server's
  leaf-certificate SHA-256 fingerprint, associated with the configured server, so that subsequent
  connections to the same server proceed without prompting, including after an application restart.
- **FR-003**: The client MUST treat a certificate as acceptable if its leaf-certificate fingerprint
  matches the recorded trust for the configured server **or** the certificate passes system
  verification; any other certificate MUST be rejected by default.
- **FR-004**: Recorded trust MUST be scoped to the configured server; changing the configured
  server address MUST cause verification to be evaluated fresh for the new server rather than
  reusing a previous server's trust.
- **FR-005**: When a server with recorded trust presents a different certificate that also fails
  system verification, the client MUST present a "certificate changed" warning that is visibly more
  severe than the first-trust prompt, MUST NOT exchange data, and MUST NOT connect unless the user
  explicitly accepts.
- **FR-006**: Accepting a "certificate changed" warning MUST replace the recorded trust with the
  newly accepted certificate's fingerprint.
- **FR-007**: If the user declines the first-trust prompt or the "certificate changed" warning, the
  client MUST NOT connect and MUST return the user to a state where they can review or change the
  server address.
- **FR-008**: When connected to a server trusted only via a recorded self-signed certificate (not a
  system authority), the client MUST show a neutral, persistent indicator stating the certificate
  is self-signed but trusted on this device.
- **FR-009**: The client MUST retain the existing advanced command-line option that bypasses all
  certificate verification; while it is active the client MUST show the existing severe
  "certificate not verified" warning, and this bypass MUST NOT be offered through the UI.
- **FR-010**: This feature MUST replace feature 017's session-level insecure opt-in (a prompt on
  every untrusted connection, never remembered): the client MUST NOT offer a "continue insecurely
  for this session only, not remembered" path through the UI. The corresponding 017 requirements
  are superseded.

#### Firewall control — User Story 2

- **FR-011**: The client MUST provide a user-controllable setting that allows inbound network
  traffic from the VPN subnet (100.127.0.0/16) to reach this device, and this setting MUST default
  to OFF.
- **FR-012**: The client MUST persist the inbound-allow setting across restarts.
- **FR-013**: Whenever the inbound-allow setting is ON and the tunnel is connected, the client MUST
  ensure an inbound allowance for the VPN subnet is in effect for this device; whenever either
  condition is false, the client MUST ensure no such allowance is in effect.
- **FR-014**: The client MUST establish the inbound allowance upon a successful connection while the
  setting is ON, and immediately upon the user switching the setting ON while already connected.
- **FR-015**: The client MUST remove the inbound allowance upon any of: tunnel disconnect, the
  setting being switched OFF, logout, or application exit.
- **FR-016**: The inbound allowance MUST be identifiable as belonging to lanweave and MUST be
  applied idempotently so that repeated connect/disconnect or toggle cycles never accumulate
  duplicate allowances.
- **FR-017**: On startup, the client MUST clear any leftover lanweave inbound allowance from a
  previous unclean shutdown before reconciling to the current connection-and-setting state.
- **FR-018**: The client MUST display, adjacent to the inbound-allow setting, a persistent inline
  warning that enabling it lets peers within the same VPN subnet reach all local services on this
  device; switching the setting ON or OFF MUST take effect without a separate confirmation dialog.
- **FR-019**: Inbound-allow enforcement applies only on the Windows platform; on other platforms the
  enforcement MUST be a no-op while the recorded preference is still retained.

#### Shared state

- **FR-020**: The client MUST carry forward existing locally stored client state from the prior
  schema version, adding the recorded-certificate-trust and inbound-allow fields with safe defaults
  ("no trust recorded" and "inbound allow off") so that existing users are not required to
  re-onboard and their security posture is unchanged until they act.

### Key Entities *(include if feature involves data)*

- **Client State Record**: the existing local, non-secret record describing this device's
  onboarding (server address, device name, address, server identity, network). It gains two
  fields: a recorded certificate-trust fingerprint for the configured server, and the inbound-allow
  preference (on/off). Its schema version advances by one, and older records load with the two new
  fields defaulted.
- **Certificate Trust Pin**: a remembered SHA-256 fingerprint of a server's leaf certificate,
  captured the first time the user trusts it, used thereafter to recognize the same certificate.
- **Inbound Allowance**: a named, lanweave-owned host inbound rule permitting traffic from the VPN
  subnet to this device, present only while the inbound-allow setting is ON and the tunnel is
  connected.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user connecting to a self-signed server is asked to trust it at most once per
  server; across any number of subsequent app restarts the connection succeeds with zero further
  certificate prompts.
- **SC-002**: With the inbound-allow setting on and the client connected, a peer in the VPN subnet
  can reach a local service that was unreachable with the setting off; turning the setting off, or
  disconnecting, blocks that access again.
- **SC-003**: Across any sequence of connect, disconnect, toggle-on, toggle-off, and abrupt
  termination-then-restart, the device has an inbound allowance in effect exactly when (and only
  when) the setting is on and the client is connected, with no duplicate or orphaned allowance
  remaining.
- **SC-004**: When a trusted server's certificate changes to an unrecognized one, 100% of
  connection attempts surface a distinct, heavier warning before any data is exchanged, and
  declining blocks the connection.
- **SC-005**: Users upgrading from the prior client version keep their onboarding (no
  re-registration), with the inbound-allow setting defaulting off and no certificate trust recorded
  until they next choose to trust one.
- **SC-006**: For users who change nothing, the default posture is unchanged: the device remains
  closed to unsolicited inbound traffic and certificates are still verified (recorded-trust or
  system authority).

## Assumptions

- This feature builds on feature 017 (client onboarding, main panel, local state, certificate
  handling) and feature 010 (tunnel connect/disconnect lifecycle), and reuses their structures.
- It supersedes feature 017's session-level insecure opt-in requirements; the frozen design
  (DESIGN.md, including the reactive-opt-in clauses and the accepted-risks register) will be
  amended in the same change set to record the TOFU posture and the client-managed inbound rule.
- The two new fields (certificate trust and inbound-allow preference) share a single client-state
  schema migration (version 1 → 2); they are not split across two migrations.
- The VPN subnet is fixed at 100.127.0.0/16, per the project design.
- Certificate identity is pinned by the leaf certificate's SHA-256 fingerprint captured at first
  trust, not by the full chain.
- Inbound-allow enforcement targets the Windows host firewall; non-Windows platforms (used for
  headless testing) treat enforcement as a no-op.
- The advanced command-line bypass-verification option is retained as an escape hatch and is never
  surfaced in the UI.
- Logout (from feature 017) clears all local client state; consequently the recorded certificate
  trust and the inbound-allow preference are discarded on logout, and re-onboarding starts from
  defaults.
