# Phase 1 Data Model: WireGuard Server Interface

This feature introduces **no SQLite tables**. Its "data" is kernel/data-plane state
plus one on-disk key file. The database remains the single source of truth; the
state below is derived and rebuilt at startup (here, to an empty default-deny shape).

---

## Entity: Server tunnel identity (on-disk + derived)

### Key file `<data_dir>/wg_private`

| Property | Value |
|----------|-------|
| Contents | The WireGuard private key, base64-encoded (one line). |
| Permissions | `0600` (owner read/write only). Broader perms are tightened or cause failure. |
| Lifecycle | Generated once on first start; loaded verbatim on every later start; never regenerated while present. |
| Corruption | If present but unreadable/unparseable → startup error, no regeneration (FR-004). |

### Derived public key (in-memory)

- `PublicKey = PrivateKey.PublicKey()`. Stable as long as the key file is stable.
- Held in process for later use (feature 004's `GET /server`); not persisted separately.

---

## Entity: Tunnel interface (kernel state)

| Attribute | Source | Value this feature |
|-----------|--------|--------------------|
| name | `config.wireguard.interface` | e.g. `wg-lanweave` |
| type | fixed | `wireguard` |
| address | derived from `config.wireguard.network` | first usable host address, e.g. `100.127.0.1/16` |
| listen port | `config.wireguard.listen_port` | e.g. `51820` |
| private key | key file | the server key |
| peers | — | **empty** (FR-007; clients added in feature 004) |
| admin state | — | up |

**Reconciliation (startup)**: create if absent; if present and of type `wireguard`,
adopt and reconcile (address, port, key, up) without teardown; if present with a
different type, error.

---

## Entity: Isolation table (kernel nftables state)

| Object | Value this feature |
|--------|--------------------|
| table | `inet <config.nftables.table>` (e.g. `inet lanweave`) |
| chain | `forward` — `type filter hook forward priority filter; policy drop;` |
| sets | none |
| rules | none |

**Rebuild (startup, FR-013)**: delete the table if it exists, then add table + chain
in one netlink flush → deterministic empty default-deny state. Feature 005 will add
`set zone_<id>` and `ip saddr @z ip daddr @z accept` rules into this same table.

---

## Entity: Host forwarding (kernel sysctl)

| Setting | Value | Lifecycle |
|---------|-------|-----------|
| `net.ipv4.ip_forward` | `1` | enabled at startup (idempotent); **not** reverted on shutdown (FR-010) |

---

## Pure logic (unit-testable, no privilege)

- `FirstUsableAddress(cidr)` → (addr, prefixLen) or error for ranges too small.
- `LoadOrGenerateKey(path)` → key + a flag (generated vs loaded); enforces `0600`;
  errors on corruption.
- Config → desired interface/table description mapping.

These carry the correctness-critical invariants (stable identity, owner-only key,
default-deny) and are validated everywhere; the kernel-applying code is validated on
a privileged runner (research.md R8).

---

## Relationship to other features

```
config (001) ──► wg identity + interface (003) ──► peers (004) ──► zone sets/rules (005)
                 nftables table skeleton (003) ──────────────────► zone sets/rules (005)
```

No SQLite schema change here; features 004 (`nodes`) and 005 (`zones`,
`zone_members`) add the tables whose contents later drive peer and rule rebuilds.
