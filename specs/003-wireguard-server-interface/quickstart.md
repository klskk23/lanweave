# Quickstart: WireGuard Server Interface

Extends the feature-001 quickstart. Because this feature manipulates kernel network
state, the data-plane checks require **root** or a **rootless user+net namespace**.

## Build

```bash
CGO_ENABLED=0 go build -ldflags "-X main.version=0.3.0" -o ./lanweaved ./cmd/lanweaved
```

## Run (privileged) and verify the data plane

Use the same `config.toml` as feature 001 (it already has `[wireguard]` and
`[nftables]`). Run as root (data-plane setup needs `CAP_NET_ADMIN`):

```bash
sudo ./lanweaved --config /etc/lanweave/config.toml &
```

### US1 — interface with stable identity

```bash
ip addr show wg-lanweave            # UP, inet 100.127.0.1/16
sudo wg show wg-lanweave            # public key set, listening port, NO peers
ls -l /var/lib/lanweave/wg_private  # -rw------- (0600)

# Stable identity across restart:
PUB1=$(sudo wg show wg-lanweave public-key)
sudo kill -TERM %1; sleep 1; sudo ./lanweaved --config /etc/lanweave/config.toml &
PUB2=$(sudo wg show wg-lanweave public-key)
[ "$PUB1" = "$PUB2" ] && echo "stable identity ✓"
```

### US2 — forwarding + default-deny isolation table

```bash
cat /proc/sys/net/ipv4/ip_forward          # 1
sudo nft list table inet lanweave          # forward chain, policy drop, no rules/sets
```

### US3 — idempotent restart & safe failure

```bash
# Routine restart adopts the live interface (index unchanged):
IDX1=$(cat /sys/class/net/wg-lanweave/ifindex)
sudo kill -TERM %1; sleep 1; sudo ./lanweaved --config /etc/lanweave/config.toml &
IDX2=$(cat /sys/class/net/wg-lanweave/ifindex)
[ "$IDX1" = "$IDX2" ] && echo "interface preserved across restart ✓"

# Corrupt key => startup fails, no regeneration:
sudo kill -TERM %1
sudo cp /var/lib/lanweave/wg_private /tmp/wg_private.bak
echo "not-a-key" | sudo tee /var/lib/lanweave/wg_private >/dev/null
sudo ./lanweaved --config /etc/lanweave/config.toml ; echo "exit=$?"   # non-zero, clear error
sudo diff <(echo "not-a-key") /var/lib/lanweave/wg_private && echo "key NOT regenerated ✓"
sudo cp /tmp/wg_private.bak /var/lib/lanweave/wg_private

# Unprivileged start => clear privilege error:
./lanweaved --config /etc/lanweave/config.toml ; echo "exit=$?"   # non-zero, privilege error
```

## Automated tests

```bash
# Unit tier (runs anywhere, no privilege):
go test ./internal/server/wg/ ./internal/server/netfw/ -run 'Key|Addr|Desired'

# Full tier incl. kernel integration — run as root OR in a rootless netns:
sudo go test ./internal/server/wg/ ./internal/server/netfw/
#   or, rootless (maps you to root in a fresh user+net namespace):
unshare -rUn go test ./internal/server/wg/ ./internal/server/netfw/

# Whole suite:
go test ./...        # privileged data-plane tests SKIP (with a clear message) if unprivileged
make lint
```

> CI MUST run the privileged tier (root job or `unshare -rUn`) so the kernel paths are
> actually exercised, not perpetually skipped (constitution Principle II; research.md R8).

## Cleanup (privileged)

```bash
sudo kill -TERM %1
sudo ip link del wg-lanweave 2>/dev/null
sudo nft delete table inet lanweave 2>/dev/null
```
