# Feature Specification: Invite Code Expiry

**Feature Branch**: `026-invite-expiry`

**Created**: 2026-06-07

**Status**: Draft

**Input**: User description: "为邀请码添加有效期。admin 签发的邀请码默认 24h 过期，有效期由配置文件全局值 invite_ttl 控制（0/空 = 永不过期，无 per-code 参数）。过期码注册被拒并归入通用「邀请码无效」错误。旧码祖父化永久有效。lanweavectl 建码输出带过期时间。"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Expired codes can no longer be redeemed (Priority: P1)

An operator runs an invite-only deployment. Invite codes get shared over chat, screenshots, and email, and a leaked-but-unused code currently stays usable forever. The operator wants codes to stop working automatically after a bounded window, so a stale or leaked code can't be redeemed by an unintended party days later.

**Why this priority**: This is the core security value of the slice. Without expiry enforcement at registration, nothing else in the feature matters. It is the minimum shippable increment.

**Independent Test**: Generate an invite code under a short configured expiry, wait past the window, and attempt to register with it — registration is rejected. Generate another code and register with it immediately — registration succeeds.

**Acceptance Scenarios**:

1. **Given** expiry is configured to a non-zero window and an admin has just generated a code, **When** a prospective user registers with that code before the window elapses, **Then** registration succeeds.
2. **Given** the same configuration, **When** a prospective user registers with that code after the window has elapsed, **Then** registration is rejected.
3. **Given** an expired code is rejected, **When** the registrant reads the error, **Then** it is the same generic "invalid invite code" response returned for an unknown or already-used code, with no indication that the code merely expired.

---

### User Story 2 - Operator controls the expiry window globally (Priority: P2)

The operator wants a single place to set how long invite codes stay valid, matched to their security posture, and the ability to turn expiry off entirely for a closed, trusted setup. Deployments that predate this feature must not have their already-issued codes silently invalidated on upgrade.

**Why this priority**: Makes the P1 behavior tunable and safe to roll out. Essential for adoption but builds on the enforcement from US1.

**Independent Test**: Set the expiry window in configuration, generate a code, and confirm its validity window matches. Set the window to the "disabled" value, generate a code, and confirm it never expires. Upgrade a deployment that already has unused codes and confirm those codes still work.

**Acceptance Scenarios**:

1. **Given** the operator sets the global expiry window to a specific duration, **When** an admin generates a code, **Then** that code's validity window matches the configured duration.
2. **Given** the operator sets the expiry setting to its "disabled" value (zero or empty), **When** an admin generates a code, **Then** that code never expires.
3. **Given** a deployment that already had unused invite codes before this feature was installed, **When** the feature is deployed, **Then** every pre-existing unused code remains redeemable (no retroactive expiry).
4. **Given** the operator provides an invalid expiry value (e.g., a negative duration), **When** the server starts, **Then** startup fails with a configuration error rather than applying an unsafe default.

---

### User Story 3 - Admin sees the expiry when generating a code (Priority: P3)

When an admin generates an invite code to hand to someone, they want to know how long the recipient has to redeem it, so they can communicate the deadline (or that there is none).

**Why this priority**: A usability nicety that prevents the admin from issuing a code with an unknown lifetime. Independent of enforcement; the feature is still useful without it.

**Independent Test**: Generate a code via the admin tooling and confirm the output states the expiry moment (or clearly indicates the code never expires).

**Acceptance Scenarios**:

1. **Given** expiry is enabled, **When** an admin generates a code, **Then** the generation output includes the moment the code expires.
2. **Given** expiry is disabled, **When** an admin generates a code, **Then** the generation output clearly indicates the code never expires.

---

### Edge Cases

- **Boundary moment**: A code is considered expired only when the current time is strictly past its expiry moment; at the instant of creation it is still valid.
- **Disabling expiry after codes were already stamped**: Codes generated while expiry was enabled keep their expiry and will still expire; only codes generated after expiry is disabled are issued without an expiry. Disabling is not retroactive in either direction.
- **Used vs expired**: An already-used code remains invalid regardless of expiry; "used" and "expired" both resolve to the same generic rejection.
- **Clock reference**: Expiry is evaluated against the server's clock at registration time.
- **No background cleanup**: Expired, unused codes remain stored; they are simply rejected at registration and are never auto-deleted.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST stamp each newly generated invite code with an expiry moment computed from its creation time plus the configured expiry window.
- **FR-002**: System MUST reject any registration attempt that presents an invite code whose expiry moment has passed.
- **FR-003**: System MUST return, for an expired code, the same generic rejection it returns for an unknown, already-used, or otherwise invalid code — without disclosing that the code specifically expired.
- **FR-004**: System MUST allow the expiry window to be set through a single global configuration value. The shipped example configuration provides a 24-hour value so new deployments have expiry enabled out of the box; the system itself applies NO built-in default — when the value is absent or empty, codes never expire (see FR-005).
- **FR-005**: System MUST treat a configured expiry value of zero (or empty/unset) as "codes never expire," stamping such codes with no expiry.
- **FR-006**: System MUST treat any invite code that carries no expiry stamp as never-expiring.
- **FR-007**: System MUST NOT retroactively assign expiry to, or invalidate, invite codes that existed before this feature was deployed.
- **FR-008**: System MUST preserve all existing invite-code behavior — one-time use, invalidation on redemption, and admin-only issuance — with expiry added on top, not replacing any of it.
- **FR-009**: System MUST surface the expiry moment (or a clear "never expires" indication) to the admin at the time a code is generated.
- **FR-010**: System MUST retain expired, unused invite codes rather than deleting them; enforcement happens only at registration.
- **FR-011**: System MUST reject an invalid configured expiry value (e.g., negative duration) at startup rather than starting with an unsafe fallback.

### Key Entities *(include if feature involves data)*

- **Invite code**: An existing one-time, admin-issued credential that authorizes one registration. It gains an optional expiry moment. Absent/empty expiry means the code never expires. Its existing attributes (the code value, who issued it, whether/when it was used) are unchanged.
- **Invite expiry setting**: A single global configuration value expressing how long newly issued codes stay valid. Default 24 hours; a zero/empty value disables expiry. Applied at code-generation time only.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Under the default configuration, an invite code can no longer be redeemed once its configured window (24 hours) has elapsed since creation.
- **SC-002**: Under the default configuration, an invite code is redeemable at any point within its 24-hour window.
- **SC-003**: With expiry disabled, a newly generated code remains redeemable with no time limit.
- **SC-004**: 100% of unused invite codes that existed before deployment remain redeemable after the feature is deployed.
- **SC-005**: A registrant cannot distinguish an expired code from an unknown code based on the rejection they receive.
- **SC-006**: An admin generating a code can see, from the generation output alone, when that code expires (or that it never does).

## Assumptions

- Expiry is evaluated against the server's clock at the moment of registration; client clocks are irrelevant.
- The example/default configuration ships with a 24-hour window, so new deployments have expiry enabled out of the box; operators may change or disable it.
- Configuration is read at startup, consistent with how the deployment already loads other settings (no hot reload of the expiry window).
- "Used" status continues to take precedence: a redeemed code is rejected regardless of its expiry state.
- Expiry is additive to the existing invite-only model (slice 002): only admins issue codes, each code is single-use, and redemption invalidates it. Self-service or email-delivered codes are explicitly out of scope for this slice.
- The admin code-generation surface (the command-line helper) is where expiry is surfaced; no new code-listing capability is introduced.
