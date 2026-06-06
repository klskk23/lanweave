# Data Model: Zone Create Auto-Join

No schema migration. All tables already exist (feature 004 `nodes`, feature 005
`zones` / `zone_members`). The only new data is one optional **request** field.

## Changed: CreateZoneRequest (wire contract)

`pkg/protocol/zone.go`

| Field      | Type     | JSON        | Required | Meaning |
|------------|----------|-------------|----------|---------|
| Name       | string   | `name`      | yes      | Zone name, 1–64 chars (unchanged) |
| Password   | string   | `password`  | yes      | Zone password, ≥ 8 chars (unchanged) |
| **NodeID** | int64    | `node_id`   | **no**   | Caller's device to auto-join. `0`/omitted = create-only (legacy). |

`NodeID` uses `json:"node_id,omitempty"` so existing clients that send no field, and
the value `0`, are wire-identical to today.

## Reused entities (unchanged structure)

- **Zone** (`zones`): `id, name, password_hash, owner_user_id, created_at`. Owner is the
  creating user. Created via `ZoneRepo.Create`; removed (cascade) via `ZoneRepo.Delete`.
- **Membership** (`zone_members`): `(zone_id, node_id, joined_at)`, unique on
  `(zone_id, node_id)`. Inserted via `ZoneRepo.Join` (idempotent `ON CONFLICT DO
  NOTHING`); cascade-deleted when the zone is deleted.
- **Device / Node** (`nodes`): `id, user_id, name, pubkey, ip, created_at`. Ownership
  resolved via `NodeRepo.GetOwned(userID, nodeID)` → `*Node` or `ErrNodeNotFound`. The
  `ip netip.Addr` is what is added to the zone's nft set.

## Derivative state (nftables — reconstructible from SQLite)

- **Zone set** `zone_<id>`: created by `netfw.AddZone(zoneID)`; destroyed by
  `netfw.DeleteZone(zoneID)`.
- **Set element**: the device IP, added by `netfw.AddMember(zoneID, ip)`; removed by
  `netfw.RemoveMember` or implicitly when the set is destroyed. Membership presence in
  this set is what makes a device traffic-eligible within the zone.

## State transition — createZone with NodeID (atomic outcome)

```
validate name/password
   │
   ├─ NodeID != 0 ──► GetOwned(userID, NodeID)
   │                     └─ ErrNodeNotFound ──► 404, NO zone created ───► END
   │
hash password
Zones().Create ─────────► zone row exists
   │
netfw.AddZone(zone.ID)
   └─ fail ──► Zones().Delete(zone.ID) ──► 500 ──► END   (no nft to tear down)
   │
   ├─ NodeID == 0 ──► 201 {zone, is_owner:true}, no member ───────────► END (legacy)
   │
   └─ NodeID != 0:
        Zones().Join(zone.ID, node.ID)
           └─ fail ──► rollback(zone.ID) ──► 500 ──► END
        netfw.AddMember(zone.ID, node.IP)
           └─ fail ──► rollback(zone.ID) ──► 500 ──► END
        201 {zone, is_owner:true}; node is a member; nft set has node.IP ─► END

rollback(zoneID) = Zones().Delete(zoneID) [DB cascade]  +  netfw.DeleteZone(zoneID) [best-effort]
```

**Invariant (NodeID != 0)**: terminal state is either `{zone + membership + nft set
element}` all present, or none present.
