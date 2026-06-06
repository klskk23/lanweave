# Contract: POST /api/v1/zones (create, with optional auto-join)

Creates a password-protected zone owned by the authenticated caller and installs its
(empty) nftables isolation set + accept rule. When `node_id` is supplied, the caller's
device is also admitted to the zone in the same operation.

## Request

- **Method/Path**: `POST /api/v1/zones`
- **Auth**: Bearer JWT (required). Missing/invalid → `401 unauthorized`.
- **Body** (`application/json`): `CreateZoneRequest`

```json
{ "name": "devteam", "password": "zone-strong-pw", "node_id": 12 }
```

| Field      | Type   | Required | Rules |
|------------|--------|----------|-------|
| `name`     | string | yes      | trimmed, 1–64 chars |
| `password` | string | yes      | ≥ 8 chars |
| `node_id`  | int64  | no       | when present & non-zero, must be a node owned by the caller; `0`/omitted = create-only |

## Responses

| Status | When | Body |
|--------|------|------|
| `201 Created` | Zone created (and, if `node_id` given, caller's device joined) | `ZoneResponse` `{id, name, is_owner:true}` |
| `400 validation_error` | name empty/>64, password <8, or malformed body | error envelope |
| `401 unauthorized` | no/invalid token | error envelope |
| `404 not_found` | `node_id` non-zero but not a node owned by the caller | error envelope; **no zone created** |
| `409 zone_name_taken` | name already exists | error envelope |
| `500` | nft/DB failure during create or join | error envelope; **no partial state** (compensation removes the zone) |

`ZoneResponse` is unchanged — it does not echo membership; the client confirms
membership via `GET /api/v1/zones/{name}/members`.

## Behavioral guarantees

1. **Backward compatible**: a request with no `node_id` (or `node_id: 0`) behaves
   exactly as before — zone created, caller is owner, **no** member.
2. **Ownership-first**: with a non-zero `node_id`, ownership is checked *before* the zone
   is created. A foreign/unknown `node_id` yields `404` and creates nothing.
3. **Atomic with auto-join**: on success the device is both a `zone_members` row and an
   element of the zone's nft set (`zone_<id>`). On any post-create failure the zone is
   removed (DB cascade) and its nft state torn down — never a zone the creator is not in.
4. **No enumeration**: unknown vs. not-owned `node_id` are indistinguishable (both
   `404`), matching `joinZone`'s foreign-node behavior.
5. **No password re-verification for the creator**: the creator's auto-join does not
   re-check the zone password they just set.

## Unchanged sibling contract

`POST /api/v1/zones/{name}/join` (`JoinZoneRequest{node_id, password}`) is unchanged and
remains the way to join a zone you do **not** own (name + password required).
