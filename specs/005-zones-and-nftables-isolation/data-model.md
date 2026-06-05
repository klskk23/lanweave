# Phase 1 Data Model: Zones and nftables Isolation

Introduces `zones` and `zone_members`, reuses `users` (001) and `nodes` (004). The
nftables sets/rules are derived from `zone_members` and rebuilt from it at startup.

---

## Entity: Zone

### Table `zones`

| Column          | Type    | Constraints                                      | Notes |
|-----------------|---------|--------------------------------------------------|-------|
| `id`            | INTEGER | PRIMARY KEY AUTOINCREMENT                         | Stable handle; nftables set is `zone_<id>`. |
| `name`          | TEXT    | NOT NULL, UNIQUE                                  | Globally unique; the join address (FR-002). |
| `password_hash` | TEXT    | NOT NULL                                          | argon2id (reused). Never logged. |
| `owner_user_id` | INTEGER | NOT NULL, REFERENCES users(id) ON DELETE CASCADE | The creator (FR-001). CASCADE prepares for feature 008. |
| `created_at`    | TEXT    | NOT NULL                                          | RFC3339 UTC. |

### Validation

| Rule | Source | Where |
|------|--------|-------|
| name non-empty, ≤ 64 | FR-003 | handler |
| password ≥ 8 chars | FR-003 | handler |
| name globally unique | FR-002 | `UNIQUE(name)` → `ErrZoneNameTaken` (409) |
| password verified on join | FR-006 | argon2id verify; wrong → generic error |

---

## Entity: Zone membership

### Table `zone_members`

| Column      | Type    | Constraints                                       | Notes |
|-------------|---------|---------------------------------------------------|-------|
| `zone_id`   | INTEGER | NOT NULL, REFERENCES zones(id) ON DELETE CASCADE  | |
| `node_id`   | INTEGER | NOT NULL, REFERENCES nodes(id) ON DELETE CASCADE  | Deleting a node removes its memberships (DB part of FR-018). |
| `joined_at` | TEXT    | NOT NULL                                          | RFC3339 UTC. |
| | | **PRIMARY KEY (zone_id, node_id)** | Idempotent join (`ON CONFLICT DO NOTHING`, FR-008). |

- `ON DELETE CASCADE` on `node_id` removes membership rows when a node is deleted; the
  matching nftables set element is removed by the node-delete handler (FR-018), and the
  startup rebuild reconciles any drift.

### State / lifecycle

```
        join (INSERT membership + nft AddMember)
 (none) ─────────────────────────────────────────▶ member ───────────────▶ (none)
        idempotent: re-join is a no-op             leave: DELETE + nft RemoveMember
                                                    node delete: cascade + nft RemoveMember (per zone)
```

---

## Derived: nftables isolation state (not stored)

| Object | Per | Content |
|--------|-----|---------|
| set `zone_<id>` | zone | member node addresses (from `zone_members` → `nodes.ip`) |
| accept rule | zone | `ip saddr @zone_<id> && ip daddr @zone_<id> accept` in the `forward` chain |
| default policy | table | `drop` (from feature 003) — cross-zone / zone-less denied |

Rebuilt from `zones` + `zone_members` at startup (FR-017); mutated incrementally on
create/join/leave/node-delete.

---

## Repository surface

### `internal/server/store/zones.go` — `ZoneRepo`

```go
type Zone struct {
    ID        int64
    Name      string
    OwnerID   int64
    CreatedAt time.Time
}
type ZoneMember struct {
    NodeID    int64
    NodeName  string
    IP        netip.Addr
    OwnerName string   // owning username (transparency, FR-015)
}
type ZoneState struct {          // for the startup nft rebuild
    ID        int64
    MemberIPs []netip.Addr
}

var (
    ErrZoneNameTaken  = errors.New("zone name already exists")
    ErrZoneOrPassword = errors.New("invalid zone or password")
    ErrNotMember      = errors.New("node is not a member of the zone")
)

func (s *Store) Zones() *ZoneRepo
func (r *ZoneRepo) Create(ctx, ownerID int64, name, passwordHash string) (*Zone, error)
func (r *ZoneRepo) GetByName(ctx, name string) (*Zone, error)         // nil if absent
func (r *ZoneRepo) Join(ctx, zoneID, nodeID int64) error              // idempotent
func (r *ZoneRepo) Leave(ctx, zoneID, nodeID int64) error             // ErrNotMember if absent
func (r *ZoneRepo) MembersByZone(ctx, zoneID int64) ([]ZoneMember, error)
func (r *ZoneRepo) ListForUser(ctx, userID int64) ([]ZoneWithOwnership, error)
func (r *ZoneRepo) IsParticipant(ctx, zoneID, userID int64) (bool, error)
func (r *ZoneRepo) AllForRebuild(ctx) ([]ZoneState, error)
func (r *ZoneRepo) ZonesForNode(ctx, nodeID int64) ([]int64, error)
```

### `internal/server/store/nodes.go` (extended)

```go
// GetOwned returns the node only if owned by userID (ip + pubkey), else ErrNodeNotFound.
func (r *NodeRepo) GetOwned(ctx, userID, nodeID int64) (*Node, error)
```

### `internal/server/netfw/nftables.go` (extended)

```go
type ZoneState struct { ID int64; MemberIPs []netip.Addr }   // or reuse store's via a small adapter
func (m *Manager) Rebuild(zones []ZoneState, log *slog.Logger) error  // full, from DB
func (m *Manager) AddZone(zoneID int64) error                         // empty set + accept rule
func (m *Manager) AddMember(zoneID int64, ip netip.Addr) error        // set add element
func (m *Manager) RemoveMember(zoneID int64, ip netip.Addr) error     // set delete element
```

---

## Migration: `0004_zones.sql`

```sql
-- +goose Up
CREATE TABLE zones (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TEXT    NOT NULL
);
CREATE TABLE zone_members (
    zone_id   INTEGER NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    node_id   INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    joined_at TEXT    NOT NULL,
    PRIMARY KEY (zone_id, node_id)
);

-- +goose Down
DROP TABLE zone_members;
DROP TABLE zones;
```

---

## Relationship view (after this feature)

```
users (001) 1──< zones.owner_user_id
users (001) 1──< nodes (004) 1──< zone_members >──1 zones (005)
                         nodes.ip ─► nftables set zone_<id> ─► accept rule (same-zone)
```

Feature 006 adds owner operations on `zones`; feature 008 adds full user-deletion cascade.
