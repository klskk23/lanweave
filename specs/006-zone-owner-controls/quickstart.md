# Quickstart: Zone Owner Controls

Builds on 001–005. Authz + password update work unprivileged (httptest); the real
nftables effects of kick/delete need root or a rootless netns. Assumes a running
relay and a zone `devteam` (owner = admin) with two member nodes (feature 005).

```bash
CURL="curl -sk"; BASE=https://localhost:8443/api/v1
TOKEN=$($CURL -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<admin-pw>"}' | jq -r .token)
```

## US1 — change the password (members kept)

```bash
$CURL -X PATCH $BASE/zones/devteam -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"password":"rotated-strong-pw"}' -w "\n%{http_code}\n"   # 200

# Existing members still listed (none ejected):
$CURL $BASE/zones/devteam/members -H "Authorization: Bearer $TOKEN" | jq '.members | length'

# Old password no longer joins; the new one does (register a fresh node first):
PUB=$(wg genkey|wg pubkey)
NID=$($CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"name\":\"c\",\"wg_pubkey\":\"$PUB\"}" | jq -r .id)
$CURL -X POST $BASE/zones/devteam/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"node_id\":$NID,\"password\":\"<old-pw>\"}" -w "\nold-pw -> %{http_code}\n"   # 403
$CURL -X POST $BASE/zones/devteam/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"node_id\":$NID,\"password\":\"rotated-strong-pw\"}" -w "\nnew-pw -> %{http_code}\n"   # 200
```

## US2 — kick a member (privileged effect)

```bash
# Kick node NID; its address leaves the zone set.
$CURL -X DELETE $BASE/zones/devteam/members/$NID -H "Authorization: Bearer $TOKEN" -w "kick -> %{http_code}\n"   # 204
sudo nft list set inet lanweave zone_1   # the kicked node's address is gone
$CURL $BASE/nodes -H "Authorization: Bearer $TOKEN" | jq '.nodes[] | select(.id=='"$NID"')'   # node still exists
# Kick again → 404 (no longer a member).
$CURL -X DELETE $BASE/zones/devteam/members/$NID -H "Authorization: Bearer $TOKEN" -w "re-kick -> %{http_code}\n"   # 404
```

## US3 — delete the zone (name released, rules destroyed)

```bash
$CURL -X DELETE $BASE/zones/devteam -H "Authorization: Bearer $TOKEN" -w "delete -> %{http_code}\n"   # 204
sudo nft list table inet lanweave    # zone_1 set + its accept rule are gone
# The name can be re-created:
$CURL -X POST $BASE/zones -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"devteam","password":"fresh-strong-pw"}' -w "\nrecreate -> %{http_code}\n"   # 201
# Member nodes still exist:
$CURL $BASE/nodes -H "Authorization: Bearer $TOKEN" | jq '.nodes | length'
```

## US4 — authz: non-owner forbidden

```bash
# As a different user (bob), all three owner ops on admin's zone → 403:
BOB=$($CURL -X POST $BASE/login -H 'Content-Type: application/json' -d '{"username":"bob","password":"<bob-pw>"}' | jq -r .token)
$CURL -o /dev/null -w "patch -> %{http_code}\n"  -X PATCH  $BASE/zones/devteam -H "Authorization: Bearer $BOB" -H 'Content-Type: application/json' -d '{"password":"hijack-attempt"}'   # 403
$CURL -o /dev/null -w "kick -> %{http_code}\n"   -X DELETE $BASE/zones/devteam/members/1 -H "Authorization: Bearer $BOB"   # 403
$CURL -o /dev/null -w "delete -> %{http_code}\n" -X DELETE $BASE/zones/devteam -H "Authorization: Bearer $BOB"   # 403
# Nonexistent zone → 404:
$CURL -o /dev/null -w "missing -> %{http_code}\n" -X DELETE $BASE/zones/ghostzone -H "Authorization: Bearer $TOKEN"   # 404
```

## US4 — restart reflects everything

```bash
sudo systemctl restart lanweaved   # or re-run the binary
sudo nft list table inet lanweave  # deleted zones absent; kicked members absent; sets match the DB
```

## Automated tests

```bash
go test ./internal/server/store/ ./internal/server/api/   # password/authz/kick/delete (real SQLite)
unshare -rUn go test ./...                                 # + real nftables (kick element gone, DeleteZone destroys set+rule, restart)
go test ./...                                              # privileged nft tests SKIP if unprivileged
make lint
```

> CI MUST run the privileged tier so the kick/delete/rebuild nftables effects are exercised.
