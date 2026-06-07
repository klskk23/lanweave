# API Contract Changes: per-user-limits

This feature adds **no new endpoints**. It adds two new refusal responses to two
existing authenticated endpoints, and a new optional config section. Request shapes
and success responses are unchanged.

## Error envelope (unchanged shape)

All errors use the existing envelope written by `protocol.WriteJSONError`:

```json
{ "error": "<snake_code>", "message": "<human sentence>" }
```

## `POST /api/v1/nodes` — register device

Unchanged request (`{ "name": ..., "wg_pubkey": ... }`) and success
(`201 Created`, `NodeResponse`). **New refusal:**

| Condition | Status | `error` code | Notes |
|-----------|--------|--------------|-------|
| Regular user already owns ≥ `max_devices_per_user` devices | **409 Conflict** | `device_limit_reached` | No node created. Admin caller is exempt (never returned). Distinct from `node_name_taken` (409), `pubkey_taken` (409), `pool_exhausted` (503). |

Precedence: request validation (400) → `device_limit_reached` (409) is decided by the
store's conditional insert. (Name/pubkey UNIQUE conflicts surface as their own errors;
a user at the cap is refused with `device_limit_reached` before address allocation
matters for the count check.)

Example refusal body:

```json
{ "error": "device_limit_reached", "message": "You have reached your device limit." }
```

## `POST /api/v1/zones` — create zone

Unchanged request (`{ "name": ..., "password": ..., "node_id"?: ... }`) and success
(`201 Created`, `ZoneResponse`). **New refusal:**

| Condition | Status | `error` code | Notes |
|-----------|--------|--------------|-------|
| Regular user already owns ≥ `max_owned_zones_per_user` zones | **409 Conflict** | `zone_limit_reached` | No zone created. Admin caller is exempt. Distinct from `zone_name_taken` (409). |

Note: only **owned** zones count. `POST /api/v1/zones/{name}/join` is **not** gated by
any cap and is unchanged — a user at their owned-zone cap can still join others' zones.

When a capped user submits an already-taken name, the store evaluates the count first,
so the response is `zone_limit_reached` (not `zone_name_taken`). Both are 409 and
distinguishable by code.

Example refusal body:

```json
{ "error": "zone_limit_reached", "message": "You have reached your zone limit." }
```

## Client typed-error mapping (`internal/client/apiclient`)

`mapError` gains two arms:

| Server `error` code | Client sentinel |
|---------------------|-----------------|
| `device_limit_reached` | `apiclient.ErrDeviceLimitReached` |
| `zone_limit_reached` | `apiclient.ErrOwnedZoneLimitReached` |

UI mapping:
- Wizard (device setup): `errors.Is(err, ErrDeviceLimitReached)` → `i18n.T("wizard.errDeviceLimit")`
- Panel (zone create): `errors.Is(err, ErrOwnedZoneLimitReached)` → `i18n.T("panel.errZoneLimit")`

## Configuration contract (`config.toml`)

New optional section. Absent → both caps default to 10.

```toml
[limits]
max_devices_per_user     = 10   # unset → 10; 0 → unlimited; negative → startup error
max_owned_zones_per_user = 10   # unset → 10; 0 → unlimited; negative → startup error
```

Startup behavior:
- Section or key absent → cap = 10.
- Value `0` → unlimited for that resource.
- Value `< 0` → `Validate()` error; server refuses to start and reports the field.
