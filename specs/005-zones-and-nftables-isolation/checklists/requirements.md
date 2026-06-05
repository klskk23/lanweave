# Specification Quality Checklist: Zones and nftables Isolation

**Purpose**: Validate specification completeness and quality before proceeding to planning

**Created**: 2026-06-05

**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Reachability is expressed at the capability level ("can/cannot exchange tunnel
  traffic"); the nftables set/rule mechanism (DESIGN §6) is left to plan/research.
- **Key correctness call-outs**:
  - FR-018 — node deletion must clear memberships + rules, because feature-004 IP
    recycling means a stale set element would otherwise let a NEW node inherit a
    deleted node's zone reachability. This is the most important consistency
    requirement and couples this feature with feature 004's node-delete path.
  - FR-017 — DB is the source of truth; rules rebuilt at startup (extends the
    feature-003 startup rebuild from the empty skeleton to populated zone sets/rules).
  - FR-009/010 — same-zone admit + cross-zone deny + multi-zone membership are the
    core isolation semantics; reachability is per shared zone, not transitive.
- **No-enumeration decisions**: join returns one generic error for wrong-password and
  unknown-zone (FR-006); members/zone visibility refuses non-members as not-found
  (FR-016) — mirroring the login/node no-enumeration choices.
- **Scope guard**: owner-only management (change password / kick / delete zone) is
  feature 006; full user-deletion cascade is feature 008. This feature establishes
  the owner field and the create/join/leave/list surface only.
- **Testing reality**: like 003/004, the real nftables set/element effects and
  node-to-node reachability need CAP_NET_ADMIN; verified as real (non-mocked)
  integration under root / `unshare -rUn`, skipping with a clear signal otherwise.
  Zone CRUD + membership against SQLite is fully testable unprivileged.
- Next after this: `006-zone-owner-controls` (depends on 005).
