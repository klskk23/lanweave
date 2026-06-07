# Quickstart: per-user-limits

How to exercise the two caps end-to-end. Server steps are automatable (and are covered
by acceptance tests run under `unshare -rUn go test ./...`); the GUI-presentation rows
are the manual matrix per the Constitution II GUI/exec exemption.

## 0. Configure

Add to `config.toml` (or omit the section to accept the default of 10 for both):

```toml
[limits]
max_devices_per_user     = 3    # small values make the matrix quick
max_owned_zones_per_user = 2
```

- Omit `[limits]` entirely → both caps are 10.
- Set a cap to `0` → that resource is unlimited.
- Set a cap to `-1` → server **refuses to start** and prints the offending field.

Start the server; confirm `GET /api/v1/healthz` returns 200.

## 1. Device cap (US1)

As a **regular** user (created via an invite, not the admin):

1. Register devices up to the cap (3): each `POST /api/v1/nodes` → `201`.
2. Register one more → `409 { "error": "device_limit_reached" }`. No node created
   (`GET /api/v1/nodes` still shows 3).
3. `DELETE /api/v1/nodes/{id}` one device → `204`. Register again → `201` (the deletion
   freed exactly one slot; a second extra create is refused again).

**GUI (manual)**: in the wizard's device-setup step, hitting the cap shows the
localized **device-limit** message (zh-Hans: 设备数量上限; en: device limit) — not a
generic error, and distinct from the "name taken" message.

## 2. Owned-zone cap (US2)

Same regular user:

1. Create zones up to the cap (2): each `POST /api/v1/zones` → `201`.
2. Create one more → `409 { "error": "zone_limit_reached" }`. No zone created.
3. `DELETE /api/v1/zones/{name}` one **owned** zone → `204`. Create again → `201`.
4. **Join is uncapped**: while at the owned-zone cap, `POST /api/v1/zones/{other}/join`
   (a zone owned by a *different* user, correct password) → `200`. Joining does not
   count and is not refused.

**GUI (manual)**: in the panel's create-zone flow, hitting the cap shows the localized
**zone-limit** message — distinct from "zone name taken".

## 3. Operator / admin / unlimited (US3)

1. **Default**: remove `[limits]`, restart → the 11th device / 11th owned zone is the
   first refused (effective cap 10).
2. **Unlimited**: set a cap to `0`, restart → create well past 10 of that resource with
   zero refusals.
3. **Negative**: set a cap to `-1`, start → server fails to start, names the bad field
   (never starts with a silently clamped value).
4. **Admin exempt**: as the **admin** account, create devices / zones beyond any
   positive cap → all `201`, never refused.
5. **Grandfathering**: with several devices already registered, lower
   `max_devices_per_user` below the current count and restart → existing devices remain
   and keep working (`GET /api/v1/nodes` lists them all; the tunnel is unaffected); only
   *new* `POST /api/v1/nodes` is refused until the count drops below the new cap.

## 4. Concurrency (SC-010)

Fire N concurrent `POST /api/v1/nodes` for the same user sitting one below the cap. At
most one succeeds; the user never ends up over the cap. (Covered by a parallel-request
acceptance test against the real store — the conditional-insert atomicity is what makes
this hold.)

## Manual GUI verification matrix

| # | Surface | Action | Expected |
|---|---------|--------|----------|
| G1 | Wizard device setup | register past device cap | localized device-limit message; no device added; can go back |
| G2 | Panel create zone | create past owned-zone cap | localized zone-limit message; no zone added |
| G3 | Panel join zone | join another user's zone while at owned-zone cap | join succeeds (uncapped) |
| G4 | Both | message language follows the in-app language selector | zh-Hans / en strings render correctly |

## Test command

```sh
unshare -rUn go test ./...
```

Server unit + integration tests (config, store, api) run here against real SQLite with
no mocks. The four GUI rows above are verified by hand on the Windows client (Mesa
`opengl32.dll` present on the test VM).
