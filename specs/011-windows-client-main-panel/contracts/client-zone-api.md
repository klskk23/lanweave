# Contract: Client → Server zone/node API usage

**Feature**: 011-windows-client-main-panel | **Date**: 2026-06-06

No new server endpoints. The panel consumes endpoints from features 004/005/006/007. This
pins which calls the `apiclient` adds and how responses map to panel behavior. All calls are
authenticated with the session bearer token.

## Calls

| Operation | Request | Success | Notable failures → typed error |
|-----------|---------|---------|-------------------------------|
| Validate session | `GET /api/v1/me` | `200 MeResponse` | `401` → `ErrSessionExpired` |
| List devices | `GET /api/v1/nodes` | `200 NodeListResponse` (with `online`, `last_handshake`) | `401` → `ErrSessionExpired` |
| List zones | `GET /api/v1/zones` | `200 ZoneListResponse` (with `is_owner`) | `401` → `ErrSessionExpired` |
| Zone members | `GET /api/v1/zones/{name}/members` | `200 ZoneMembersResponse` (each member now carries `node_id` — additive server change, M1) | `404` → `ErrZoneNotFound` |
| Create zone | `POST /api/v1/zones` `CreateZoneRequest` | `201 ZoneResponse` | `409 zone_name_taken` → `ErrZoneNameTaken` |
| Join zone | `POST /api/v1/zones/{name}/join` `JoinZoneRequest` | `200`/`204` | wrong password → `ErrZonePasswordWrong`; `404` → `ErrZoneNotFound` |
| Leave zone | `POST /api/v1/zones/{name}/leave` `LeaveZoneRequest` | `200`/`204` | not a member → `ErrNotMember` |
| Change password | `PATCH /api/v1/zones/{name}` `ChangeZonePasswordRequest` | `200`/`204` | `403` → `ErrNotOwner` |
| Kick member | `DELETE /api/v1/zones/{name}/members/{node_id}` | `204` | `403` → `ErrNotOwner`; `404` → `ErrNotMember` |
| Delete zone | `DELETE /api/v1/zones/{name}` | `204` | `403` → `ErrNotOwner` |

Confirmed against the 005/006 handlers: a wrong zone password and an unknown zone are
**merged** into `403 invalid_zone_or_password` (no enumeration) → `ErrZoneOrPassword`; owner
ops by a non-owner return `403 forbidden` → `ErrNotOwner`; a duplicate create is `409
zone_name_taken`.

**Server change (M1, additive)**: `ZoneMemberResponse` gains `node_id` so the panel can kick a
member by id (the kick endpoint is `DELETE /zones/{name}/members/{node_id}`). This is the only
server-side change in 011.

## Reused DTOs (`pkg/protocol`, unchanged)

`MeResponse`, `NodeResponse`, `NodeListResponse`, `ZoneResponse`, `ZoneListResponse`,
`CreateZoneRequest`, `JoinZoneRequest`, `LeaveZoneRequest`, `ChangeZonePasswordRequest`,
`ZoneMemberResponse`, `ZoneMembersResponse`, `ErrorResponse`.

## Transport

- HTTPS with the cached session bearer token; the same `apiclient` (009) verify-on TLS rules
  apply (system trust by default; `--insecure`/`RootCAs` only for troubleshooting/tests).
- Any `401` on an authenticated call surfaces as `ErrSessionExpired`, which the panel turns
  into a sign-in prompt, then resumes.

## Out of scope

- No server changes; no new endpoints; no WebSocket push (v1.1).
