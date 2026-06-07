# Data Model: per-user-limits

**No database schema change.** This feature adds no tables, columns, or indexes. The
two "allowances" are *derived* at write time from existing row counts; the two caps
are *configuration*, not persisted data. This document records the conceptual entities
and the validation rules that bind them.

## Entities

### Limit configuration (config, loaded once at startup)

New TOML section `[limits]`, decoded into `config.LimitsConfig`.

| Field | TOML key | Type | Semantics |
|-------|----------|------|-----------|
| MaxDevicesPerUser | `max_devices_per_user` | `*int` (three-state) | `nil` (absent) → default **10**; `0` → unlimited; `>0` → ceiling; `<0` → invalid (startup error) |
| MaxOwnedZonesPerUser | `max_owned_zones_per_user` | `*int` (three-state) | same semantics |

**Lifecycle / transitions**:
- `Load()` decodes the TOML (`nil` if the key is absent).
- `applyDefaults()`: `nil → 10`. An explicit `0` is preserved (distinct from `nil`).
- `Validate()`: a resolved value `< 0` appends `"limits.max_… must be >= 0 (0 = unlimited)"`
  to the joined error set → server refuses to start.
- After load+validate, `main` dereferences to plain `int` and hands the two values to
  `api.Options`. Changing a cap requires a restart (no hot reload — consistent with all
  other settings).

### Device allowance (derived, per regular user)

The relationship `currentDeviceCount(user) < effectiveDeviceCap` evaluated atomically
inside `NodeRepo.Create`.

- **currentDeviceCount** = `SELECT COUNT(*) FROM nodes WHERE user_id = ?` (live; no
  stored counter).
- **effectiveDeviceCap** = the value the handler passes: configured cap for a regular
  user, or `0` (unlimited) when the caller is admin.
- **Permitted** iff `effectiveDeviceCap <= 0` (unlimited) **or** `currentDeviceCount <
  effectiveDeviceCap`. Otherwise → `ErrDeviceLimitReached`.
- **Frees a slot on delete**: deleting a node lowers `currentDeviceCount` immediately
  (count is live), so a subsequent create succeeds.

### Owned-zone allowance (derived, per regular user)

The relationship `currentOwnedZoneCount(user) < effectiveZoneCap` evaluated atomically
inside `ZoneRepo.Create`.

- **currentOwnedZoneCount** = `SELECT COUNT(*) FROM zones WHERE owner_user_id = ?`.
  **Only owned (created) zones count.** Zones the user merely *joined* (a member node
  in a zone owned by someone else) are **excluded** and joining is never gated by this
  cap.
- **effectiveZoneCap** = configured cap for a regular user, or `0` when admin.
- **Permitted** iff `effectiveZoneCap <= 0` **or** `currentOwnedZoneCount <
  effectiveZoneCap`. Otherwise → `ErrOwnedZoneLimitReached`.
- **Frees a slot on delete**: deleting an owned zone lowers the count immediately.

## Store API changes (Go signatures)

```go
// new sentinels
var ErrDeviceLimitReached     = errors.New("device limit reached for this user")
var ErrOwnedZoneLimitReached  = errors.New("owned-zone limit reached for this user")

// nodes.go — maxDevices <= 0 means unlimited (admin or configured 0)
func (r *NodeRepo) Create(ctx context.Context, userID int64,
    name, pubKey string, first, last uint32, maxDevices int) (*Node, error)

// zones.go — maxOwnedZones <= 0 means unlimited
func (r *ZoneRepo) Create(ctx context.Context, ownerID int64,
    name, passwordHash string, maxOwnedZones int) (*Zone, error)
```

Both methods: when `maxN <= 0`, run the existing unconditional `INSERT`. When
`maxN > 0`, run the conditional `INSERT … SELECT … WHERE (SELECT COUNT(*) …) < ?` and
return the limit error when `RowsAffected() == 0`. UNIQUE-violation handling is
unchanged (it surfaces as an error, not a 0-row result). The `NodeRepo.Create` ip
retry loop is preserved: a `0` rows result returns the limit error immediately (no
retry); only a `nodes.ip` UNIQUE error retries.

## Wiring

```
config.toml [limits]
   └─ config.LimitsConfig (*int, defaulted, validated)
        └─ main: deref → api.Options{ MaxDevicesPerUser int, MaxOwnedZonesPerUser int }
             └─ handlers{ maxDevicesPerUser, maxOwnedZonesPerUser int }
                  ├─ registerNode: effective = admin?0:maxDevicesPerUser → Nodes().Create(..., effective)
                  └─ createZone:   effective = admin?0:maxOwnedZonesPerUser → Zones().Create(..., effective)
```

## Validation rules (summary)

| Rule | Where enforced | On failure |
|------|----------------|------------|
| cap value `>= 0` | `config.Validate()` (startup) | server refuses to start, reports field |
| device count `<` cap (regular user) | `NodeRepo.Create` conditional insert | `ErrDeviceLimitReached` → 409 `device_limit_reached` |
| owned-zone count `<` cap (regular user) | `ZoneRepo.Create` conditional insert | `ErrOwnedZoneLimitReached` → 409 `zone_limit_reached` |
| admin / `0` → no check | handler passes `0`; store early-out | always permitted |
| joining another's zone uncapped | not gated (join path untouched) | n/a |
