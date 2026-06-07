# Research: Password Complexity

All open questions were resolved in a requirements interview before planning; this
file records the decisions, their rationale, and the alternatives rejected.

## Decision 1 — Scope is the account password only

**Decision**: The policy applies only to the user **account** password, enforced at
registration. Zone passwords and the config-file bootstrap admin password are out of
scope and unchanged.

**Rationale**: Account passwords are the per-user authentication secret with the
broadest blast radius. Zone passwords are shared, rotatable group secrets with a
different threat model; the bootstrap admin password is a deploy-time operator value
in a file already treated as a secret. Mixing them would couple unrelated change.

**Alternatives considered**: Apply to all three (rejected — different threat models,
larger fixture churn, no requested benefit). Apply to zone passwords too (deferred
to its own evaluation).

## Decision 2 — Exact rule

**Decision**: 8–64 characters; ≥1 ASCII uppercase, ≥1 ASCII lowercase, ≥1 digit;
only ASCII printable characters `0x21`–`0x7E`; spaces and any non-ASCII character
rejected; ASCII symbols allowed but not required. Length is counted in characters,
which equals bytes because the input is ASCII-only.

**Rationale**: Matches the stated requirement and removes every ambiguity that would
let client and server drift. ASCII-only letter classes make the rule deterministic
and are the explicit reason non-ASCII passwords (which have no case) are rejected
outright rather than partially accepted.

**Alternatives considered**: Unicode-aware case classes (rejected — CJK has no case,
so "中文+digits" could never satisfy upper/lower; more code, more surprise). Require a
symbol (rejected — not requested; raises friction). Allow internal spaces (rejected —
user chose to forbid all spaces; avoids trailing-space foot-guns).

## Decision 3 — 64-character cap rationale (argon2id, not bcrypt)

**Decision**: Keep the 64-character maximum as a deliberate defensive ceiling.

**Rationale**: Hashing here is **argon2id** (`internal/server/auth/password.go`),
which has **no** 72-byte truncation limit (that is a bcrypt property and does not
apply). The cap is therefore justified purely as a defensive bound on the work an
attacker-supplied input forces the hash step to do, plus a sane UX ceiling — not as a
technical limit of the scheme. An earlier interview note referencing a 72-byte hash
limit was corrected during planning.

**Alternatives considered**: No upper bound (rejected — unbounded input on a
deliberately slow hash is a needless DoS surface). 72 (rejected — 64 is the value the
user chose; the bcrypt-derived 72 is irrelevant here).

## Decision 4 — Single shared validator in `pkg/passwordpolicy`

**Decision**: One package `pkg/passwordpolicy` exposes `Validate`, imported by both
the server handler and the Fyne client. It returns a **typed reason** (enum) plus an
ok bool, never the password.

**Rationale**: Both ends are Go, so a single definition is the only durable way to
guarantee client and server never disagree (FR-006/SC-005). A typed reason lets each
side render its own message — English-hardcoded server error vs. localized client
text — from the same verdict. Lives outside `pkg/protocol` because it is shared logic
with no wire-type coupling (Principle I single responsibility).

**Alternatives considered**: Duplicate the rule in client and server (rejected —
guaranteed drift). Put it in `pkg/protocol` (rejected — protocol holds wire types;
mixing logic in dilutes its responsibility). Return only a bool (rejected — loses the
per-rule feedback US2 requires).

## Decision 5 — Server authoritative; login not gated

**Decision**: The `register` handler validates and rejects with the existing
`validation_error`/400 envelope (English message). Login performs no policy check.

**Rationale**: The server is the security boundary regardless of client state
(FR-005). Gating login would lock out every pre-policy account and the bootstrap
admin for zero security gain (the password is already set); the policy belongs at
set-time only (FR-009/SC-004).

**Alternatives considered**: Re-validate at login and force a reset on weak
passwords (rejected — out of scope, hostile UX, no request for it).

## Decision 6 — Client blocks submit + per-reason message + persistent hint

**Decision**: In the wizard `stepAuth`, when mode is CreateAccount, a non-compliant
password blocks form submission and shows the specific failing reason inline
(localized); a persistent static rule hint sits beneath the password field, visible
before any input. SignIn mode is unaffected (login not gated).

**Rationale**: Immediate local feedback (no round trip) improves completion; the
persistent hint sets expectations up front (US2/US3). The server still re-validates —
the client is convenience, never trust.

**Alternatives considered**: Send-and-let-server-reject (rejected — extra round trip,
worse UX). Live per-rule checklist turning green (rejected — chose the simpler static
hint; richer widget not worth the complexity now). Gate SignIn too (rejected — login
not gated).

## Decision 7 — Reason rendering split: server English, client i18n

**Decision**: Server maps each reason to a fixed English `validation_error` message
(matches all existing API errors). Client maps each reason to a localized string via
new `wizard.pwRule*` i18n keys in `zh-Hans.json` and `en.json`.

**Rationale**: Consistency with the codebase's existing conventions on each side; no
new i18n machinery on the server, full localization where the human reads it.

**Alternatives considered**: Localize server messages (rejected — no existing
mechanism; all API errors are English). One generic message both sides (rejected —
loses the per-rule precision US2 needs).

## Decision 8 — Test-fixture migration is in scope

**Decision**: Update every test that registers an account with a now-non-compliant
password to a compliant value, and sweep store-layer fixtures for consistency.

**Rationale**: The new rule deterministically rejects fixtures like `bobs-strong-pw`
(no uppercase/digit). Updating them is the expected, required migration — not a
regression being papered over. Doing the store-layer sweep too keeps fixtures uniform
and avoids future confusion even where the store layer bypasses handler validation.

**Alternatives considered**: Only fix handler-path fixtures (rejected — user asked to
fix all password-related test logic for consistency).

## Determinism note (testing)

The policy is a pure function of its string input — no clock, no I/O, no randomness —
so its tests are inherently deterministic and need no namespace isolation. The
register acceptance test runs under `unshare -rUn` against real SQLite per
Constitution II; the client wizard tests run with loopback up like the existing
panel/onboard suites.
