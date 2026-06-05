# Phase 0 Research: Zone Owner Controls

Small feature constrained by DESIGN.md §5.4, ROADMAP feature 006, and the 005
codebase (zones/zone_members, `netfw` set/rule ops). No `NEEDS CLARIFICATION`. No new
dependency.

---

## R1. Ownership gate (403 vs 404)

**Decision**: a shared `ownedZone(w, r, identity) (*store.Zone, bool)` helper:
`GetByName(name)` → nil ⇒ **404 not_found**; else if `zone.OwnerID != caller` ⇒ **403
forbidden**; else return the zone. All three owner handlers call it first.

**Rationale**: ROADMAP explicitly says non-owner → 403. Missing zone → 404 is the
natural "no such resource". This deliberately differs from the no-enumeration 404 used
for node ownership (004): the user asked for an explicit forbidden signal here, and
zone names are globally unique/guessable anyway, so revealing existence to an
authenticated caller is acceptable (recorded in spec Assumptions). The helper removes
duplicated gate logic across the three handlers (constitution I).

---

## R2. Change password without ejecting members

**Decision**: `ZoneRepo.UpdatePassword(ctx, zoneID, newHash)` updates only
`zones.password_hash`. Memberships and nftables sets/rules are untouched, so existing
members keep reachability (FR-002). Future joins verify against the new hash (the join
handler already reads `password_hash` via `GetByName`), so the old password stops
working and the new one starts (FR-003).

**Rationale**: the password is a join-time credential only; it is not part of the
isolation state. Changing the hash is a single-row update with zero data-plane impact.
New password validated ≥ 8 chars (matches creation); same-value rotation is allowed.

---

## R3. Kick a member node (cross-user)

**Decision**: `kickMember` does `ownedZone` → `NodeRepo.GetByID(nodeID)` (unscoped, to
get the member's address) → `ZoneRepo.Leave(zoneID, nodeID)` → `netfw.RemoveMember(zoneID, ip)`.
`Leave` returns `ErrNotMember` (→ 404) when the node isn't in the zone; `GetByID`
returns `ErrNodeNotFound` (→ 404) when the node doesn't exist.

**Rationale**: the owner's authority is over the zone, not the node, so `GetByID` is
**unscoped** (any owner's node may be kicked, FR-005) — distinct from `GetOwned`
(004/005) which scopes to the caller. The kick removes only the membership + the set
element; the node record and its other zones are untouched (FR-006). Same
DB-authoritative + best-effort-nft + startup-reconcile pattern as 004/005.

**New store method**: `NodeRepo.GetByID(ctx, nodeID) (*Node, error)` — returns the node
(id, ip, pubkey) or `ErrNodeNotFound`. The existing `Leave` and `RemoveMember` are reused.

---

## R4. Delete a zone (destroy set + rule, release name)

**Decision**: `deleteZone` does `ownedZone` → `ZoneRepo.Delete(zoneID)` (already exists
from 005; cascades `zone_members` via FK) → `netfw.DeleteZone(zoneID)`.

**`netfw.DeleteZone(zoneID)`** (new): in the table's `forward` chain, delete the
rule(s) whose expressions reference the set `zone_<id>`, then delete the set itself.
The rule MUST be deleted before the set (the kernel rejects deleting a set still
referenced by a rule). Implemented with google/nftables: `GetRules(table, chain)` →
`DelRule` for rules referencing the set → `GetSetByName` → `DelSet` → `Flush`
(split into two flushes if a single batch rejects the ordering).

**Rationale**: deleting the zone row releases the unique `name` (FR-010) and cascades
the memberships (FR-009) while leaving `nodes` intact. Destroying the set + rule
removes all reachability the zone granted. The startup rebuild (005) already excludes
deleted zones, so any best-effort nft gap self-heals on restart (FR-013).

---

## R5. No app/startup change needed

**Decision**: reuse feature 005's `rebuildZoneRules` unchanged. A changed password
(hash in DB), a kicked member (membership row gone), and a deleted zone (zone row gone)
are all already reflected by `AllForRebuild` → `Manager.Rebuild`, so a restart
reconciles every owner operation (FR-013/SC-005) with no new wiring.

**Rationale**: the data plane is fully derived from the DB; the owner operations only
mutate the DB (+ best-effort live nft), so the existing rebuild is the backstop.

---

## R6. Endpoints & DTO

**Decision**: under `AuthRequired`:
- `PATCH /api/v1/zones/{name}` `{password}` → 200 (change password)
- `DELETE /api/v1/zones/{name}/members/{node_id}` → 204 (kick)
- `DELETE /api/v1/zones/{name}` → 204 (delete zone)

New DTO `ChangeZonePasswordRequest{ Password string }`. `node_id` via `r.PathValue("node_id")`.

**Rationale**: matches ROADMAP. The two `DELETE` patterns (`zones/{name}` vs
`zones/{name}/members/{node_id}`) are distinct stdlib mux patterns — no conflict.

---

## R7. What is deliberately deferred (scope guard)

- **No** ownership transfer — DESIGN v1.1.
- **No** admin override of zone control — only the owner manages a zone (008 handles
  admin-driven user deletion cascade).
- **No** new member-view endpoint — feature 005's `GET /zones/{name}/members` (any
  participant) is reused unchanged (FR-015).
- **No** IPv6.
