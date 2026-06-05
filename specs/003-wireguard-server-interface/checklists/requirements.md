# Specification Quality Checklist: WireGuard Server Interface

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

- This is an infrastructure feature with an **operator** actor and no end-user UI or
  HTTP API; user stories are framed as operator journeys, which is appropriate.
- Domain terms (WireGuard interface, nftables/isolation table, IP forwarding) are
  used at the capability level because they are the product's defined architecture
  (DESIGN.md); the specific libraries/syscalls are deliberately left to plan/research.
- **Key safety call-out**: FR-004 / SC-005 require that a present-but-corrupt key
  file aborts startup and never silently regenerates — silent regeneration would
  rotate the server identity and orphan every client. This is the most important
  correctness/security requirement of the feature.
- **Testing reality** (flagged for the planner): creating a kernel WireGuard
  interface and an nftables table requires root/`CAP_NET_ADMIN`. The pure logic
  (key persistence + permissions, first-address derivation, config consumption) is
  fully unit-testable unprivileged; the kernel-touching behavior needs a privileged
  runner. The dev environment observed (uid 1000, `nft` CLI absent though `nf_tables`
  loaded) cannot exercise the privileged paths — the plan must account for this
  without violating constitution Principle II's "no mocking of system boundaries"
  (i.e., run real privileged integration where privilege exists; skip with a clear
  signal otherwise, not a mock).
- Scope guard: NO client peers (004), NO zone groups/allow rules (005), NO new HTTP
  endpoint. The server public key is produced here but surfaced by a later feature.
- Next after this: `004-node-registration-and-ipam` (depends on 002 + 003).
