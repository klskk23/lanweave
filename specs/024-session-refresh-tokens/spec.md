# Feature Specification: Session Refresh Tokens

**Feature Branch**: `024-session-refresh-tokens`

**Created**: 2026-06-07

**Status**: Draft

**Input**: User description: "024 session-refresh-tokens"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Stay signed in without re-entering the password (Priority: P1)

A user signs in to the client once. They keep using the app over hours and days. When their short-lived session silently expires in the background, the client renews it automatically without ever showing a password prompt. The user only notices that the app keeps working.

**Why this priority**: This is the core value of the slice. Today the short session expires about every two hours and the user is forced to retype their password — the single biggest friction point in daily use. Fixing this alone delivers a complete, demonstrable improvement.

**Independent Test**: Sign in, let the short session expire (or force expiry), then perform any action that needs the server. The action succeeds and no password prompt appears.

**Acceptance Scenarios**:

1. **Given** a signed-in user whose short session has expired, **When** they perform an action requiring the server, **Then** the action completes and no password prompt is shown.
2. **Given** a freshly signed-in user, **When** login succeeds, **Then** the client holds both a short-lived session credential and a long-lived renewal credential.
3. **Given** a user who closes and reopens the client within the renewal window, **When** the app starts, **Then** it resumes the session without asking for a password.

---

### User Story 2 - Revoke a device's session (Priority: P2)

An operator needs to cut off a specific device or an entire user. Logging out from a device invalidates that device's long-lived renewal credential on the server. Deleting a user invalidates every renewal credential that user held. After revocation, the affected device can no longer renew and is returned to password login.

**Why this priority**: Long-lived renewal credentials are useless for security unless they can be revoked. This makes the feature safe to ship and is the foundation the logout-hardening slice (025) builds on.

**Independent Test**: Sign in on a device, revoke that device's renewal credential on the server, then let the device's short session expire. The next server action fails to renew and the device falls back to password login.

**Acceptance Scenarios**:

1. **Given** a device with a valid renewal credential, **When** it logs out, **Then** the server marks that credential revoked and a later renewal attempt with it is rejected.
2. **Given** a logout request carrying an unknown or already-revoked credential, **When** the server processes it, **Then** it reports success without error (idempotent).
3. **Given** a user with one or more active device sessions, **When** the user is deleted, **Then** all of that user's renewal credentials are invalidated.

---

### User Story 3 - Eventually require re-login after long inactivity (Priority: P3)

A renewal credential does not last forever. It expires after a fixed period of inactivity; each successful renewal extends the window. A device left unused past that window must sign in with a password again.

**Why this priority**: Bounds the exposure of a long-lived credential without hurting active users. Lower priority because active users effectively never hit it.

**Independent Test**: Issue a renewal credential, advance time past the inactivity window without using it, then attempt renewal. Renewal is rejected and the device falls back to password login.

**Acceptance Scenarios**:

1. **Given** a renewal credential unused for longer than the inactivity window, **When** the client attempts to renew, **Then** renewal is rejected and the user is prompted to sign in.
2. **Given** a renewal credential used regularly, **When** each renewal succeeds, **Then** its expiry slides forward and the user is never forced to re-login.

---

### Edge Cases

- **Both credentials expired**: short session expired and renewal credential expired/revoked → client falls back to password login.
- **Concurrent expiry**: several queued requests hit "session expired" at once → renewal may be attempted more than once; because the renewal credential is reusable until it expires or is revoked, concurrent renewals are safe and the requests proceed.
- **Server restart with a new signing key**: existing short sessions become invalid, but renewal credentials are validated against the server's database and remain valid. The previous "restart invalidates everyone" property no longer holds; invalidating all sessions now requires deleting users or clearing the stored renewal credentials. (Documented accepted risk.)
- **Revocation mid-session**: a renewal credential revoked while a device is online → the device's next renewal fails and it falls back to password login.
- **Registration**: registering a new account does not by itself create a session; the client signs in afterward to obtain credentials.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: On successful login, the system MUST issue a long-lived renewal credential alongside the existing short-lived session credential.
- **FR-002**: The system MUST let a client obtain a new short-lived session credential by presenting a valid renewal credential, without requiring the user's password.
- **FR-003**: The short-lived session credential's lifetime MUST remain short (2 hours) and unchanged by this feature.
- **FR-004**: Renewal credentials MUST be opaque and high-entropy; the server MUST store them only in a non-reversible (hashed) form. The plaintext value is returned to the client once at issuance and never persisted server-side in plaintext.
- **FR-005**: A renewal credential MUST expire after 30 days of inactivity; each successful renewal MUST slide the expiry forward by 30 days.
- **FR-006**: The client MUST renew lazily — only after an authenticated request is rejected as expired — and then retry the original request once. The client MUST NOT run a proactive renewal timer.
- **FR-007**: When renewal fails because the renewal credential is expired, revoked, or unknown, the client MUST clear the local session and fall back to password login.
- **FR-008**: The system MUST support revoking an individual renewal credential (logout), and this operation MUST be idempotent — revoking an unknown or already-revoked credential still reports success.
- **FR-009**: Deleting a user MUST invalidate all of that user's renewal credentials.
- **FR-010**: The client MUST persist the renewal credential in the operating system's protected credential store, alongside the session credential, and MUST remove it on logout and on session cleanup.
- **FR-011**: The registration flow MUST remain unchanged — registration does not issue a session; the client logs in afterward to obtain credentials.
- **FR-012**: The renewal and logout operations MUST authenticate using the renewal credential itself, not the (possibly expired) session credential.

### Key Entities

- **Renewal credential (refresh token)**: A long-lived, per-user credential that lets a device obtain fresh short-lived session credentials without a password. Attributes: owning user, creation time, expiry (slides on use), and revocation state. Stored on the server only as a non-reversible hash; held by the client in the OS-protected credential store.
- **Session credential (access token)**: The existing short-lived (2-hour) credential authorizing API calls. Unchanged by this feature except that it can now be obtained via renewal as well as via login.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with the client in regular use stays signed in across session expiry without ever seeing a password prompt for at least 30 days.
- **SC-002**: After a device's renewal credential is revoked, that device can no longer obtain a new session and is returned to password login on its next session expiry.
- **SC-003**: 100% of logout/revocation requests carrying an unknown or already-revoked credential complete successfully with no server error.
- **SC-004**: Deleting a user leaves zero usable renewal credentials for that user — every device session is invalidated.
- **SC-005**: A renewal credential unused for more than 30 days no longer grants a new session.

## Assumptions

- Builds on the existing login, session-credential issuance, and authentication enforcement (slice 002), and on the client's protected credential store and server REST client (slice 009).
- The renewal credential is persisted at the same point in the flow where the session credential is persisted today (the fix from slice 019), including the post-onboarding provisioning path.
- No renewal-credential rotation or replay detection in this slice: a renewal credential is reusable until it expires or is explicitly revoked.
- No proactive/scheduled renewal — renewal is lazy and on-demand only.
- There is no self-service password-change feature, so "revoke renewal credentials on password change" is out of scope.
- No admin "log out everyone / force global re-login" operation in this slice; deleting a user is sufficient to invalidate that user's sessions.
- The server's database is the single source of truth; expiry and revocation are evaluated against it on every renewal.
- The inactivity window is fixed at 30 days for this slice (not user- or operator-configurable).
- Accepted risk: each device now holds one additional long-lived credential locally (protected by the OS credential store), and a server restart no longer invalidates all sessions.
