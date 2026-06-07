# Feature Specification: Password Complexity

**Feature Branch**: `027-password-complexity`

**Created**: 2026-06-07

**Status**: Draft

**Input**: User description: "027 — 账号密码复杂度校验(客户端 + 服务端):至少 8 个字符、含大小写字母和数字。"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Server rejects weak account passwords at registration (Priority: P1)

A new user registers with an invite code, username, and password. The server
refuses to create the account unless the chosen password meets the strength
policy, and it never stores a non-compliant password.

**Why this priority**: This is the security guarantee. Even if a client is
modified, old, or bypassed, the server is the authority that keeps weak account
credentials out of the system. It is independently valuable and shippable on its
own.

**Independent Test**: Drive the registration endpoint directly with compliant
and non-compliant passwords; confirm compliant passwords create an account and
every non-compliant password is rejected with a validation error and no account
is created.

**Acceptance Scenarios**:

1. **Given** a valid invite and username, **When** the user registers with
   `Aa345678`, **Then** the account is created.
2. **Given** a valid invite and username, **When** the user registers with
   `aa345678` (no uppercase), **Then** registration is rejected and no account
   is created.
3. **Given** a valid invite and username, **When** the user registers with
   `Abcdefg` (7 chars), **Then** registration is rejected.
4. **Given** a valid invite and username, **When** the user registers with a
   65-character password, **Then** registration is rejected.
5. **Given** a valid invite and username, **When** the user registers with
   `Aa 345678` (contains a space) or `密码Aa345678` (non-ASCII), **Then**
   registration is rejected.

---

### User Story 2 - Registrant gets immediate, specific feedback (Priority: P2)

While filling in the registration form, the user is stopped from submitting a
non-compliant password and is told exactly which rule is unmet, without waiting
for a server round trip.

**Why this priority**: Improves completion rate and reduces frustration, but the
account is still protected by US1 even if this is absent — so it ranks below the
server guarantee.

**Independent Test**: In the registration form, enter passwords that each break a
single rule and confirm the form blocks submission and surfaces the specific
failing rule; enter a compliant password and confirm submission proceeds.

**Acceptance Scenarios**:

1. **Given** the registration form, **When** the user enters `aa345678`, **Then**
   submission is blocked and the form indicates a missing uppercase letter.
2. **Given** the registration form, **When** the user enters `Aa3`, **Then**
   submission is blocked and the form indicates the password is too short.
3. **Given** the registration form, **When** the user enters a compliant
   password, **Then** the form allows submission.
4. **Given** the user's language is Chinese, **When** a rule fails, **Then** the
   message is shown in Chinese; in English locale it is shown in English.

---

### User Story 3 - Rules are visible before the user types (Priority: P3)

The registration form shows a persistent description of the password rules
directly beneath the password field, so the user knows the requirements up front
rather than discovering them through repeated rejections.

**Why this priority**: A usability nicety that reduces trial-and-error; the
feature still functions without it, so it ranks last.

**Independent Test**: Open the registration form and confirm a static rule hint
("8–64 characters; upper, lower, and digit; ASCII only, no spaces") is visible
beneath the password field before any input, in the user's language.

**Acceptance Scenarios**:

1. **Given** the registration form is shown, **When** no password has been typed,
   **Then** the rule hint is already visible under the password field.
2. **Given** the user's language is Chinese, **When** the form is shown, **Then**
   the hint text is in Chinese.

---

### Edge Cases

- **Boundary lengths**: 7 chars rejected; exactly 8 accepted; exactly 64
  accepted; 65 rejected.
- **Single missing class**: has upper+lower but no digit → rejected; has
  upper+digit but no lower → rejected; has lower+digit but no upper → rejected.
- **Disallowed characters**: any space (leading, trailing, or internal) →
  rejected; any non-ASCII character (CJK, emoji, accented Latin) → rejected;
  ASCII control characters → rejected.
- **Allowed symbols**: ASCII punctuation/symbols are permitted and count toward
  neither blocking nor satisfying the letter/digit requirements (e.g.,
  `Aa3!5678` is valid).
- **Multiple failures at once**: a password breaking several rules surfaces a
  specific, deterministic reason (not a generic "invalid").
- **Existing accounts**: users whose passwords predate this policy (including the
  bootstrap admin) are NOT locked out — login does not re-check the policy.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: At account registration, the system MUST require the password to be
  between 8 and 64 characters inclusive.
- **FR-002**: At account registration, the system MUST require the password to
  contain at least one ASCII uppercase letter, at least one ASCII lowercase
  letter, and at least one digit.
- **FR-003**: At account registration, the system MUST reject any password that
  contains a space or any character outside the ASCII printable range (i.e.,
  only `!`–`~` are allowed; spaces, control characters, and non-ASCII characters
  are rejected).
- **FR-004**: ASCII symbol/punctuation characters MUST be allowed in passwords
  but MUST NOT be required.
- **FR-005**: The server MUST be the authoritative enforcer: a registration with
  a non-compliant password MUST be rejected with a validation error and MUST NOT
  create an account or store the password, regardless of client behavior.
- **FR-006**: The client and the server MUST apply identical rules so that any
  given password is accepted or rejected the same way by both (no divergence).
- **FR-007**: The client MUST block submission of the registration form while the
  password is non-compliant and MUST indicate which specific rule is unmet.
- **FR-008**: The client registration form MUST display a persistent description
  of the password rules beneath the password field, visible before any input.
- **FR-009**: The policy MUST apply only when an account password is being set
  (registration). Login MUST NOT enforce the policy, so existing accounts with
  weaker passwords continue to authenticate.
- **FR-010**: Zone passwords and the bootstrap admin password are OUT OF SCOPE
  and MUST be left unchanged by this feature.
- **FR-011**: The validation verdict MUST carry the specific failing rule (too
  short, too long, missing uppercase, missing lowercase, missing digit, contains
  a space, contains a non-ASCII character), and the **client-facing** feedback MUST
  render that specific rule to the user. The server's English rejection message MAY
  collapse related reasons into a shared phrase (e.g. one "must be 8-64 characters"
  for short/long, one "must include an uppercase letter, a lowercase letter, and a
  digit" for the missing-class reasons); precise per-rule guidance is the client's
  responsibility, where the user actually reads it.
- **FR-012**: Server-returned validation messages MUST be in English, consistent
  with the existing API error convention; client-displayed messages MUST be
  localized to the user's selected language.

### Key Entities *(include if feature involves data)*

- **Account Password**: the secret a user chooses at registration to authenticate
  later. This feature constrains what values are acceptable at the moment it is
  set; it does not change how it is stored or verified.
- **Password Policy**: the single set of strength rules (length bounds, required
  character classes, allowed character set) applied identically by client and
  server.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of registration attempts whose password violates any policy
  rule are rejected — no account with a non-compliant password can be created
  through registration.
- **SC-002**: A registrant entering a non-compliant password learns the specific
  unmet rule from the form itself, before submitting, without a server round
  trip.
- **SC-003**: The password rules are visible in the registration form before the
  user types anything (persistent hint present in 100% of form displays).
- **SC-004**: 0 existing accounts are locked out by this change — every account
  that could log in before can still log in (login is not gated by the policy).
- **SC-005**: For any given candidate password, client and server agree on
  accept/reject in 100% of cases (no rule drift between the two).

## Assumptions

- The only place an account password is set today is the registration flow; there
  is no separate "change account password" capability, so this policy attaches at
  registration. If such a capability is added later, it is expected to reuse the
  same rules.
- Login is intentionally not gated by the policy; the policy is enforced only when
  a password is being set, so pre-existing (possibly weaker) passwords remain
  usable. No retroactive invalidation or forced reset is performed.
- Zone passwords and the config-file bootstrap admin password keep their current
  behavior and are not subject to this policy.
- "Letter case" classes are defined over ASCII letters only; this is acceptable
  for this tool's user base, and it is the reason non-ASCII (e.g., Chinese)
  passwords — which have no case — are rejected outright rather than partially
  accepted.
- The 64-character upper bound is a deliberate, comfortable ceiling that also
  bounds the work the credential-hashing step has to do on attacker-supplied
  input (a defensive cap, not a hard technical limit of the hashing scheme).
- Both the client and the server can evaluate the same rule set, allowing a single
  shared definition to drive both (satisfying FR-006).
- Existing automated tests that register accounts with passwords that do not meet
  the new policy will be updated to compliant values as part of this work; this is
  expected and required, not a regression.
