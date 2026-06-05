# Phase 0 Research: Node Registration and IPAM

Decisions constrained by DESIGN.md §3/§4/§6/§8/§9, the constitution, and the
001–003 codebase. No `NEEDS CLARIFICATION` remains. No new dependency.

---

## R1. IP address storage — INTEGER (uint32)

**Decision**: store `nodes.ip` as an INTEGER holding the IPv4 address as a uint32.

**Rationale**:
- "Lowest free address" requires correct numeric ordering. TEXT dotted form sorts
  lexically wrong (`100.127.0.10` < `100.127.0.9`). A uint32 gives exact MIN/gap math.
- Conversions are trivial: `netip.Addr.As4()` ↔ `binary.BigEndian.Uint32`.
- Presented to users in dotted form in API responses (DESIGN, Assumptions).

**Alternatives**: TEXT dotted (wrong ordering, app-side sort needed) — rejected.

---

## R2. Lowest-free allocation + concurrency safety

**Decision**: compute the lowest free address with a candidate-set SQL query, INSERT
with a `UNIQUE(ip)` constraint, and **retry on an ip-uniqueness conflict** (a small
bounded loop). Name/pubkey conflicts do NOT retry — they are user errors (409).

**The query** (start = first client uint32, end = last client uint32):
```sql
SELECT c FROM (
    SELECT :start AS c
    UNION ALL
    SELECT ip + 1 FROM nodes WHERE ip >= :start AND ip < :end
) WHERE c NOT IN (SELECT ip FROM nodes) AND c <= :end
ORDER BY c LIMIT 1;
```
The candidate set is `{start} ∪ {used+1}`; the smallest candidate not already used
and within range is the lowest free address. No free address → no row → pool exhausted.

**Concurrency (FR-016)**: two registrations may compute the same lowest-free value;
the `UNIQUE(ip)` constraint lets exactly one INSERT win, the other gets a
`UNIQUE constraint failed: nodes.ip` error and **retries** (recomputes the now-next
lowest-free). This avoids any dependence on a driver-specific `BEGIN IMMEDIATE`
(the `_txlock` uncertainty flagged in feature 003's analyze) — the UNIQUE index is
the authority. A retry cap (e.g. 100) bounds the loop; exceeding it surfaces an error.

**Why not a long-held transaction with SELECT-then-INSERT**: that reintroduces the
read-then-write lock-upgrade contention; retry-on-conflict is simpler and robust.

**Distinguishing conflicts**: SQLite's error names the constraint
(`nodes.ip` / `nodes.wg_pubkey` / `nodes.user_id, nodes.name`); the repo inspects the
message to decide retry (ip) vs typed error (`ErrPubKeyTaken`, `ErrNodeNameTaken`).

---

## R3. Pool bounds

**Decision**: `ipam.PoolRange(cidr)` returns `(firstClient, lastClient uint32)` where
`firstClient = networkBase + 2` (skip `.0` network and `.1` server) and
`lastClient = broadcast - 1` (skip the broadcast address). IPv4 only.

**Rationale**: DESIGN §3.2 — server holds the first usable (`.1`); clients ascend from
`.2`. Reserving the broadcast address keeps the range standards-correct. Pure and
fully unit-testable. A range with no client slot (e.g. `/30` after reserving
server+broadcast leaves few; `/31`,`/32`) yields an empty/invalid range → caller
treats allocation as exhausted/misconfigured.

---

## R4. WireGuard peer model

**Decision**: each node is a WG peer with `PublicKey = node.pubkey` and a single
`AllowedIPs = node.ip/32`. Add via `wgctrl ConfigureDevice` with `ReplacePeers:false`
and one `PeerConfig{PublicKey, ReplaceAllowedIPs:true, AllowedIPs:[ip/32]}`. Remove via
`PeerConfig{PublicKey, Remove:true}`. Startup rebuild via `ReplacePeers:true` with all
nodes (FR-018).

**Rationale**: hub-and-spoke — the relay must know which peer owns each pool address,
so the /32 allowed-ip per peer is exactly the routing entry. No endpoint or keepalive
is set server-side (clients dial the server; the server never initiates).

**Pubkey validation (FR-005)**: parse with `wgtypes.ParseKey` at the handler boundary;
a parse failure is a `validation_error` (400) with no allocation.

---

## R5. DB ↔ tunnel consistency

**Decision**:
- **Register**: allocate+INSERT the node (DB), then `AddPeer`. If `AddPeer` fails,
  delete the node row (compensate) and return an error — nothing persists (FR-004).
- **Delete**: `DELETE` the owned node (DB is authoritative), then `RemovePeer`
  best-effort (log on failure). The address is freed by the row deletion.
- **Startup**: `ReplacePeers` from the full nodes table, so any crash gap (node row
  without a peer, or a stale peer) is reconciled to match the DB (FR-018).

**Rationale**: the DB is the single source of truth (constitution I; DESIGN §6.3).
Immediate best-effort peer sync gives correct live behavior; the startup rebuild is
the backstop that makes the system self-healing and lets nodes survive restarts
(SC-007), building on feature 003's interface preservation.

---

## R6. Server tunnel endpoint config

**Decision**: add `wireguard.endpoint` to config — the publicly reachable `host:port`
clients dial over UDP (may differ from the API address and from `listen_port` under
NAT). Validated as a non-empty `host:port`. `GET /server` returns it verbatim along
with the server public key, `network`, and `mtu`.

**Rationale**: the relay cannot reliably infer its own public endpoint; the operator
declares it (FR-009, DESIGN §9.3). Returning it verbatim supports NAT port-forwarding
(external port ≠ bind port). The example config and the feature-001/003 test config
helpers are updated to include it (a small additive change).

**Alternatives**: derive from the request Host header — rejected (that is the API host,
often not the UDP endpoint, and may be an internal address).

---

## R7. Endpoints, ownership, and routing

**Decision**: all under `AuthRequired` (any authenticated user, not admin-only):
- `POST /api/v1/nodes` `{name, wg_pubkey}` → 201 `{id, name, ip}`
- `GET /api/v1/nodes` → caller's nodes only
- `DELETE /api/v1/nodes/{id}` → owner-only; `DELETE ... WHERE id=? AND user_id=?`,
  RowsAffected 0 → 404 (covers both "not found" and "not yours" with no enumeration)
- `GET /api/v1/server` → server connection info

**Rationale**: registering/managing nodes is a normal authenticated-user action.
Ownership scoping on every node query (`WHERE user_id = <caller>`) enforces FR-011/013.
The stdlib mux path value (`r.PathValue("id")`) carries the node id.

---

## R8. Node name & pubkey uniqueness (schema-enforced)

**Decision**: `UNIQUE(user_id, name)` (per-user name, FR-006) and `UNIQUE(wg_pubkey)`
(relay-wide key, FR-007) and `UNIQUE(ip)` (FR-016) as table constraints; the handler
maps each violation to its typed error/HTTP code. Name validated ≤ 64 chars, non-empty
(matches username limit).

**Rationale**: constraints are the authoritative guard (same pattern as 002's
username uniqueness and the ip-conflict retry); the app interprets which constraint
tripped.

---

## R9. What is deliberately deferred (scope guard)

- **No** zone membership or node-to-node reachability — feature 005 (the default-deny
  forward chain from 003 still blocks node-to-node; a new node reaches only the relay).
- **No** cascade deletion of a user's nodes on user delete — feature 008.
- **No** online/last-handshake status — feature 007.
- **No** client UI — features 009–011.
- **No** IPv6 pool — IPv4 only (DESIGN).
