# Quickstart: Zone Create Auto-Join

How to build, exercise, and verify the feature. Server integration tests touch **real
nftables** and must run with CAP_NET_ADMIN.

## Build & static checks

```bash
gofmt -l .            # must be empty
go vet ./...
staticcheck ./...
```

## Run the full test suite (real SQLite + real nftables)

The zone tests use `testutil.RequireNetAdmin(t)`; run inside a rootless user+net
namespace so they actually execute (a fresh netns starts with `lo` DOWN — bring it up):

```bash
unshare -rUn bash -c 'ip link set lo up && go test ./...'
```

Targeted:

```bash
unshare -rUn bash -c 'ip link set lo up && go test ./internal/server/api/ -run TestCreateZone -count=1 -v'
unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/... -count=1'
```

## Server acceptance check (US1) — create makes you a member

Against the `zoneHarness` (real nft table):

1. Seed a user `alice` and a device node `laptop` (`h.seedNode`).
2. `POST /api/v1/zones` with `{name:"z1", password:"zone-strong-pw", node_id:<laptop id>}`.
3. Expect `201` and `ZoneResponse{is_owner:true}`.
4. Assert `h.setHas(zone.ID, laptop.IP)` is **true** — the device IP is in the real nft
   zone set.
5. `GET /api/v1/zones/z1/members` lists `laptop` (name + `100.127.x.y` IP).

## Server security check (US edge) — foreign device id creates nothing

1. `alice` owns `laptop`; create `bob` who does **not** own it.
2. As `alice`, `POST /api/v1/zones` with `node_id:<a node bob owns / a bogus id>`.
3. Expect `404 not_found`.
4. Assert the zone was **not** created: `GET /api/v1/zones` for `alice` does not list it,
   and the nft set for it does not exist (`h.setExists` is false for any new id).

## Server backward-compat check — no device id = create-only

1. `POST /api/v1/zones` with `{name:"z2", password:"zone-strong-pw"}` (no `node_id`).
2. Expect `201`; `GET /api/v1/zones/z2/members` is **empty** (legacy behavior).
3. Existing `TestCreateZone` (which sends no `node_id`) stays green.

## Client checks

- **apiclient**: `CreateZone("z1", 12, "pw")` issues `POST /api/v1/zones` whose body
  contains `"node_id":12` (assert against an `httptest` server capturing the body).
- **panel controller**: with a fake `api`, `Controller.CreateZone("z1","pw")` calls
  `api.CreateZone` with `nodeID == this machine's node id` (resolved from the device
  list via the setup record), proving the current device is auto-joined.

## Manual GUI check (documented exception)

On Windows, sign in → main panel → **Create zone** (name + password) → without clicking
"Join zone", the new zone's member list shows **this device**. This Fyne-level check is
manual (see plan.md Complexity Tracking); the headless controller behavior above is the
automated proof.

## Definition of done

- All three server cases pass under `unshare -rUn` (happy + foreign + backward-compat).
- Client apiclient + controller seam tests pass.
- `TestCreateZone` / `TestJoinZone` and the rest of the suite stay green.
- gofmt/vet/staticcheck clean.
