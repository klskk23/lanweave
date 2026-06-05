# Specification Quality Checklist: Node Registration and IPAM

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

- WireGuard/peer/tunnel terms are used at the capability level (the product's
  defined architecture, DESIGN.md); the specific control mechanism (wgctrl) is left
  to plan/research.
- **Key correctness call-outs**:
  - FR-004 — registration is atomic across DB + tunnel (peer-add failure rolls back
    the node/address), so the two never drift.
  - FR-018 — the DB is the source of truth and peers are rebuilt from it at startup,
    so nodes survive a relay restart (extends feature 003's interface-preservation to
    the peer set). This is what lets connected clients keep working across restarts.
  - FR-014/015/016 — lowest-free allocation + immediate recycle + concurrency safety
    are the IPAM integrity guarantees (DESIGN §3.3).
- **New config need (for the planner)**: the relay's public tunnel endpoint
  (host clients dial over UDP) is operator-configured and may differ from the API
  address; the plan will add a `wireguard.endpoint` config value consumed by the
  server-info operation (FR-009).
- **Testing reality**: like feature 003, adding/removing real WireGuard peers and the
  startup peer rebuild require CAP_NET_ADMIN; those run as real (non-mocked)
  integration under root / `unshare -rUn` and skip with a clear signal otherwise. The
  IPAM allocation logic and node CRUD against SQLite are fully unit/integration
  testable without privilege.
- Scope guard: NO zone membership/reachability (005), NO user-deletion cascade (008),
  NO online status (007). A registered node reaches only the relay until a zone admits
  it (default-deny forward chain from 003 still blocks node-to-node).
- Next after this: `005-zones-and-nftables-isolation` (depends on 004).
