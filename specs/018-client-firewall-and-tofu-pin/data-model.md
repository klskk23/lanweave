# Phase 1 Data Model: Client Firewall Control and TOFU Certificate Pinning

This feature changes **only client-local data**. There is no server schema or API change. The single
persisted change is two additive fields on the client state record, behind one `SchemaVersion` bump.

## Entity: Client State Record (`internal/client/state.Record`)

The existing non-secret onboarding record, extended. The device private key and session token stay
**keyring-only** and are never part of this record.

| Field | Type | New? | Meaning / Validation |
|---|---|---|---|
| `SchemaVersion` | int | changed | Now `2`. `Load` rejects `0`; accepts `1` (legacy) and `2`. |
| `ServerURL` | string | — | Required (unchanged). |
| `NodeName` | string | — | Required (unchanged). |
| `IP` | string | — | Required (unchanged). |
| `ServerPublicKey` | string | — | Unchanged. |
| `Endpoint` | string | — | Unchanged. |
| `Network` | string | — | Unchanged. |
| `PinnedCertSHA256` | string | **new** | Lowercase hex SHA-256 of the trusted server **leaf** certificate (DER). Empty = unpinned. Associated with `ServerURL` (the record is per current server). |
| `FirewallAllowVPN` | bool | **new** | Persisted inbound-allow preference. `false` (default) = closed. |

**JSON tags**: `pinned_cert_sha256`, `firewall_allow_vpn`.

### Migration (1 → 2)

- `SchemaVersion` constant becomes `2`.
- `Load`: unchanged validation (`SchemaVersion == 0 || ServerURL == "" || NodeName == "" || IP == ""`
  → incomplete). A v1 record (no new keys) unmarshals with `PinnedCertSHA256 == ""` and
  `FirewallAllowVPN == false`; both are valid defaults (unpinned, firewall off). The in-memory record
  may be normalized to version `2`; the next `Save` writes version `2`.
- `Save`: defaults `SchemaVersion` to `2` when zero (unchanged mechanism, new constant). Writes the
  two new fields.
- **No re-onboarding, no data loss** (FR-020). Existing users keep their identity; posture unchanged
  until they act (SC-005, SC-006).

### State transitions (pin)

```
unpinned (PinnedCertSHA256 == "")
   │  user accepts first-trust prompt for fingerprint F
   ▼
pinned to F
   │  same server later presents fingerprint F' ≠ F, F' not CA-valid  → ErrCertChanged (blocked)
   │      user accepts "certificate changed" warning
   ▼
pinned to F'   (overwrites F; FR-006)

pinned to F  ──(server becomes CA-valid)──▶ connects via system CA; pin F dormant (still stored)
logout / state.Clear ──▶ unpinned (record removed entirely)
```

### State transitions (firewall preference)

`FirewallAllowVPN` flips only via the user toggle and is wiped by logout (state cleared). It is
independent of connection state; the *effect* (the OS rule) is derived (see below).

## Entity: Certificate Trust Pin (logical)

Not a separate stored object — it **is** `Record.PinnedCertSHA256`. Captured at first trust from the
failed verification's leaf certificate (`SHA-256(cert.Raw)`), compared on every later connection.

## Entity: Inbound Allowance (derived OS state, `internal/client/firewall`)

A named Windows Defender Firewall rule. **Derivative state** — not stored in `state.json`; fully
reconstructible from (`FirewallAllowVPN` ∧ tunnel `Connected`) and swept on startup (Principle I:
nftables/firewall rules are derivative, reconstructible, no hidden runtime-only memory).

| Attribute | Value |
|---|---|
| Name | `lanweave-vpn-inbound` (constant; enables idempotent find/delete + sweep) |
| Direction | inbound (`dir=in`) |
| Action | allow |
| Remote scope | `100.127.0.0/16` (VPN subnet; DESIGN §66/§77) |
| Ports / protocol | all (no filter) — covers all local services + ICMP |
| Profile | `any` |
| Platform | Windows only; no-op on other platforms |

**Invariant**: the rule is present **iff** `FirewallAllowVPN == true` **and** the tunnel is
`Connected`. Enforced by `panel.Controller.ReconcileFirewall` / `SetFirewallAllowed` and the startup
sweep.

**Lifecycle**:
```
present  ⟺  (FirewallAllowVPN ∧ Connected)
add:     successful Connect while preference ON; toggle ON while Connected
remove:  Disconnect; toggle OFF; logout; app exit
apply:   delete-by-name then add  (idempotent — no duplicates across reconnects)
sweep:   firewall.Clear() on startup (removes a rule stranded by an unclean exit)
```

## Relationships

- `Record` (1) ──holds──▶ `PinnedCertSHA256` (0..1 pin) for its one `ServerURL`.
- `Record.FirewallAllowVPN` (preference) ──drives, with tunnel state──▶ `Inbound Allowance`
  (0..1 OS rule).
- No relationship to any server-side entity; nothing in this model crosses the network.
