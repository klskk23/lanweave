# Feature Specification: Invites and User Auth

**Feature Branch**: `002-invites-and-user-auth`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "invites-and-user-auth"

Scope drawn from ROADMAP.md feature 002 and DESIGN.md §4.1, §7.1, §7.3, §8:
admin-issued one-time invite codes, invite-gated registration, login that issues
a short-lived session token, token verification on protected endpoints, and a
"who am I" endpoint.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 已有用户登录并访问受保护资源 (Priority: P1)

An existing account holder (at minimum the bootstrap admin from feature 001)
submits their username and password and receives a short-lived session token.
They present that token to a protected endpoint and learn who they are
(username, admin status). Presenting no token, or an invalid/expired one, is
refused.

**Why this priority**: This is the authentication backbone. Every later feature's
protected endpoint depends on "log in → get token → token is verified." It is
independently testable using only the admin account that already exists after
feature 001, so it needs nothing else in this feature to demonstrate value.

**Independent Test**: With a freshly bootstrapped server, POST the admin's
username+password to the login endpoint → receive a token. GET the "me" endpoint
with that token → 200 with the admin's identity. GET it with no token and with a
garbage token → 401 each time.

**Acceptance Scenarios**:

1. **Given** a valid account, **When** the user submits the correct username and password to login, **Then** they receive a session token and a success status.
2. **Given** a valid account, **When** the user submits a wrong password, **Then** login is refused with an authentication error and no token is issued.
3. **Given** a username that does not exist, **When** a login is attempted, **Then** it is refused with the same generic authentication error as a wrong password (no account-existence disclosure).
4. **Given** a valid token, **When** the user calls the "me" endpoint, **Then** they receive their own username and admin status.
5. **Given** no token, **When** the "me" endpoint is called, **Then** the response is 401 unauthorized.
6. **Given** a tampered, malformed, or expired token, **When** a protected endpoint is called, **Then** the response is 401 unauthorized.

---

### User Story 2 — 管理员签发与查看邀请码 (Priority: P1)

An administrator generates a one-time invite code to onboard a new person, and
can list previously generated codes to see which are still unused and which have
been consumed (and by whom). Only administrators may do this.

**Why this priority**: Registration is invite-gated (DESIGN §7.1), so the system
cannot onboard anyone until an admin can mint codes. It is independently testable
by logging in as admin (US1) and exercising the invite endpoints.

**Independent Test**: Logged in as admin, request a new invite code → receive a
code string. List invites → the new code appears as unused. Logged in as a
non-admin user (or with no token), the same requests are refused.

**Acceptance Scenarios**:

1. **Given** an authenticated admin, **When** they request a new invite code, **Then** a unique, hard-to-guess code is created and returned.
2. **Given** existing invite codes, **When** an admin lists invites, **Then** each code is shown with its status (unused or used) and, for used codes, which account consumed it and when.
3. **Given** a non-admin authenticated user, **When** they attempt to generate or list invites, **Then** the request is refused with a forbidden (403) error.
4. **Given** an unauthenticated request, **When** it targets an invite endpoint, **Then** it is refused with 401 unauthorized.

---

### User Story 3 — 受邀者凭一次性邀请码注册账号 (Priority: P1)

A new person who has received an invite code registers by choosing a username and
password and supplying the code. On success they have an account and can log in
(via US1). The code is consumed and cannot be reused.

**Why this priority**: This is the growth path of the whole product — without it,
only the bootstrap admin exists. Independently testable end-to-end: admin mints a
code (US2), the invitee registers, then authenticates (US1).

**Independent Test**: Obtain an unused code (US2), submit it with a fresh
username and password to register → success. Then log in as the new user → token
issued, admin status false.

**Acceptance Scenarios**:

1. **Given** a valid unused invite code, **When** a new user registers with an available username and an acceptable password, **Then** the account is created and the code is marked used (recording who used it and when).
2. **Given** a freshly registered account, **When** that user logs in, **Then** they receive a session token and are not an administrator.
3. **Given** an invite code that has already been used, **When** someone attempts to register with it, **Then** registration is refused and no account is created.
4. **Given** an invite code that does not exist, **When** someone attempts to register with it, **Then** registration is refused and no account is created.
5. **Given** a username already taken, **When** registration is attempted with a valid code, **Then** registration is refused with a conflict error and the invite code remains unused.
6. **Given** a registration attempt with no invite code, **When** it is submitted, **Then** it is refused (there is no open, un-gated registration).

---

### User Story 4 — 认证与邀请的安全边界在滥用下成立 (Priority: P2)

Under concurrent or hostile use, the one-time guarantee, the admin gate, and
credential confidentiality all hold: a single code can be redeemed at most once
even under a race, secrets never appear in logs, and brute-force attempts are
bounded by the global rate limiter inherited from feature 001.

**Why this priority**: The happy paths (US1–US3) are the deliverable; this story
hardens them. It is valuable but the product can be demonstrated without the
adversarial guarantees being exhaustively proven, so it sits at P2.

**Independent Test**: Fire many simultaneous registrations with the same code →
exactly one succeeds. Inspect logs after login/registration → no plaintext
password, token, or invite secret present. Exceed the request rate → requests are
throttled (behavior inherited from feature 001).

**Acceptance Scenarios**:

1. **Given** one unused code, **When** many registrations race to use it simultaneously, **Then** exactly one succeeds and the rest are refused.
2. **Given** any login or registration request, **When** it is processed, **Then** no plaintext password, session token, or invite code value appears in any log line.
3. **Given** repeated failed logins from one source, **When** they exceed the configured request rate, **Then** excess requests are throttled (429), consistent with feature 001.
4. **Given** the operator rotates the session signing secret, **When** previously issued tokens are presented, **Then** they are rejected as invalid.

---

### Edge Cases

- **Username case/uniqueness**: registration MUST respect the case-insensitive uniqueness already enforced on accounts (feature 001), so `Alice` and `alice` cannot both exist.
- **Empty/whitespace username or password** at registration → refused with a validation error.
- **Password too short / too weak** → refused with a validation error stating the minimum requirement.
- **Token without an expiry, or with an expiry far in the future** (tampered) → still rejected because the signature won't verify.
- **Admin registering via a code**: not a meaningful flow (they already exist); registration with an existing username is refused, and new registrations are never admin.
- **Invite created by an admin who is later deleted** (feature 008): the code's "created by" reference may dangle; listing MUST still succeed (degrade gracefully rather than error).
- **Clock skew**: a token that is barely expired is treated as expired (no grace window in v1).
- **Very large request body** to register/login → rejected at the boundary (no unbounded reads).

---

## Requirements *(mandatory)*

### Functional Requirements

**Login & session token**

- **FR-001**: The system MUST provide a public login operation that accepts a username and password and, on a correct match, returns a signed session token.
- **FR-002**: Login with an incorrect password OR a non-existent username MUST return the SAME generic authentication failure, disclosing nothing about whether the account exists.
- **FR-003**: The session token MUST be short-lived, expiring after the configured lifetime (default 2 hours, from feature 001 configuration), and MUST carry the account's identity and admin status.
- **FR-004**: The system MUST NOT provide token refresh or server-side revocation in this feature; expiry is the only invalidation path (consistent with DESIGN §7.3). Rotating the signing secret invalidates all outstanding tokens.

**Token verification & identity**

- **FR-005**: The system MUST verify the session token on every protected endpoint, rejecting requests whose token is absent, malformed, expired, or whose signature does not verify, with a 401 response.
- **FR-006**: The system MUST provide a protected "me" operation that returns the authenticated account's username and admin status.
- **FR-007**: Token verification MUST establish the caller's identity (account id, username, admin status) for downstream authorization without requiring an additional database read solely to authenticate.

**Invite issuance & listing (admin)**

- **FR-008**: The system MUST allow an administrator to generate a new invite code that is unique and not feasibly guessable.
- **FR-009**: The system MUST allow an administrator to list invite codes, showing for each its status (unused/used), and for used codes the consuming account and the time of consumption.
- **FR-010**: Invite generation and listing MUST be restricted to administrators; non-admin authenticated callers MUST receive 403, unauthenticated callers MUST receive 401.

**Registration (invite-gated)**

- **FR-011**: The system MUST provide a public registration operation requiring a valid, unused invite code together with a username and password.
- **FR-012**: Registration MUST refuse any request lacking an invite code; there is no open registration path.
- **FR-013**: On successful registration the system MUST create a non-administrator account and atomically mark the invite code as used, recording the consuming account and the consumption time.
- **FR-014**: Registration MUST refuse a code that is already used or does not exist, creating no account.
- **FR-015**: Registration MUST refuse a username that already exists (case-insensitively), returning a conflict error and leaving the invite code unused.
- **FR-016**: Registration MUST validate the username (non-empty, within the account length limit from feature 001) and password (meeting a documented minimum strength), rejecting violations with a clear validation error.
- **FR-017**: Stored passwords MUST be hashed with the same strong scheme used for the bootstrap admin (feature 001); plaintext passwords MUST never be stored.

**Invite lifecycle**

- **FR-018**: Each invite code MUST be redeemable at most once. Concurrent redemption attempts of the same code MUST result in exactly one success.
- **FR-019**: Invite codes MUST NOT expire on a timer in this feature (one-time, no TTL, per DESIGN §7.1).

**Security & cross-cutting**

- **FR-020**: Plaintext passwords, session tokens, and invite code values MUST NOT appear in any log line, error message, or panic output.
- **FR-021**: All new endpoints MUST sit behind the global rate limiter established in feature 001; this feature adds no account-level lockout (deferred to v1.1).
- **FR-022**: Authentication and authorization failures MUST return the shared JSON error envelope established in feature 001, with stable machine-readable codes.

### Key Entities

- **User account**: Extends the entity from feature 001. Registration adds non-admin accounts. Attributes: unique username (case-insensitive), password hash, admin flag, creation time.
- **Invite code**: A one-time onboarding token. Attributes: the code value (unique, unguessable), the admin who created it, the account that consumed it (nullable until used), the consumption time (nullable until used).
- **Session token**: A signed, self-contained credential carrying the account identity, admin status, issue time, and expiry. Not stored server-side.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new person, given an invite code, can go from "register" to "logged in and identified" in under 2 minutes and fewer than 3 submissions.
- **SC-002**: A single invite code succeeds for exactly one registration: across 100 attempts (sequential or concurrent) using one code, exactly 1 account is created.
- **SC-003**: 100% of admin-only operations reject non-admin and unauthenticated callers (403 and 401 respectively) across the test matrix.
- **SC-004**: 100% of protected-endpoint requests with an absent, malformed, expired, or tampered token are rejected with 401.
- **SC-005**: Login with a wrong password and login for a non-existent username are indistinguishable in status code and message (no user enumeration), verified across sampled attempts.
- **SC-006**: Inspecting logs across a full register-then-login cycle reveals zero plaintext passwords, tokens, or invite code values.
- **SC-007**: A registered user's session token grants access to the "me" endpoint for the full configured lifetime and is rejected immediately after expiry.

---

## Assumptions

- Builds directly on feature 001: the `users` table, argon2id hashing, the TOML config (including `jwt_secret` and `jwt_ttl`), structured logging with secret redaction, the global rate limiter, and the shared JSON error envelope all already exist and are reused.
- The session token is a stateless signed token (JWT, HS256) signed with the configured `jwt_secret`; because that secret is persisted in config, a normal restart does NOT invalidate tokens — only an operator changing the secret does (this reconciles DESIGN §7.3's "restart rotates key" note with feature 001's persistent secret).
- Token lifetime comes from the configured `jwt_ttl` (default 2h).
- Registration does not auto-issue a token; the new user logs in separately (matches DESIGN §8.1 where login is the token-issuing operation).
- Invite codes are high-entropy random strings, URL-safe, with enough length to be infeasible to guess or enumerate; exact length/alphabet is an implementation detail.
- Minimum password length is 8 characters unless configured otherwise; no complexity rules in v1.
- Only administrators may create accounts indirectly (by issuing invites); there is no self-service open registration and no email verification in v1.
- Account-level brute-force lockout, invite expiry, invite revocation, and multi-use invites are out of scope (v1.1 / not planned per DESIGN).
- The first administrator already exists from feature 001's bootstrap; this feature does not create administrators (new registrations are always non-admin).
- This feature introduces no WireGuard, node, or zone behavior (features 003–005).
