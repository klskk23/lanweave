<!--
SYNC IMPACT REPORT
==================
Version change: (uninitialized template) → 1.0.0
Bump rationale: First ratification of project constitution. No prior version
exists; placeholders replaced with concrete principles.

Modified principles: (none — initial)
Added principles:
  - I. Code Quality
  - II. Testing Standards (NON-NEGOTIABLE)
  - III. User Experience Consistency
  - IV. Performance Requirements
Added sections:
  - Security & Operational Discipline
  - Development Workflow & Quality Gates
  - Governance
Removed sections: (none — replaced template placeholders)

Templates requiring updates:
  ✅ .specify/templates/plan-template.md — Constitution Check gate is a generic
     placeholder; downstream /speckit-plan invocations will apply the new
     principles automatically. No edit needed.
  ✅ .specify/templates/spec-template.md — Spec template focuses on user
     scenarios and FRs which already align with Principles I, III; no edit
     needed for this initial ratification.
  ⚠ .specify/templates/tasks-template.md — Template currently states tests are
     OPTIONAL ("only include them if explicitly requested in the feature
     specification"). Principle II (Testing Standards, NON-NEGOTIABLE)
     contradicts this. Template updated in the same commit to mark tests as
     REQUIRED for every feature unless explicitly waived in the spec with
     justification.
  ✅ .specify/templates/checklist-template.md — Generic checklist template,
     no principle-specific content; no edit needed.
  ✅ CLAUDE.md — Not yet created. Runtime AI guidance will be added in a
     later /init pass; constitution will be referenced then.

Follow-up TODOs: none.
-->

# lanweave Constitution

## Core Principles

### I. Code Quality

Source code MUST be small, obvious, and reversible.

- Every package has a single, named responsibility. If a package needs three
  paragraphs to describe, it is two packages.
- No premature abstraction. Three similar callsites are better than one
  speculative interface. Abstract on the fourth, not the second.
- `gofmt`, `go vet`, and `staticcheck` MUST be clean on every commit. CI
  rejects red builds. Lint waivers require a written justification at the
  callsite.
- Errors are values; panics are forbidden outside `main` boot-time fatal paths
  and explicitly documented `panic-on-misuse` library helpers.
- SQLite is the single source of truth for persistent state. nftables rules
  and WireGuard peer tables are derivative state and MUST be reconstructible
  from the database at any time, with no hidden runtime-only memory.
- Configuration MUST be loaded once at startup from the documented TOML file.
  Scattered `os.Getenv` calls across the codebase are forbidden; environment
  variables override configuration only via a single, documented bridge.
- Comments explain WHY, never WHAT. Code that needs WHAT comments is code
  that needs renaming.

**Rationale**: A small team (initially one developer) cannot afford
codebases that drift. Strictness on size, obviousness, and reversibility
keeps the cognitive load low enough that the same person can revisit any
module after weeks away and still ship a fix in an afternoon.

### II. Testing Standards (NON-NEGOTIABLE)

Every feature ships with automated tests before it merges.

- Three test tiers are MANDATORY for any feature that crosses a process or
  kernel boundary: **unit** (pure-Go, table-driven), **integration** (real
  SQLite, real nftables on a privileged test environment, real `wireguard-go`
  when applicable), and **acceptance/smoke** (built binary exercised against
  the feature's `quickstart.md`).
- SQLite, nftables, and WireGuard MUST NOT be mocked. Tests that touch these
  systems run against real instances; CI provides them via container or
  privileged runner. A mock here passing while production fails is exactly
  the failure mode we are prepared to spend CI minutes to prevent.
- Each user story in a feature `spec.md` MUST have at least one acceptance
  test that demonstrates the story end-to-end. The story is not "done" until
  that test is green.
- New code SHOULD reach ≥ 70% line coverage; below this, the PR description
  MUST list which paths are uncovered and why. Coverage is a signal, not a
  gate, but unexplained drops fail review.
- Regression tests MUST accompany every bug fix. The test fails before the
  fix and passes after; that diff is part of the PR.
- Test code follows the same code-quality bar as production code (Principle
  I). Flaky tests are bugs and MUST be fixed or deleted, never retried.

**Rationale**: lanweave's correctness depends on three independently
stateful systems (DB, kernel firewall, kernel networking) staying in sync.
Test coverage at the boundary between them is the only defense against
silent drift. The "mocks forbidden for system boundaries" rule comes from a
broader industry pattern where green mocked tests masked broken production
migrations; we will not relearn that.

### III. User Experience Consistency

The Windows client is the only end-user surface in v1. Every screen, every
operation, every status message MUST behave like part of one application.

- Field representations are uniform across screens: an IP is always
  `100.127.x.y`, a node name is always rendered with its owner's username,
  a timestamp is always shown in the user's local zone with a tooltip for
  the ISO form.
- Every long operation (anything that could exceed 500 ms) MUST show
  immediate visible feedback (spinner, progress, or a "working…" message).
  Silent waits are forbidden — the user must never doubt whether the app
  heard them.
- Every destructive operation (delete node, delete zone, leave zone, kick
  member) MUST require explicit confirmation, and the confirmation MUST
  name the specific entity being affected.
- The first-run wizard MUST allow the user to cancel and go back at every
  step. No screen traps the user.
- Errors shown to the user are written for humans: no stack traces, no Go
  error chains, no internal IDs. Technical detail goes to the log; the UI
  shows a sentence the user can act on, plus a "copy details" affordance
  for filing a bug.
- Connection state (tunnel up/down, server reachable yes/no, current node
  IP) MUST be visible from any screen via a persistent status indicator.
- Keyboard navigation works in every dialog: Enter confirms the default
  action, Escape cancels.

**Rationale**: This is a security-relevant tool. Users who do not trust
what the app is doing will turn it off and route around it. Consistency
across the small surface area is cheap to maintain and is the single
biggest contributor to "this product feels reliable."

### IV. Performance Requirements

Every release MUST meet the following budgets on a typical small-VPS
deployment (2 vCPU, 2 GB RAM) and a typical Windows 10/11 client.

- **Server cold start to `/api/v1/healthz` 200**: ≤ 3 seconds.
- **API read endpoints (auth'd, P50 latency)**: ≤ 100 ms at 100 concurrent
  users with steady-state load.
- **API write endpoints (P50, includes nftables + WG side-effects)**: ≤
  300 ms at the same concurrency.
- **nftables set element add/remove**: ≤ 50 ms wall time, measured server
  side from API entry to kernel commit.
- **WireGuard first handshake after client `connect`**: ≤ 3 seconds end to
  end on a low-latency network.
- **Online-status update lag**: ≤ 30 seconds from a client's first or last
  handshake to the API reporting the corresponding `online` value.
- **Client UI input to server-reflected state**: ≤ 1 second on a local
  network (e.g., joining a zone shows the new member in the panel within
  1 s of the API responding).
- **Server resident memory at 1000 active nodes**: < 100 MB.
- **Server graceful shutdown after SIGTERM**: ≤ 10 seconds.

These are budgets, not aspirations. A PR that demonstrably exceeds any
budget MUST either bring numbers back under or include an updated budget
in this constitution (PATCH or MINOR bump as appropriate) with rationale.
Performance regressions discovered after merge are tracked as bugs at the
same severity as functional regressions.

**Rationale**: The product is a "set it and forget it" infrastructure
tool. Budgets above are calibrated to the user expectation of "fast as
SSH"; anything slower invites users to assume the service is hung and
take destructive action (kill, reinstall, switch tools).

## Security & Operational Discipline

These constraints bind every feature, even when not called out in the
feature's own spec.

- **Secrets in logs**: Plaintext passwords, TLS private keys, WireGuard
  private keys, JWT signing keys, invite codes, and zone passwords MUST
  NOT appear in any log line, error message, panic trace, or committed
  fixture. Tests assert this for every endpoint that handles such values.
- **Network input validation**: Every handler validates body size, content
  type, and field shapes at the boundary. Internal layers MAY assume
  validated input.
- **Crypto choices**: No new primitives. Use the Go standard library or
  vetted community packages (`golang.org/x/crypto`, `wireguard.com/wgctrl`,
  etc.). Hashing MUST be argon2id with parameters meeting current OWASP
  guidance.
- **Accepted risks register**: `DESIGN.md §11` is the only place project-wide
  security risks may be accepted. Anything not listed there requires either
  a fix or a `DESIGN.md` amendment (with version bump on this constitution
  if it touches a principle).
- **Dependency hygiene**: CVE-fix updates MUST land within 14 days of
  upstream disclosure. `go.mod` MUST pin minimum versions; the build is
  reproducible from `go.sum`.
- **Privilege**: The server runs as root (kernel `nftables` + WireGuard
  require it). Systemd unit MUST narrow `CapabilityBoundingSet` to the
  minimum needed (`CAP_NET_ADMIN`, plus filesystem caps for the data dir).
- **Single-instance assumption**: The current architecture assumes one
  server process per host (SQLite file lock, single nftables table, single
  WG interface). Features MUST NOT introduce hidden assumptions that
  silently break under future multi-instance deployments; if a feature
  requires shared state, it surfaces that requirement explicitly.

## Development Workflow & Quality Gates

These gates are enforced at PR review time.

- **Spec-Kit flow**: Every feature follows
  `/speckit-specify` → `/speckit-plan` → `/speckit-tasks` → `/speckit-implement`.
  Skipping a phase requires a one-line PR-description justification.
- **DESIGN.md authority**: `DESIGN.md` is the canonical cross-feature
  source of truth. A per-feature `spec.md` MAY refine but MUST NOT
  contradict it. If the feature requires a contradiction, `DESIGN.md` is
  updated in the same PR.
- **ROADMAP.md tracking**: When a feature is completed, its entry in
  `ROADMAP.md` is checked off in the merge commit. This is the canonical
  way to read "what's done."
- **PR checklist** (minimum): tests added/updated and green; lint clean;
  no `[NEEDS CLARIFICATION]` left in spec; CHANGELOG entry if user-visible.
- **Constitution Check gate** (in `/speckit-plan`): the plan MUST
  explicitly list which principles apply and how the design honors each.
  Any violation goes in the plan's Complexity Tracking table with a
  reason; reviewers MAY reject the plan there.
- **Solo-developer reality**: Self-review is acceptable but MUST be
  written (a self-review comment that lists the principles checked). This
  preserves the audit trail without requiring a second person.

## Governance

This constitution supersedes every ad-hoc decision and informal
convention. `DESIGN.md`, `ROADMAP.md`, and per-feature `spec.md` /
`plan.md` files all inherit from it.

**Amendments**: Any change to this file MUST update the Sync Impact
Report at the top, bump the version below per semver rules, and update
`LAST_AMENDED_DATE`. Amendments land in their own commit with the message
prefix `docs(constitution):`.

**Versioning policy**:

- **MAJOR**: A principle is removed, redefined in a backward-incompatible
  way, or governance procedures change in a way that invalidates past
  process.
- **MINOR**: A new principle is added, or an existing principle gains a
  materially new requirement.
- **PATCH**: Wording clarifications, typo fixes, rationale expansions, or
  threshold tightenings that do not change which features pass.

**Compliance reviews**: The `/speckit-plan` Constitution Check gate is
the primary review mechanism. Annual (or pre-release) audits MAY also
sweep the entire codebase for drift; findings land as a Complexity
Tracking entry on the next plan.

**Runtime guidance**: AI-assisted development tools follow `CLAUDE.md`
(when present) for project-specific patterns. `CLAUDE.md` MAY restate
constitutional principles for context but MUST NOT contradict them; in
case of conflict, this file wins.

**Version**: 1.0.0 | **Ratified**: 2026-06-05 | **Last Amended**: 2026-06-05
