# Quickstart: Node Registration and IPAM

Builds on 001–003. Node CRUD over HTTP works unprivileged via httptest, but the
**real WireGuard peer effects** (peer present/absent, startup rebuild) need root or
a rootless netns. The running binary needs `CAP_NET_ADMIN` for the data plane.

Config must now include a `[wireguard] endpoint` value (the public UDP host:port).

```bash
CURL="curl -sk"; BASE=https://localhost:8443/api/v1
# Log in (admin from 001 bootstrap) and capture a token:
TOKEN=$($CURL -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<admin-pw>"}' | jq -r .token)
# A throwaway client key pair:
PRIV=$(wg genkey); PUB=$(echo "$PRIV" | wg pubkey)
```

## US1 — register a node and read server info

```bash
# Register → assigned address
$CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"laptop\",\"wg_pubkey\":\"$PUB\"}"
# → {"id":1,"name":"laptop","ip":"100.127.0.2"}

# Server info for the client tunnel config
$CURL $BASE/server -H "Authorization: Bearer $TOKEN" | jq .
# → {"public_key":"...","endpoint":"vpn.example.com:51820","network":"100.127.0.0/16","mtu":1420}

# (privileged) the relay now has a peer for this node:
sudo wg show wg-lanweave | grep -A2 "$PUB"
# → peer: <PUB> ; allowed ips: 100.127.0.2/32
```

## US1 — addresses ascend

```bash
PUB2=$(wg genkey | wg pubkey)
$CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"name\":\"phone\",\"wg_pubkey\":\"$PUB2\"}"
# → ip 100.127.0.3  (next after .2)
```

## US2 — list my nodes

```bash
$CURL $BASE/nodes -H "Authorization: Bearer $TOKEN" | jq '.nodes[] | {name,ip}'
# → laptop/100.127.0.2, phone/100.127.0.3
```

## US3 + US4 — delete frees + recycles the lowest address

```bash
# Delete the .2 node:
$CURL -X DELETE $BASE/nodes/1 -H "Authorization: Bearer $TOKEN" -w "%{http_code}\n"   # 204
sudo wg show wg-lanweave | grep "$PUB" || echo "peer removed ✓"

# Next registration reuses the freed lowest address (.2):
PUB3=$(wg genkey | wg pubkey)
$CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"name\":\"tablet\",\"wg_pubkey\":\"$PUB3\"}"
# → ip 100.127.0.2  (recycled)
```

## US4 — conflicts and exhaustion

```bash
# Duplicate name (same user) → 409 node_name_taken
$CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"name\":\"phone\",\"wg_pubkey\":\"$(wg genkey|wg pubkey)\"}" \
  -w "\n%{http_code}\n"   # 409

# Duplicate pubkey (any user) → 409 pubkey_taken
$CURL -X POST $BASE/nodes -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"name\":\"dup\",\"wg_pubkey\":\"$PUB2\"}" \
  -w "\n%{http_code}\n"   # 409
```

## US4 — restart preserves peers (privileged)

```bash
# After registering nodes, restart the relay (root). The interface is preserved
# (feature 003) and peers are rebuilt from the DB (feature 004):
sudo systemctl restart lanweaved   # or re-run the binary
sudo wg show wg-lanweave | grep -c "allowed ips"   # == number of registered nodes
```

## Automated tests

```bash
# Unit + SQLite-integration (no privilege): IPAM math, node CRUD, alloc/recycle/concurrency/exhaustion
go test ./internal/server/ipam/ ./internal/server/store/ ./internal/server/api/

# Full incl. real WG peers + startup rebuild — root or rootless netns:
unshare -rUn go test ./...

go test ./...     # privileged peer tests SKIP (clear message) if unprivileged
make lint
```

> CI MUST run the privileged tier (root / `unshare -rUn`) so peer add/remove and the
> startup rebuild are actually exercised (constitution Principle II).
