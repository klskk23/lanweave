# Data Model: Windows Client Main Panel

**Feature**: 011-windows-client-main-panel | **Date**: 2026-06-06

No server *schema* change. There is **one minimal additive server DTO change** (resolves
analyze M1): the zone-members response gains each member's `node_id` so the panel can remove
(kick) a member by id — the members DTO previously exposed only name/owner/ip. Otherwise the
panel reuses the existing server DTOs and adds in-memory view models plus one cached secret
(the session token). No new persisted local file.

## Server DTO change (additive)

| DTO / query / handler | Change |
|-----------------------|--------|
| `protocol.ZoneMemberResponse` | add `node_id` (`json:"node_id"`) |
| `store.ZoneMember` + `MembersByZone` | add `NodeID`; select `n.id` |
| `zoneMembers` handler | map `node_id` into the response |

This is purely additive (existing clients ignore the new field) and is covered by updating
the existing members-transparency server tests.

## Reused server DTOs (`pkg/protocol`, unchanged)

| DTO | Used for |
|-----|----------|
| `NodeResponse` (`id, name, ip, online, last_handshake`) | the device list + this-machine status |
| `NodeListResponse` | `GET /nodes` |
| `ZoneResponse` (`id, name, is_owner`) | the zones list + owner gating |
| `ZoneListResponse` | `GET /zones` |
| `CreateZoneRequest` (`name, password`) | create a zone |
| `JoinZoneRequest` (`node_id, password`) | join a zone |
| `LeaveZoneRequest` (`node_id`) | leave a zone |
| `ChangeZonePasswordRequest` (`password`) | owner change-password |
| `ZoneMemberResponse` (`node_name, ip, owner`) | a zone's member row (transparency) |
| `ZoneMembersResponse` | `GET /zones/{name}/members` |
| `MeResponse` (`user_id, username, is_admin`) | validate the session on start |

## View models (in-memory, `panel`)

### DeviceView

| Field | Source | Notes |
|-------|--------|-------|
| Name | `NodeResponse.Name` | |
| IP | `NodeResponse.IP` | rendered `100.127.x.y` |
| Online | `NodeResponse.Online` | refreshed on the timer |
| LastSeen | `NodeResponse.LastHandshake` | local time; "never" when empty |
| IsThisMachine | match vs setup record `node_name`/`ip` | exactly one is true |

### ZoneView

| Field | Source | Notes |
|-------|--------|-------|
| Name | `ZoneResponse.Name` | |
| IsOwner | `ZoneResponse.IsOwner` | gates owner controls |
| Members | `ZoneMembersResponse` (lazy, on expand) | each member's name, owner, address |

### MemberView

| Field | Source |
|-------|--------|
| NodeID | `ZoneMemberResponse.NodeID` (used to kick the member) |
| NodeName | `ZoneMemberResponse.NodeName` |
| Owner | `ZoneMemberResponse.Owner` |
| IP | `ZoneMemberResponse.IP` |

## Session (cached secret)

| Item | Where | Notes |
|------|-------|-------|
| Session token | OS secure store (`keyring`, `SessionTokenName`) | reused while valid; never in a plain file or log |

- **Lifecycle**: load on start → validate (`GET /me`) → use; on 401 → sign-in prompt →
  `Login` → re-cache. The token is the only new secret; everything else is display data.

## Controller errors (typed, from `apiclient`)

| Error | From | Panel meaning |
|-------|------|---------------|
| `ErrSessionExpired` | 401 on an authed call | re-prompt sign-in, then resume |
| `ErrZonePasswordWrong` | join with a wrong password | "Wrong zone password." |
| `ErrZoneNameTaken` | create a duplicate name | "A zone with that name already exists." |
| `ErrNotOwner` | owner op by a non-owner | "Only the zone owner can do that." |
| `ErrNotMember` | leave a zone you're not in | "This device isn't a member of that zone." |
| `ErrZoneNotFound` | unknown zone | "That zone doesn't exist." |
| `ErrUnreachable`/`ErrServer` | transport/5xx | "Couldn't reach the server / something went wrong." |

## Relationships & invariants

- The session token is the only persisted secret this feature adds; it lives solely in the
  secure store (FR-012, DESIGN §8).
- Owner controls are shown only when `IsOwner` is true (FR-008); the server still enforces
  ownership (006), so a stale UI cannot bypass it.
- A member's view of a zone includes every member's name, owner, and address (FR-013) — the
  transparency guarantee from 005.
- After any successful operation the relevant view is re-fetched from the server (FR-014), so
  the panel never shows stale state for actions it performed.
- Exactly one device in the list is this machine (matched against the setup record).
