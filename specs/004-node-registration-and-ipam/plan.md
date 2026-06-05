# Implementation Plan: Node Registration and IPAM

**Branch**: `004-node-registration-and-ipam` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/004-node-registration-and-ipam/spec.md`

## Summary

Authenticated users register named nodes by uploading a WireGuard public key. The
server allocates the lowest free pool address (recycling freed ones, concurrency-safe
via a UNIQUE(ip) constraint with retry), persists the node, and adds it as a
WireGuard peer (pubkey + the node's /32) so the relay routes to it. Users list and
delete their own nodes (delete frees the address and removes the peer). A server-info
endpoint returns the relay's public key, tunnel endpoint, network, and MTU for client
tunnel config. At startup all node peers are rebuilt from the database (DB is the
source of truth), so nodes survive a restart.

## Technical Context

**Language/Version**: Go 1.23+ (existing module `lanweave`)

**Primary Dependencies**: All inherited — `modernc.org/sqlite`, `goose`, `wgctrl`/`wgtypes` (003), `golang-jwt/v5` (002), `golang.org/x/time/rate`, stdlib `net/http`, `net/netip`, `database/sql`. **No new dependency.**

**Storage**: SQLite. New `nodes` table; the IP is stored as an INTEGER (uint32) so "lowest free" ordering and gap-finding are exact and cheap.

**Testing**: Three tiers. **Unit (unprivileged)**: IPAM pool-range + uint32↔addr conversions, public-key validation. **Integration (real SQLite, unprivileged)**: node CRUD, lowest-free allocation, recycle, concurrency (retry-on-conflict), pool exhaustion, uniqueness. **Integration (privileged, real kernel)**: WG peer add/remove and startup peer rebuild, plus the full HTTP register→peer-present flow — run under root / `unshare -rUn`, skip with a clear signal otherwise.

**Target Platform**: Linux, kernel WireGuard + nftables, x86-64.

**Project Type**: Single Go project — extends server packages.

**Performance Goals** (constitution IV): register ≤ 300 ms P50 (alloc query + insert + one wgctrl call); list/server-info ≤ 100 ms; 50 concurrent registrations all get distinct addresses (SC-005).

**Constraints**:
- Lowest-free allocation, immediate recycle, no duplicate address even under concurrency (FR-014/015/016).
- DB + tunnel stay consistent: peer-add failure rolls back the node (FR-004); DB is authoritative and peers rebuilt at startup (FR-018).
- Public key unique relay-wide; node name unique per user (FR-006/007).
- Server never handles a client private key (FR-019).
- Delete reveals nothing about others' nodes (404, FR-013).

**Scale/Scope**: Single instance, pool up to /16 (~65k client addresses). This feature: 1 new table, 1 new pure package (`ipam`), 4 new endpoints, peer methods on `wg.Server`, 1 new config field. No new dependency.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Applies? | How this plan honors it |
|-----------|----------|-------------------------|
| **I. Code Quality** | Yes | Pure IPAM helpers isolated in `ipam` (CIDR→range, uint32↔addr); allocation SQL in `store` (owns the DB); peer ops on `wg.Server` (owns the device); HTTP in `api`. DB is the single source of truth; peers are derived and rebuilt from it. No premature abstraction — allocation uses the UNIQUE constraint as the concurrency authority rather than a bespoke lock manager. Errors are typed values (`ErrNodeNameTaken`, `ErrPubKeyTaken`, `ErrPoolExhausted`, `ErrNodeNotFound`). |
| **II. Testing Standards (NON-NEGOTIABLE)** | Yes (privilege gap documented) | SQLite is **not** mocked: allocation/recycle/concurrency/exhaustion/uniqueness run on a real temp DB. WireGuard is **not** mocked: peer add/remove and startup rebuild run on the real kernel under `unshare -rUn`, skipping (not faking) when unprivileged. Each US has an acceptance test; the 50-way concurrency (SC-005) and restart-rebuild (SC-007) are explicit. CI must run the privileged tier (carried over from 003). |
| **III. UX Consistency** | **N/A** | No end-user UI (the Windows client is features 009–011). The machine-facing surface — node/server-info JSON and stable error codes (`validation_error`, `node_name_taken`, `pubkey_taken`, `pool_exhausted`, `not_found`, `unauthorized`) — stays uniform for the future client. |
| **IV. Performance Requirements** | Yes | Register is one bounded alloc query + insert + a single wgctrl call. The lowest-free query is O(used) via a candidate set, fine at /16 scale. Budgets smoke-checked in quickstart; SC-005 concurrency verified. |
| **Security & Operational Discipline** | Yes | Public-key format validated at the boundary; the server never accepts or stores a private key (FR-019). Ownership enforced on list/delete with no enumeration (FR-013 → 404). Auth + global rate limit reused. Body size capped (002's decode helper). |

**Result**: PASS. The privileged-test gap is the same documented environmental limitation as feature 003 (real tests, skipped when unprivileged), recorded in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/004-node-registration-and-ipam/
├── plan.md
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/
│   ├── nodes.md         # POST/GET/DELETE /api/v1/nodes
│   └── server-info.md   # GET /api/v1/server
├── checklists/requirements.md
└── tasks.md             # /speckit-tasks (later)
```

### Source Code (repository root) — new and changed

```text
internal/server/
├── ipam/
│   ├── ipam.go           # NEW: PoolRange(cidr)->(firstClient,lastClient uint32); Uint32ToAddr; AddrToUint32
│   └── ipam_test.go      # NEW (unprivileged): ranges for /16,/24,/30; conversions; round-trip
├── store/
│   ├── migrations/0003_nodes.sql   # NEW: nodes table (ip INTEGER UNIQUE; (user_id,name) UNIQUE; wg_pubkey UNIQUE)
│   ├── nodes.go          # NEW: NodeRepo — Create (lowest-free + retry-on-ip-conflict), ListByUser, GetOwned, DeleteOwned, AllForPeers
│   └── nodes_test.go     # NEW (real temp DB): alloc ascends, recycle lowest, concurrency distinct, exhaustion, name/pubkey conflict
├── wg/
│   ├── iface.go          # CHANGED: Server.AddPeer(pub,ip), RemovePeer(pub), ReplacePeers([]PeerConfig)
│   └── peers_test.go     # NEW (privileged): add/remove/replace real peers; pubkey+allowed-ip correct
├── api/
│   ├── node_handlers.go  # NEW: registerNode, listNodes, deleteNode
│   ├── server_handler.go # NEW: GET /server
│   ├── node_handlers_test.go # NEW: acceptance (register→list→delete, server-info, authz, conflicts) — peer checks privileged
│   └── router.go         # CHANGED: Options +WG *wg.Server +WGConfig; routes under AuthRequired
├── app/
│   ├── app.go            # CHANGED: pass WG + WGConfig into router
│   └── dataplane.go      # CHANGED: after setupDataPlane, rebuildNodePeers(ctx, store, wgServer)
└── config/
    └── config.go         # CHANGED: add WireGuard.Endpoint (public host:port); validate non-empty host:port

pkg/protocol/
└── node.go               # NEW: RegisterNodeRequest, NodeResponse, NodeListResponse, ServerInfoResponse
```

**Structure Decision**: Continue the single-project layout. A new pure `ipam` package
holds the only genuinely reusable, privilege-free logic (pool math + conversions),
kept separate so it is exhaustively unit-tested. The allocation query lives in
`store` because it must run inside the database against the UNIQUE(ip) constraint,
which is the concurrency authority. Peer management is added to the existing
`wg.Server` (it owns the wgctrl handle). The startup peer rebuild (FR-018) extends
the feature-003 data-plane wiring in `app/dataplane.go`. One new config field
(`wireguard.endpoint`) is required for the server-info response; the example config
and the feature-001/003 test config helpers are updated to include it.

## Complexity Tracking

> One documented environmental limitation, not a constitution violation.

| Item | Why | Mitigation / Note |
|------|-----|-------------------|
| Privileged tests skip when unprivileged | Real WG peer add/remove + startup rebuild need `CAP_NET_ADMIN`. | Tests are real, not mocked; run under root / `unshare -rUn` (verified usable in this env in feature 003). SQLite/IPAM logic fully covered unprivileged. CI must run the privileged tier. |
