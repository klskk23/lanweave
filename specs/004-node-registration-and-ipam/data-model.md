# Phase 1 Data Model: Node Registration and IPAM

Introduces one new table (`nodes`) and reuses `users` (001). The WireGuard peer set
and the assigned addresses are derived from this table.

---

## Entity: Node

### Table `nodes`

| Column       | Type    | Constraints                                   | Notes |
|--------------|---------|-----------------------------------------------|-------|
| `id`         | INTEGER | PRIMARY KEY AUTOINCREMENT                      | |
| `user_id`    | INTEGER | NOT NULL, REFERENCES users(id) ON DELETE CASCADE | Owner. CASCADE prepares for feature 008. |
| `name`       | TEXT    | NOT NULL                                       | ≤ 64 chars, non-empty. |
| `wg_pubkey`  | TEXT    | NOT NULL, UNIQUE                               | Client WireGuard public key (base64). Relay-wide unique (FR-007). |
| `ip`         | INTEGER | NOT NULL, UNIQUE                               | Assigned pool address as uint32 (R1). Unique (FR-016). |
| `created_at` | TEXT    | NOT NULL                                       | RFC3339 UTC. |
| | | **UNIQUE(user_id, name)** | Per-user name uniqueness (FR-006). |

**Indexes**: `UNIQUE(wg_pubkey)`, `UNIQUE(ip)`, `UNIQUE(user_id, name)`. A
`user_id` lookup index is covered by the composite unique index prefix.

### Validation rules

| Rule | Source | Enforced where |
|------|--------|----------------|
| name non-empty, ≤ 64 | FR-005 | handler |
| wg_pubkey is a valid WireGuard key | FR-005 | handler (`wgtypes.ParseKey`) |
| wg_pubkey relay-wide unique | FR-007 | `UNIQUE(wg_pubkey)` → `ErrPubKeyTaken` (409) |
| name unique per user | FR-006 | `UNIQUE(user_id,name)` → `ErrNodeNameTaken` (409) |
| ip unique (concurrency) | FR-016 | `UNIQUE(ip)` → retry (R2) |
| ip is lowest free in pool | FR-014 | allocation query (R2) |

### Lifecycle

```
        register (alloc lowest-free + INSERT, then AddPeer)
 (none) ───────────────────────────────────────────────────▶ active ──────▶ (deleted)
        peer-add fails → row removed, nothing persists (FR-004)   DELETE row + RemovePeer;
                                                                  address freed (FR-012/015)
```

### Repository surface (`internal/server/store/nodes.go`)

```go
type Node struct {
    ID        int64
    UserID    int64
    Name      string
    PubKey    string
    IP        netip.Addr   // converted from the stored uint32
    CreatedAt time.Time
}

var (
    ErrNodeNameTaken = errors.New("node name already exists for this user")
    ErrPubKeyTaken   = errors.New("public key already registered")
    ErrPoolExhausted = errors.New("no addresses available in the pool")
    ErrNodeNotFound  = errors.New("node not found")
)

func (s *Store) Nodes() *NodeRepo

// Create allocates the lowest free address (retry-on-ip-conflict) and inserts the
// node. Returns ErrNodeNameTaken / ErrPubKeyTaken / ErrPoolExhausted as applicable.
func (r *NodeRepo) Create(ctx, userID int64, name, pubKey string, firstClient, lastClient uint32) (*Node, error)

func (r *NodeRepo) ListByUser(ctx, userID int64) ([]Node, error)

// DeleteOwned removes the node only if owned by userID. Returns the deleted node's
// pubkey (so the caller can remove the peer) or ErrNodeNotFound.
func (r *NodeRepo) DeleteOwned(ctx, userID, nodeID int64) (pubKey string, err error)

// AllForPeers returns every node (pubkey + ip) for the startup peer rebuild.
func (r *NodeRepo) AllForPeers(ctx) ([]Node, error)
```

---

## Entity: Address pool (derived, not stored)

- Configured by `wireguard.network`. The server holds `base+1` (feature 003). Clients
  occupy `[base+2, broadcast-1]`.
- `ipam.PoolRange(cidr) -> (firstClient, lastClient uint32)`; `Uint32ToAddr` /
  `AddrToUint32` convert for storage and presentation. Pure, unit-tested.

---

## Entity: Tunnel peer (derived kernel state, not stored)

| Field | Source |
|-------|--------|
| public key | `nodes.wg_pubkey` |
| allowed IP | `nodes.ip` as a single `/32` |

Maintained on register (add) / delete (remove) and fully rebuilt from `nodes` at
startup (`ReplacePeers`) — the table is authoritative (FR-018).

---

## Entity: Server connection info (computed, not stored)

| Field | Source |
|-------|--------|
| `public_key` | server WG key (feature 003, `wg.Server.PublicKey`) |
| `endpoint` | `wireguard.endpoint` config (R6) |
| `network` | `wireguard.network` config |
| `mtu` | `wireguard.mtu` config |

---

## Migration: `0003_nodes.sql`

```sql
-- +goose Up
CREATE TABLE nodes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    wg_pubkey  TEXT    NOT NULL UNIQUE,
    ip         INTEGER NOT NULL UNIQUE,
    created_at TEXT    NOT NULL,
    UNIQUE (user_id, name)
);

-- +goose Down
DROP TABLE nodes;
```

- `ON DELETE CASCADE` on `user_id` means deleting a user removes their nodes at the
  DB layer; feature 008 will add the matching peer/address cleanup. (This feature does
  not expose user deletion.)

---

## Relationship view (after this feature)

```
users (001) 1 ──< nodes (004)        nodes.ip drives  ──► WireGuard peers (rebuilt at startup)
                       │
                       └── (feature 005) nodes ──< zone_members >── zones
```

`zones` / `zone_members` arrive in feature 005, which references `nodes`.
