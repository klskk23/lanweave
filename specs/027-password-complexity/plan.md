# Implementation Plan: Password Complexity

**Branch**: `027-password-complexity` | **Date**: 2026-06-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/027-password-complexity/spec.md`

## Summary

Account passwords set at registration must be 8–64 characters, contain at least
one ASCII uppercase letter, one ASCII lowercase letter, and one digit, and use
only ASCII printable characters with no spaces. The rule is defined once in a new
shared package `pkg/passwordpolicy` whose `Validate` returns a typed reason on
failure. The server's `register` handler is the authoritative enforcer (rejects
with `validation_error`/400, English message). The Fyne client imports the same
package to block submission of the registration form with a per-rule, localized
inline message, and shows a persistent rule hint beneath the password field. Login
is not gated, so existing accounts (and the bootstrap admin) keep working. Zone
passwords and the bootstrap admin password are out of scope.

## Technical Context

**Language/Version**: Go 1.23 (single module `lanweave`).

**Primary Dependencies**: standard library only for the policy itself (`unicode`,
`strings`); existing `golang.org/x/crypto/argon2` for hashing (unchanged); Fyne v2
for the client wizard; existing `internal/client/i18n` for localization.

**Storage**: SQLite via `modernc.org/sqlite`. **No schema change** — this feature
constrains an input value, it does not persist anything new.

**Testing**: `go test`; server/store/api suites run namespace-isolated under
`unshare -rUn`; client wizard tests run with loopback up. Policy package gets pure
table-driven unit tests. No system boundary is mocked (Constitution II).

**Target Platform**: Linux server; Windows 10/11 Fyne client.

**Project Type**: Single Go module — server (`internal/server/...`), client
(`internal/client/...`), shared wire/util packages (`pkg/...`).

**Performance Goals**: Policy validation is an O(n) scan over ≤64 bytes — negligible
against the existing argon2id hash on the register path; no measurable change to the
Constitution IV write-endpoint budget.

**Constraints**: Validation rule MUST be identical on client and server (single
shared source). Server messages English-hardcoded (existing API convention); client
messages localized. ASCII-only letter classes (non-ASCII rejected outright).

**Scale/Scope**: One new pure package (~1 file + test), one server handler edit, one
client wizard edit, new protocol error-reason constants, new i18n keys (zh-Hans/en),
DESIGN.md §7 amendment, ROADMAP 027 entry, and test-fixture password updates across
existing suites.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS. New package `pkg/passwordpolicy` has a single named
  responsibility (decide whether a candidate account password is acceptable, and
  why). No premature abstraction: one `Validate` function returning a typed reason;
  the server `minPasswordLen` length-only check is replaced, not layered on. `gofmt`
  / `go vet` / `staticcheck` clean; errors are values (typed reason, no panics).
- **II. Testing (NON-NEGOTIABLE)** — PASS. `passwordpolicy` gets exhaustive
  table-driven unit tests (every boundary and every failure reason). Each user story
  gets an acceptance test: US1 → API register test (real SQLite) asserting reject +
  no account; US2/US3 → client wizard tests asserting submit-blocking, per-reason
  message, and persistent hint. No mocking of SQLite. Existing fixtures that use
  non-compliant passwords on the register path are updated (failing-before behavior
  is the migration, not a regression to hide).
- **III. UX Consistency** — PASS. Client errors stay human-readable and localized
  (no Go error chains surfaced); the persistent hint and per-rule feedback make the
  requirement legible up front; Enter/Escape behavior of the wizard is unchanged.
- **IV. Performance** — PASS. Bounded O(64) scan; no new I/O, no schema, no
  measurable latency on the register write path.
- **Security & Operational Discipline** — PASS. No new crypto (argon2id hashing
  unchanged; the policy never hashes or logs the password). Passwords MUST NOT appear
  in logs — the handler already avoids logging the body; the typed reason carries no
  password material. Boundary input validation is exactly what this feature
  strengthens. No accepted-risk register change needed.
- **Development Workflow** — PASS. Full spec-kit flow; DESIGN.md §7 amended in the
  same PR (account password policy); ROADMAP 027 entry added.

**Result**: No violations. Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/027-password-complexity/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── passwordpolicy.md   # Validate() contract: rules, reason enum, ordering
│   └── register.md         # register endpoint behavior with the policy applied
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
pkg/
├── passwordpolicy/
│   ├── passwordpolicy.go      # NEW: Validate(pw) (Reason, bool) + Reason consts/String
│   └── passwordpolicy_test.go # NEW: table-driven unit tests (boundaries + all reasons)
└── protocol/
    └── auth.go                # (unchanged for wire types; no new response field)

internal/server/api/
├── auth_handlers.go           # EDIT: register() uses passwordpolicy.Validate; drop
│                              #       minPasswordLen length-only check
└── auth_handlers_test.go      # EDIT: register rejection cases per reason; fixtures

internal/client/ui/
└── wizard.go                  # EDIT: stepAuth() — block submit on invalid (CreateAccount),
                               #       per-reason inline message, persistent rule hint label

internal/client/i18n/
├── zh-Hans.json               # EDIT: wizard.pwRule* keys (hint + per-reason messages)
└── en.json                    # EDIT: same keys

# Fixture updates (compliant passwords) across existing suites that register via the
# HTTP/handler path, plus a consistency sweep of store-layer fixtures:
internal/server/api/*_test.go, internal/server/store/*_test.go,
internal/client/onboard/*_test.go, internal/client/panel/*_test.go

DESIGN.md                      # EDIT: §7 认证与权限 — account password policy
docs/ROADMAP.md                # EDIT: 027 entry checked off at merge
```

**Structure Decision**: Single Go module. The policy lives in `pkg/passwordpolicy`
(not `pkg/protocol`) because it is shared *logic* importable by both client and
server, with no wire-type coupling — keeping it separate from `pkg/protocol` honors
the single-responsibility rule (Principle I). No new persistent entity, so no
migration and no `internal/server/store` schema change.

## Complexity Tracking

> No constitution violations. No entries.
