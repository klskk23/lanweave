# Quickstart: Zones and nftables Isolation

Builds on 001–004. Zone CRUD + membership work over HTTP unprivileged (httptest);
the **real nftables set/rule effects** and node-to-node reachability need root or a
rootless netns. The running binary needs `CAP_NET_ADMIN`.

```bash
CURL="curl -sk"; BASE=https://localhost:8443/api/v1
TOKEN=$($CURL -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<admin-pw>"}' | jq -r .token)
# Register two nodes (feature 004) with throwaway keys:
P1=$(wg genkey|wg pubkey); P2=$(wg genkey|wg pubkey)
N1=$($CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"name\":\"a\",\"wg_pubkey\":\"$P1\"}" | jq -r .id)
N2=$($CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"name\":\"b\",\"wg_pubkey\":\"$P2\"}" | jq -r .id)
```

## US1 — create a zone

```bash
$CURL -X POST $BASE/zones -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"devteam","password":"zone-strong-pw"}'
# → {"id":1,"name":"devteam","is_owner":true}
# duplicate name → 409
$CURL -X POST $BASE/zones -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"devteam","password":"x2xxxxxx"}' -w "\n%{http_code}\n"   # 409
```

## US2 — join nodes; same-zone admitted in the ruleset

```bash
$CURL -X POST $BASE/zones/devteam/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"node_id\":$N1,\"password\":\"zone-strong-pw\"}" -w " [%{http_code}]\n"   # 200
$CURL -X POST $BASE/zones/devteam/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"node_id\":$N2,\"password\":\"zone-strong-pw\"}" -w " [%{http_code}]\n"   # 200

# (privileged) the zone set holds both addresses and the accept rule exists:
sudo nft list table inet lanweave    # set zone_1 = { 100.127.0.2, 100.127.0.3 }; ip saddr @zone_1 ip daddr @zone_1 accept

# wrong password OR unknown zone → identical 403:
$CURL -X POST $BASE/zones/devteam/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"node_id\":$N1,\"password\":\"wrong\"}" ; echo
$CURL -X POST $BASE/zones/nope/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"node_id\":$N1,\"password\":\"whatever\"}" ; echo
# both → {"error":"invalid_zone_or_password",...}
```

## US4 — list zones & members

```bash
$CURL $BASE/zones -H "Authorization: Bearer $TOKEN" | jq -c '.zones[]'
$CURL $BASE/zones/devteam/members -H "Authorization: Bearer $TOKEN" | jq -c '.members[]'
# → {"node_name":"a","ip":"100.127.0.2","owner":"admin"} ...
```

## US3 — leave; address removed from the set

```bash
$CURL -X POST $BASE/zones/devteam/leave -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"node_id\":$N1}" -w "leave -> %{http_code}\n"   # 204
sudo nft list set inet lanweave zone_1   # 100.127.0.2 gone
```

## US5 — node delete clears membership; restart rebuilds

```bash
# Re-join N1, then DELETE the node → it must leave the zone set (no inherited reachability):
$CURL -X POST $BASE/zones/devteam/join -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"node_id\":$N1,\"password\":\"zone-strong-pw\"}"
$CURL -X DELETE $BASE/nodes/$N1 -H "Authorization: Bearer $TOKEN" -w "delete node -> %{http_code}\n"   # 204
sudo nft list set inet lanweave zone_1   # 100.127.0.2 gone (cleared on node delete)

# Restart → sets/rules rebuilt from the DB to match memberships:
sudo systemctl restart lanweaved   # or re-run the binary
sudo nft list table inet lanweave  # zone_1 contains exactly the current members
```

## Real reachability (manual / heavy-CI) — two clients

Stand up two WireGuard clients peered to the relay (each with its registered key +
assigned address). Join both to one zone → `ping` between their addresses succeeds.
Put them in different zones (or none shared) → `ping` is dropped. This is the literal
SC-001 check; automated tests assert the equivalent kernel ruleset state.

## Automated tests

```bash
go test ./internal/server/store/ ./internal/server/api/    # zone CRUD/membership (real SQLite, unprivileged)
unshare -rUn go test ./...                                 # + real nftables set/rule state
go test ./...                                              # privileged nft tests SKIP if unprivileged
make lint
```

> CI MUST run the privileged tier so the nftables set/rule state is actually exercised.
