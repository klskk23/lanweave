# Feature Specification: Client Logout and TLS Opt-In

**Feature Branch**: `017-client-logout-and-tls-optin`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "改善客户端体验：(1) 客户端退出登录 —— 退出后服务端 URL 可重新填写；(2) insecure-tls 作为证书错误时的可交互 opt-in。（原描述含"删除 node"，评估后判定与退出登录对本机的处理重叠、独立价值有限，已剔除。）"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Log out and switch server or account (Priority: P1)

A user who has already set up the desktop client against one server (and account) wants to
stop using it there and connect somewhere else — a different server, or a different account on
the same server. They open the client, choose "Log out", confirm a clear warning, and the
client disconnects, removes this device's registration from that server, clears the locally
stored sign-in and device identity for it, and returns them to the very first setup step where
they can type a new server address and sign in afresh.

**Why this priority**: Today onboarding is one-way — once the client is set up, the only way to
change server or account is to manually delete local files and stored credentials. Logout is
the core of "improve client experience" and the foundation the second story reuses (both return
the user to the first setup step). Without it nothing else in this feature is reachable from a
running client.

**Independent Test**: Set the client up against a server and connect; use Log out; confirm the
connection drops, this device's registration disappears on the server, locally stored sign-in
and device identity are gone, and the client shows the server-URL entry step from which a fresh
setup completes and connects.

**Acceptance Scenarios**:

1. **Given** the client is set up and connected, **When** the user confirms Log out, **Then**
   the connection is torn down, this device's registration is removed on the current server, the
   locally stored session and device identity for that server are cleared, and the client shows
   the first setup step (server-URL entry).
2. **Given** the user has just logged out, **When** they enter a server URL (the same or a
   different one) and complete setup, **Then** a fresh device registration succeeds and the
   client connects normally.
3. **Given** the user selects Log out, **When** the confirmation prompt appears, **Then** it
   states plainly that logging out will disconnect, remove this device's node on that server,
   and require re-entering the server address; the logout proceeds only on explicit confirmation.
4. **Given** the client is set up but not currently connected, **When** the user logs out,
   **Then** logout still removes the registration and returns to setup without reporting a
   "nothing to disconnect" error.

---

### User Story 2 - Proceed past an unverifiable certificate, deliberately (Priority: P2)

A user points the client at a server whose TLS certificate the client cannot verify (a
self-signed certificate or an internal certificate authority — common in testing and lab
setups). Instead of being stuck with an opaque failure, the user is shown a clear prompt that
explains the certificate could not be verified and offers to continue anyway (insecurely). If
they accept, the connection proceeds for the current session only, and the client keeps a
persistent visible warning that the certificate is not verified.

**Why this priority**: Without this, a GUI user who hits a certificate error has no way forward
except an undocumented command-line flag. It is valuable but secondary to logout, and is
deliberately constrained (no casual toggle) so users cannot blindly disable verification.

**Independent Test**: Point the client at a self-signed server; confirm a
"certificate-could-not-be-verified" prompt appears; accepting it connects and shows a persistent
"certificate not verified" warning; declining leaves the connection refused; restarting the
client verifies certificates again by default.

**Acceptance Scenarios**:

1. **Given** the server's certificate cannot be verified, **When** the user attempts to connect
   (during setup or from the running client), **Then** the client presents a prompt explaining
   the problem and offering to continue insecurely — it neither fails opaquely nor proceeds
   silently.
2. **Given** the insecure prompt is shown, **When** the user declines, **Then** no connection is
   established and no insecure connection occurs.
3. **Given** the user accepts the insecure prompt, **When** the session continues, **Then** a
   persistent, visible indicator shows that the certificate is not verified for as long as that
   state lasts.
4. **Given** the user accepted an insecure connection earlier, **When** the client is restarted,
   **Then** certificate verification is enforced again by default — the prior acceptance is not
   remembered.

---

### Edge Cases

- **Logout while disconnected**: The user logs out when no connection is active. Logout still
  removes the registration and clears local state and returns to setup; it does not error on the
  absent connection.
- **Logout when the server is unreachable**: The server-side removal cannot complete (server
  down, no network). The client still completes the local logout (clears local session/identity
  and returns to setup) and informs the user that the remote registration may linger until it is
  cleaned up. (See Assumptions — there is no remote cleanup path in this feature.)
- **Re-setup after logout does not recover the old registration**: The removed registration is
  gone; completing setup again creates a brand-new device identity, never the old one.
- **Insecure entered via command line vs. prompt**: When the client is launched with the
  command-line skip-verification option, verification is bypassed from the start and the
  interactive prompt does not appear; the persistent "not verified" indicator is still shown.
- **A publicly trusted certificate never triggers the prompt**: The insecure prompt appears only
  on an actual verification failure, so a server with a valid, trusted certificate connects
  normally with no prompt and no warning indicator.

## Requirements *(mandatory)*

### Functional Requirements

**Logout**

- **FR-001**: The running client MUST provide a Log out action in its main view, placed away from
  primary controls so it is not triggered by accident.
- **FR-002**: Selecting Log out MUST require an explicit confirmation that states logging out
  will disconnect, remove this device's registration on the current server, and require
  re-entering the server address; logout proceeds only on confirmation.
- **FR-003**: On confirmed logout, the client MUST tear down any active connection (the tunnel).
- **FR-004**: On confirmed logout, the client MUST remove this device's own registration from the
  server it is connected to.
- **FR-005**: On confirmed logout, the client MUST clear the locally stored session credential,
  the locally stored device identity, and the local setup state for that server, leaving no
  residual authentication or device identity.
- **FR-006**: After logout, the client MUST return to the first setup step (server-URL entry) so
  the user can connect to the same or a different server/account.
- **FR-007**: After logout, completing setup again MUST produce a fresh device registration (a
  new device identity), not reuse the removed one.
- **FR-008**: If the server-side registration removal cannot be completed, the client MUST still
  complete the local logout and MUST inform the user that the remote registration may remain
  until it is cleaned up.

**Interactive insecure-TLS opt-in**

- **FR-009**: When the client cannot verify the server's TLS certificate while attempting to
  connect (during setup or from the running client), it MUST present the user an explicit prompt
  that explains the certificate could not be verified and offers to continue insecurely — rather
  than failing opaquely or proceeding silently.
- **FR-010**: The insecure option MUST NOT be presented as an always-available toggle or
  checkbox; it MUST appear only in response to an actual certificate-verification failure.
- **FR-011**: If the user declines the insecure prompt, the client MUST NOT establish the
  connection.
- **FR-012**: The user's acceptance of an insecure connection MUST apply only to the current
  running session and MUST NOT be persisted; on next start the client MUST verify certificates by
  default.
- **FR-013**: Whenever the client is operating with certificate verification bypassed, it MUST
  show a persistent, visible indication that the certificate is not verified.
- **FR-014**: The existing command-line option to skip certificate verification MUST be retained;
  when used, verification is skipped from the start and the interactive prompt is not required,
  but the persistent "not verified" indication MUST still be shown.

### Key Entities *(include if feature involves data)*

- **Device registration (node)**: This client's identity and membership on a server, created at
  setup and removed at logout. Removing it is what frees the device's address and detaches it
  from the server.
- **Local session & device identity**: The locally stored proof of authentication and the
  device's own identity that let the client operate without repeating setup; both are cleared at
  logout.
- **Local setup state (server profile)**: The stored server address and connection parameters
  that decide whether the client opens to setup or to the main view; cleared at logout so the
  user is returned to server-URL entry.
- **Certificate-verification state (per session)**: Whether the current running session is
  verifying or bypassing TLS certificate verification; lives only for the session and is never
  persisted.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can go from a fully set-up, connected client to re-entering a server URL via
  a single confirmed Log out action, with no manual file or credential editing.
- **SC-002**: After logout against a reachable server, the device's registration no longer exists
  on that server.
- **SC-003**: After logout, no locally stored authentication or device identity for that server
  remains.
- **SC-004**: After logout, a returning user can complete fresh setup against the same or a
  different server and connect successfully.
- **SC-005**: A user facing an unverifiable certificate can, by an explicit choice, either
  proceed insecurely or decline, with zero cases of a silent insecure connection.
- **SC-006**: While certificate verification is bypassed, the "not verified" indicator is visible
  100% of the time.
- **SC-007**: An insecure acceptance never survives a restart — the next start verifies
  certificates by default in 100% of cases.

## Assumptions

- Logout targets only the current device's own registration; it does not remove other devices'
  registrations. This feature adds no remote device management.
- A device that cannot run logout (lost, decommissioned, or broken) leaves a registration that
  this feature provides no way to remove remotely. Cleaning up such a registration is an accepted
  limitation and out of scope.
- Logout deliberately removes the device registration rather than leaving it in place. Leaving it
  would accumulate registrations that nothing can remove, because this feature intentionally adds
  no separate "delete a node" path.
- The interactive insecure prompt and the existing command-line skip-verification flag are two
  entry points to the same one-session bypass; using the command-line flag implies consent and
  suppresses the prompt, but the "not verified" indicator still applies.
- A certificate-verification failure here means the client cannot establish trust in the server's
  certificate (self-signed, internal CA, or hostname mismatch); a server presenting a publicly
  trusted, valid certificate never triggers the prompt.
- "Insecure" affects only TLS certificate verification; it changes nothing else about
  authentication or the user's account.
- Visual confirmation of the logout confirmation dialog, the insecure prompt, and the "not
  verified" indicator is performed manually on the desktop client, consistent with the project's
  standing manual exception for desktop-GUI verification; the underlying logout sequence and
  certificate-decision logic are covered by automated headless tests.
