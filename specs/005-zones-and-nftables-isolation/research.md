# Phase 0 Research: Zones and nftables Isolation

Decisions constrained by DESIGN.md §4/§5/§6/§7, the constitution, and the 001–004
codebase (notably the existing `netfw` table from 003 and node IPs from 004). No
`NEEDS CLARIFICATION` remains. No new dependency.

---

## R1. Per-zone set + same-zone accept rule (google/nftables expressions)

**Decision**: one anonymous-named set `zone_<id>` per zone (KeyType `ipv4_addr`),
holding member node addresses; one rule per zone in the `forward` chain that accepts
traffic whose source AND destination are both in that set. Default policy stays drop
(from 003), so cross-zone and zone-less traffic is denied.

**The rule** (`ip saddr @zone_<id> && ip daddr @zone_<id> accept`) as google/nftables
expressions, with an `nfproto ipv4` guard (the table is `inet`, so it sees v4 and v6):
```go
exprs := []expr.Any{
    &expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
    &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}}, // = 2
    &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4}, // IPv4 saddr
    &expr.Lookup{SourceRegister: 1, SetName: "zone_<id>", SetID: set.ID},
    &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4}, // IPv4 daddr
    &expr.Lookup{SourceRegister: 1, SetName: "zone_<id>", SetID: set.ID},
    &expr.Verdict{Kind: expr.VerdictAccept},
}
```

**Rationale**:
- DESIGN §6.2 specifies exactly this rule shape. Both-in-set is what makes a zone a
  mutual group (a member can talk to any other member, never to outsiders).
- A node in multiple zones has its address in multiple sets; each zone's rule
  independently admits same-zone pairs, giving per-zone (non-transitive) reachability.
- The `nfproto ipv4` guard prevents a v6 packet's bytes from being mis-read as a v4
  address (defensive; our tunnel is IPv4-only).
- `unix.NFPROTO_IPV4` comes from `golang.org/x/sys/unix` (already an indirect dep via
  netlink); no new dependency.

**Verification**: asserted against the **real kernel** — after operations, fetch the
set elements (`GetSetByName` + `GetSetElements`) and the chain rules (`GetRules`) and
check they match memberships. (R7.)

---

## R2. Set identity & element type

**Decision**: set name `zone_<id>` (the zone's DB id, stable across restarts);
`KeyType: nftables.TypeIPAddr` (4-byte IPv4); element key = `ip.As4()`.

**Rationale**: the DB id is a stable, unique handle (names can change in 006; ids
don't). 4-byte IPv4 keys match the node address space. `SetAddElements` /
`SetDeleteElements` (by `GetSetByName`) give the incremental add/remove DESIGN §6.3 wants.

---

## R3. Rebuild (startup) vs incremental (runtime)

**Decision**:
- **Startup**: `Manager.Rebuild(zones []ZoneState)` drops the table and recreates
  table + forward chain (drop policy) + every zone's set + elements + accept rule in
  one batched flush — exactly matching the DB (FR-017). 003's empty-table call becomes
  `Rebuild(nil)`.
- **Runtime**: `AddZone(id)` (empty set + accept rule, on zone create), `AddMember(id, ip)`
  (on join), `RemoveMember(id, ip)` (on leave / node delete). (Zone delete is feature 006.)

**Rationale**: matches DESIGN §6.3 (full rebuild at startup, incremental at runtime).
The full rebuild is the self-healing backstop; incremental ops keep live state correct
without dropping the whole table on every membership change.

**Set/rule existence ordering**: a zone's set+rule are created at zone-create time
(`AddZone`) and recreated at startup, so a join always finds an existing set to add to.

---

## R4. Atomicity & consistency (DB ↔ nftables)

**Decision** (same pattern as feature 004):
- **Create zone**: INSERT zone (DB) → `AddZone` (nft). On `AddZone` failure, delete the
  zone row — nothing persists.
- **Join**: INSERT membership (idempotent) → `AddMember`. On `AddMember` failure, delete
  the membership row.
- **Leave**: DELETE membership (DB authoritative) → `RemoveMember` best-effort (log on failure).
- **Node delete (FR-018)**: read the node's zone ids + its address, DELETE the node
  (cascades `zone_members`), remove the WG peer, then `RemoveMember(zoneID, ip)` for each
  former zone (best-effort).
- **Startup**: `Rebuild` reconciles any drift to match the DB.

**Rationale**: the DB is the single source of truth (constitution I; DESIGN §6.3).
Atomic create/join give correct live behavior; the startup rebuild backstops crashes.
FR-018 is the critical one: without removing set elements on node delete, feature-004
IP recycling would let a NEW node (reusing the freed address) inherit the deleted
node's zone reachability — so node delete MUST clear the elements.

---

## R5. No-enumeration & authorization

**Decision**:
- **Join**: an unknown zone name OR a wrong password returns ONE generic error
  (`invalid_zone_or_password`, 403). On unknown-zone, run a dummy argon2 verify
  (`auth.DummyVerify`) to equalize timing (mirrors login, feature 002).
- **Node ownership**: join/leave naming a node the caller does not own → `not_found`
  (404), reusing the ownership-scoped lookup from feature 004.
- **Members view**: only a participant (owner, or has a member node) may view; a
  non-participant → `not_found` (404), not disclosing the zone (FR-016).

**Rationale**: zone names are global and guessable; the generic join error blocks
name+password enumeration. Member visibility is gated so zones aren't a directory.

---

## R6. Membership queries

**Decision**: `ZoneRepo` methods —
- `Create(ownerID, name, passwordHash)` → `ErrZoneNameTaken` on `UNIQUE(name)`.
- `GetByName(name)` → zone or nil.
- `Join(zoneID, nodeID)` → `INSERT ... ON CONFLICT DO NOTHING` (idempotent, FR-008).
- `Leave(zoneID, nodeID)` → rows-affected → `ErrNotMember` if 0.
- `MembersByZone(zoneID)` → join `nodes` + `users` → (node name, ip, owner username) (FR-015).
- `ListForUser(userID)` → zones where the user owns OR has a member node, with `is_owner` (FR-014).
- `IsParticipant(zoneID, userID)` → owns or has a member node (members-view gate).
- `AllForRebuild()` → every zone id + its member ips (startup nft rebuild).
- `ZonesForNode(nodeID)` → zone ids (node-delete cleanup).
And `NodeRepo.GetOwned(userID, nodeID)` → node (ip, pubkey) or `ErrNodeNotFound`
(used by join/leave/delete).

**Rationale**: thin, purpose-built queries; the `UNIQUE`/PK constraints are the
authority (zone name unique; `(zone_id,node_id)` PK makes join idempotent via
`ON CONFLICT DO NOTHING`).

---

## R7. Testing approach (constitution II)

**Decision**: three tiers.
- **Unprivileged (real SQLite)**: zone CRUD, join/leave idempotency, list/members,
  participant authz, no-enumeration, and the node-delete-clears-memberships logic.
- **Privileged (real kernel nftables, under root / `unshare -rUn`)**: after each
  operation and after a restart, assert the kernel ruleset — `zone_<id>` set exists
  with exactly the member elements, and the same-zone accept rule is present; cross-zone
  membership puts an address in the right set only. This is the **real enforcement
  state**, not a mock.
- **Manual (quickstart)**: a literal two-client setup (two WireGuard clients peered to
  the relay) verifying same-zone ping succeeds and cross-zone ping is dropped (SC-001) —
  documented for heavy-CI / operator verification, since a multi-client topology is
  impractical to stand up in unit CI.

**Why this satisfies Principle II**: we never mock nftables — automated tests run
real set/element/rule operations on the real kernel and assert the resulting state,
which *is* the mechanism that enforces isolation. The only thing deferred to manual
verification is simulating actual cross-client packets, which is a topology limitation,
not a mocked boundary. Documented in plan Complexity Tracking; CI must run the
privileged tier.

---

## R8. What is deliberately deferred (scope guard)

- **No** owner-only management (change password / kick member / delete zone) — feature 006.
- **No** full user-deletion cascade (peer/set cleanup on user delete) — feature 008;
  this feature only handles the node-delete path that exists in feature 004.
- **No** zone-to-zone peering or transitive reachability — by design (per shared zone).
- **No** IPv6 — IPv4 only.
- **No** client UI — features 009–011.
