# Contract: Panel controller (Fyne-free)

**Feature**: 011-windows-client-main-panel | **Date**: 2026-06-06

The host-agnostic controller the Fyne view binds to. It assembles view data and performs
operations against an authenticated API client. This is the unit/integration-tested surface;
the Fyne view is a thin renderer.

## Construction & session

| Operation | Contract |
|-----------|----------|
| New(api, record, keyring) | build a controller bound to an apiclient, the setup record (for "this machine"), and the secure store (for the session token) |
| LoadSession() | read the cached token, set it on the apiclient, validate via `GET /me`; returns whether a sign-in is needed |
| SignIn(username, password) | `Login`, cache the new token in the secure store, mark the session valid |

## Read (view assembly)

| Operation | Returns | Notes |
|-----------|---------|-------|
| Devices() | `[]DeviceView` | from `GET /nodes`; exactly one marked `IsThisMachine` (matched vs the record); each with address + online + last-seen |
| Zones() | `[]ZoneView` | from `GET /zones`; each carries `IsOwner` |
| Members(zoneName) | `[]MemberView` | from `GET /zones/{name}/members`; every member's node_id, name, owner, address (node_id is the additive M1 field used to kick) |

## Operations

| Operation | Effect | Errors |
|-----------|--------|--------|
| CreateZone(name, password) | create; caller becomes owner | `ErrZoneNameTaken` |
| JoinZone(name, password) | join with this machine's device (id resolved from `ListNodes` by node name) | `ErrZoneOrPassword` |
| LeaveZone(name) | leave with this machine's device | `ErrNotMember` |
| ChangePassword(name, password) | owner-only | `ErrNotOwner` |
| KickMember(name, nodeID) | owner-only; nodeID comes from a `MemberView` (the M1 field) | `ErrNotOwner`, `ErrNotMember` |
| DeleteZone(name) | owner-only | `ErrNotOwner` |

## Guarantees (from spec)

- **This-machine marking**: exactly one device is `IsThisMachine` (FR-002).
- **Owner gating**: owner operations are only meaningful when `IsOwner` is true; the view
  hides the controls otherwise, and the server enforces it regardless (FR-008).
- **Transparency**: `Members` returns every member's name/owner/address (FR-013).
- **Session**: a valid cached token is reused; `ErrSessionExpired` (401) signals the view to
  prompt a sign-in and resume (FR-012).
- **Consistency**: each operation returns only after the server confirms; the view re-fetches
  the affected list afterwards (FR-014).
- **Errors**: every failure is a typed error the view maps to a human-readable message
  (FR-011); the controller never panics.

## Out of scope (the view's job, validated manually on Windows)

- Rendering the tabs/lists, the connection switch (from 010), destructive-action confirmation
  dialogs, progress indicators, and the polling timer wiring.
