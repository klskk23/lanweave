# Research: Zone Create Auto-Join

No open `NEEDS CLARIFICATION` items — the design was fully resolved during the planning
interview. This file records the decisions and the alternatives weighed.

## D1 — Atomic create+join in one operation, not two requests

- **Decision**: `POST /api/v1/zones` performs the auto-join server-side, in the same
  request that creates the zone.
- **Rationale**: The user value is removing a redundant second step. Doing it client-side
  (client fires create, then fires join) would leave a visible window where the zone
  exists but the creator is not a member, and a failed second call would strand the user
  outside their own zone. Server-side keeps the invariant in one place.
- **Alternatives considered**: Two client calls (rejected: non-atomic, partial-failure
  UX). A new dedicated `create-and-join` endpoint (rejected: duplicates create; an
  optional field on the existing endpoint is smaller and backward compatible).

## D2 — Optional device identifier on the create request

- **Decision**: `CreateZoneRequest` gains `node_id int64` with `json:",omitempty"`. `0`
  or omitted = create-only (legacy). Non-zero = auto-join that device.
- **Rationale**: Backward compatibility — existing callers and existing tests that send
  no `node_id` keep the old result. The GUI always sends the current device id.
- **Alternatives considered**: Mandatory `node_id` (rejected: breaks create-only callers
  and every existing `TestCreateZone` case; some callers legitimately only want the zone
  shell). A server-inferred "the caller's single device" (rejected: a user may own many
  devices; the client knows which one is *this* machine, the server does not).

## D3 — Validate device ownership BEFORE creating the zone

- **Decision**: When `node_id != 0`, call `Nodes().GetOwned(userID, node_id)` first; on
  `ErrNodeNotFound` return 404 and create nothing.
- **Rationale**: A device the caller does not own must never be added to a zone, and a
  bad id must not leave an orphaned empty zone. Checking first means the common
  rejection path makes zero mutations. `GetOwned` already conflates "absent" and "not
  yours" into one error, so this does not enable enumeration.
- **Alternatives considered**: Create then validate then roll back (rejected: more
  mutations on the error path for no benefit). A separate authorization layer (rejected:
  `GetOwned` is the established check, reused from `joinZone`).

## D4 — Reuse the existing compensation-delete pattern, no new SQL transaction

- **Decision**: After the zone row exists, any subsequent failure (`AddZone`, `Join`,
  `AddMember`) triggers the existing rollback: delete the zone (DB cascade removes the
  membership row) and tear down its nft state. No `BEGIN…COMMIT` wrapper is introduced.
- **Rationale**: `createZone` already compensates an `AddZone` failure with
  `Zones().Delete`. Extending the same pattern to the join steps keeps one consistent
  idiom. `Zones().Delete` cascades `zone_members`; `netfw.DeleteZone` removes the set +
  rule (and any partial element). A multi-statement SQL transaction would not cover the
  nft side anyway (nft is a separate kernel commit), so a transaction adds ceremony
  without closing the real gap.
- **Alternatives considered**: Wrap DB writes in one SQL tx (rejected: doesn't span the
  nft commit, which is the actual cross-system concern; the delete-compensation already
  reconciles both, and startup `Rebuild` is the ultimate backstop).
- **Invariant**: with `node_id != 0`, the outcome is `{zone created + creator joined +
  nft set has the device IP}` or `{nothing created + error}`.

## D5 — Membership uses the zone password the user just set, no re-verification

- **Decision**: Auto-joining the creator does not re-check the zone password.
- **Rationale**: The creator authored the password in this very request; `joinZone`'s
  password check exists to gate *outsiders*. Re-verifying your own just-set password is
  pointless and would force the client to round-trip the plaintext again.
- **Alternatives considered**: Run the password through `VerifyPassword` anyway
  (rejected: wasted argon2 verify, no security gain).

## D6 — Client keeps `Controller.CreateZone(name, password)`; resolves device id internally

- **Decision**: `apiclient.Client.CreateZone` gains a `nodeID` parameter; the panel `api`
  interface matches. `Controller.CreateZone(name, password)` keeps its signature and
  resolves the current device via the existing `thisMachineNodeID()` (the same helper
  `Controller.JoinZone`/`LeaveZone` already use), then calls
  `api.CreateZone(name, id, password)`.
- **Rationale**: Keeping the controller signature means `internal/client/ui/panel.go`
  (the Fyne create dialog) is untouched. The id-resolution logic already exists and is
  tested; reusing it avoids divergence.
- **Alternatives considered**: Thread the id through the UI layer (rejected: needless UI
  change; the UI does not know node ids). An opt-out "join this zone now?" checkbox
  (rejected per interview: GUI always auto-joins; the separate "Join zone" button remains
  for *other* people's zones).

## D7 — Testing fidelity (Constitution II)

- **Decision**: Server behavior is proven with the real-nftables `zoneHarness`
  (`testutil.RequireNetAdmin`, asserting `setHas(zoneID, ip)`); client wiring with the
  fake-`api` seam.
- **Rationale**: nft is never mocked; the truth that the creator is *traffic-eligible* is
  the device IP being present in the zone's real nft set — the same fidelity feature 005
  used to validate isolation. The fake `api` is the HTTP-client boundary, not a forbidden
  SQLite/nft/WG mock; it is the correct seam to assert "the controller injected this
  machine's node id."
- **Alternatives considered**: Full cross-node `ping` in netns for the creator
  (rejected: that is the 005-level isolation test; membership + set element is the
  established API-level proof and keeps the test fast and deterministic).
