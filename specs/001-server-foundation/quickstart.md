# Quickstart: Server Foundation

How to build, configure, run, and verify the lanweave server foundation on a clean
Linux host. Doubles as the manual script behind the acceptance tests (US1–US4).

## Prerequisites

- Go 1.23+
- `openssl` (to mint a self-signed cert for local testing)
- `curl`
- `sqlite3` CLI (to inspect the DB)

## 1. Build

```bash
git checkout 001-server-foundation
CGO_ENABLED=0 go build -ldflags "-X main.version=0.1.0+$(git rev-parse --short HEAD)" -o ./lanweaved ./cmd/lanweaved
```

A static binary `./lanweaved` is produced (pure-Go SQLite → no CGO).

## 2. Generate a self-signed TLS cert (local only)

```bash
mkdir -p ./run
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout ./run/key.pem -out ./run/cert.pem \
  -days 365 -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

## 3. Write a config

```bash
mkdir -p ./run/data
cat > ./run/config.toml <<'TOML'
[server]
listen   = "127.0.0.1:8443"
tls_cert = "./run/cert.pem"
tls_key  = "./run/key.pem"
data_dir = "./run/data"

[log]
level = "info"

[ratelimit]
rps   = 100
burst = 200

[wireguard]
network     = "100.127.0.0/16"
listen_port = 51820
interface   = "wg-lanweave"
mtu         = 1420

[auth]
jwt_secret = "0123456789abcdef0123456789abcdef"
jwt_ttl    = "2h"

[admin]
username = "admin"
password = "supersecret-change-me"
TOML
```

## 4. Run

```bash
./lanweaved --config ./run/config.toml
```

Expected startup logs (JSON, one event per line): config loaded → migration
`0001_users` applied → **admin created** (first run) → HTTPS listening on
`127.0.0.1:8443`.

## 5. Verify

### US1 — health check (≤ 3 s cold start, SC-002)

```bash
curl --cacert ./run/cert.pem https://localhost:8443/api/v1/healthz
# → {"status":"ok","version":"0.1.0+<sha>"}   HTTP 200
```

### US1 — HTTPS only (FR-009)

```bash
curl http://localhost:8443/api/v1/healthz   # connection fails / not served over plaintext
```

### US2 — admin bootstrapped & hashed (SC-006)

```bash
sqlite3 ./run/data/db.sqlite "SELECT username, is_admin, substr(password_hash,1,18) FROM users;"
# → admin|1|$argon2id$v=19$m=   (hash is a PHC string, NOT the plaintext)
```

### US2 — bootstrap idempotency (SC-007)

```bash
# capture current hash, restart, confirm unchanged
H1=$(sqlite3 ./run/data/db.sqlite "SELECT password_hash FROM users WHERE username='admin';")
# Ctrl-C the server, then start it again:
./lanweaved --config ./run/config.toml   # logs: "admin exists, skipping bootstrap"
# (in another shell)
H2=$(sqlite3 ./run/data/db.sqlite "SELECT password_hash FROM users WHERE username='admin';")
[ "$H1" = "$H2" ] && echo "IDEMPOTENT ✓"
```

Even after editing `admin.password` in the TOML and restarting, the stored hash MUST
stay identical (FR-015).

### US2 — missing admin password aborts (US2-3)

```bash
sed 's/^password = .*/password = ""/' ./run/config.toml > ./run/bad.toml
./lanweaved --config ./run/bad.toml ; echo "exit=$?"
# → ERROR log "admin credential not provided"; exit != 0; no listener opened
```

### US1 — bad cert path aborts (US1-3)

```bash
sed 's#./run/cert.pem#./run/missing.pem#' ./run/config.toml > ./run/badcert.toml
./lanweaved --config ./run/badcert.toml ; echo "exit=$?"
# → ERROR "TLS certificate load failed"; exit != 0; no port bound
```

### US3 — logs are parseable JSON (SC-005)

```bash
./lanweaved --config ./run/config.toml 2>&1 | head -n 5 | jq -e '.time and .level' >/dev/null && echo "JSON logs ✓"
```

### US4 — rate limiting returns 429 (FR-023)

```bash
# hammer well above 100 rps; expect a mix of 200 and 429
for i in $(seq 1 500); do
  curl -s -o /dev/null -w "%{http_code}\n" --cacert ./run/cert.pem \
    https://localhost:8443/api/v1/healthz &
done | sort | uniq -c
# → some 200, some 429 ; after a pause, requests succeed again (bucket refills)
```

### US1 — graceful shutdown ≤ 10 s (SC-003)

```bash
# send SIGTERM to the running server; it should drain and exit within 10 s
kill -TERM <pid>
# logs: "shutdown initiated" → "shutdown complete"; process exits 0
```

## 6. Run the automated tests

```bash
go test ./...            # unit + integration (real temp SQLite, real TLS listener)
go vet ./... && staticcheck ./...
```

All three test tiers (unit, integration, acceptance) must be green before this
feature is considered done (constitution Principle II).

## Cleanup

```bash
rm -rf ./run ./lanweaved
```
