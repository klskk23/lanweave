# Specification Quality Checklist: Zone Owner Controls

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

- Small feature: **no new tables**. It mutates/deletes `zones` and `zone_members`
  (feature 005) and the nftables sets/rules; reuses argon2id, auth, the startup rebuild.
- **Key correctness call-outs**:
  - FR-002/003 — password change must NOT eject members; it only governs future joins.
    (Contrast with the alternative "kick everyone on rotation" — DESIGN §5.4 chose
    keep-members, ROADMAP confirms.)
  - FR-009 — deleting a zone removes memberships + isolation rules but NOT the member
    nodes; the name is released (FR-010).
  - FR-005/006 — the owner may kick ANY member node, including another user's; this is
    the first cross-user mutation, distinct from 004/005 where users acted on their own.
- **Authorization decision (explicit, per ROADMAP)**: non-owner → 403, nonexistent zone
  → 404 (recorded in Assumptions). This deliberately differs from the no-enumeration
  404 used on node ownership in 004 — here the user asked for 403 for non-owners.
- **Member-view is inherited from 005** (any participant); FR-015 states this feature
  adds no new restriction and no new endpoint for it.
- **Testing reality**: like 003–005, the real nftables effects (element removal on kick,
  set/rule destruction on delete, restart rebuild) need CAP_NET_ADMIN → real
  (non-mocked) integration under root / `unshare -rUn`, skipping with a clear signal
  otherwise. Zone password update + ownership authz + membership removal against SQLite
  are fully testable unprivileged.
- Scope guard: NO ownership transfer (v1.1), NO admin override of zone control, NO
  user-deletion cascade (008).
- Next after this: `007-node-online-status` or the Windows client line (009–011).
