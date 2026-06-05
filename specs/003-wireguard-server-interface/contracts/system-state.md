# Contract: Observable Post-Startup System State

This feature exposes no HTTP API. Its contract is the **observable system state**
after a successful start — what an operator (and feature 004/005) can rely on.

## After a successful start, on the host

### 1. Key file
- `<data_dir>/wg_private` exists, is a single base64 line, mode `0600`, owner = service user.
- Its public key is stable across restarts (same value every start).

### 2. WireGuard interface (`wg show <iface>` / netlink)
```
interface: wg-lanweave
  public key: <stable server pubkey>
  private key: (hidden)
  listening port: <config.wireguard.listen_port>
  (no peers)
```
- `ip addr show <iface>` shows the interface UP with `<first-pool-ip>/<prefix>`
  (e.g. `100.127.0.1/16`).

### 3. Forwarding
- `cat /proc/sys/net/ipv4/ip_forward` → `1`.

### 4. nftables isolation table (`nft list table inet <table>` where the CLI exists)
```
table inet lanweave {
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
}
```
- Table present; one `forward` chain; **policy drop**; **no sets, no rules**.

## Failure contract (startup aborts, service does not serve)

| Condition | Required behavior |
|-----------|-------------------|
| Key file present but corrupt/unreadable | Clear error; **no** new key written; non-zero exit. |
| Lacking privilege to manage links/nftables | Clear, actionable error; non-zero exit. |
| Interface name held by a non-WireGuard device | Clear conflict error. |
| Listen port already in use (incompatibly) | Clear conflict error. |
| Pool too small to host a server address | Clear configuration error. |

## Restart contract

- A restart with unchanged config does **not** delete/recreate the interface (its
  kernel index/identity is preserved); an already-connected client's tunnel is not reset.
- The nftables table is rebuilt to the same clean empty default-deny state.
- IP forwarding remains enabled (not toggled off then on).

## Logging contract

- Each decision is logged at startup: key generated vs loaded, interface created vs
  adopted, forwarding enabled, table rebuilt.
- The private key value appears in **no** log line.
