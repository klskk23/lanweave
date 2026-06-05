# Phase 1 Data Model: Zone Owner Controls

**No new tables.** This feature mutates feature-005 entities and the derived nftables
state. Listed here are the operations and the repository surface they need.

---

## Entity: Zone (reused, feature 005)

| Operation added this feature | Effect |
|------------------------------|--------|
| change password | `UPDATE zones SET password_hash = ? WHERE id = ?` — single row; no membership/rule change (FR-002). |
| delete | `DELETE FROM zones WHERE id = ?` — cascades `zone_members`; releases the unique `name` (FR-009/010). Member `nodes` untouched. |

Authorization uses the existing `owner_user_id`: an operation is allowed only when
`owner_user_id == caller`.

---

## Entity: Zone membership (reused, feature 005)

| Operation | Effect |
|-----------|--------|
| kick | `DELETE FROM zone_members WHERE zone_id = ? AND node_id = ?` (the existing `Leave`); `ErrNotMember` if absent. Cross-user: the membership may belong to another user's node. |
| (delete zone) | all of the zone's `zone_members` rows are removed by the FK cascade. |

The member `nodes` rows are never deleted by these operations.

---

## Derived: nftables isolation state (reused + one new op)

| Operation | nftables effect |
|-----------|-----------------|
| change password | none (membership unchanged). |
| kick | `RemoveMember(zoneID, ip)` — delete the node's address from `zone_<id>` (existing, 005). |
| delete zone | **`DeleteZone(zoneID)`** (new) — delete the accept rule(s) referencing `zone_<id>`, then delete the set. |
| (restart) | `Rebuild` (005) reproduces exactly the current zones/members — deleted zones absent, kicked members absent (FR-013). |

---

## Repository surface

### `internal/server/store/zones.go` (extended)

```go
// UpdatePassword changes only the stored hash (members keep membership).
func (r *ZoneRepo) UpdatePassword(ctx, zoneID int64, passwordHash string) error
// Delete (already exists, feature 005) — DELETE zone; cascades zone_members.
// Leave (already exists, feature 005) — DELETE one membership; ErrNotMember if absent.
// GetByName (already exists) — for the ownership gate.
```

### `internal/server/store/nodes.go` (extended)

```go
// GetByID returns any node by id (unscoped) — used to resolve a kicked member's
// address regardless of who owns it. ErrNodeNotFound if absent.
func (r *NodeRepo) GetByID(ctx, nodeID int64) (*Node, error)
```

### `internal/server/netfw/nftables.go` (extended)

```go
// DeleteZone removes the zone's accept rule(s) and then its set. The rule is deleted
// before the set (the kernel rejects deleting a referenced set).
func (m *Manager) DeleteZone(zoneID int64) error
// RemoveMember (already exists, feature 005) — used by kick.
```

---

## DTO (`pkg/protocol/zone.go`, extended)

```go
type ChangeZonePasswordRequest struct {
    Password string `json:"password"`
}
```

(Responses: change password → 200 no body or the zone; kick/delete → 204. Errors use
the shared envelope.)

---

## Relationship view (unchanged shape; this feature only mutates)

```
users (001) 1──< zones.owner_user_id      ← owner gate for change/kick/delete
users (001) 1──< nodes (004) 1──< zone_members >──1 zones (005)
                         nodes.ip ─► nftables set zone_<id> ─► accept rule
```

No schema migration. Feature 008 later adds user-deletion cascade; this feature only
adds owner-initiated change/kick/delete.
