# Contract: Tunnel profile & engine

**Feature**: 010-windows-client-tunnel | **Date**: 2026-06-05

No server endpoints. This contract pins the host-agnostic tunnel configuration the engine
applies and the engine's observable behavior, so they can be unit- and integration-tested.

## Assembled configuration (the user-space WireGuard UAPI)

Built from `state.Record` + the device private key. The engine applies this exact shape:

```text
private_key=<device private key, hex>
public_key=<server public key, hex>
endpoint=<state.Record.Endpoint>            # host:port
allowed_ip=<state.Record.Network>           # split tunnel — VPN range only
persistent_keepalive_interval=25            # DESIGN §6
```

### Rules

| Rule | Requirement |
|------|-------------|
| Keys | stored base64 (wgtypes) → converted to hex for the engine |
| Split tunnel | exactly one `allowed_ip`, equal to the recorded network range; never `0.0.0.0/0` (FR-004) |
| Keepalive | exactly 25 seconds (FR-006) |
| Peer count | exactly one — the server (hub-and-spoke) |
| Validation | address, server key, endpoint, network all present + well-formed, else a clear assembly error (FR-010/edge case) |
| Secrecy | the private key appears only in memory; never logged or written to disk |

## Engine behavior

| Operation | Contract |
|-----------|----------|
| `Connect` | create the adapter (admin on Windows), apply the profile, bring the address up, route the VPN range; reach `Connected` only after a real handshake (the device's UAPI `last_handshake_time>0`) — a completed handshake is itself proof the server is reachable; a connect timeout → `ErrServerUnreachable` + clean teardown |
| `Disconnect` | bring the device down, remove the adapter/address; idempotent; returns to `Disconnected`; no orphan adapter |
| `State` | observable `Disconnected` / `Connecting` / `Connected`, always matching reality (FR-008) |
| app exit | tears the tunnel down (in-process device closed) — no orphan (FR-012) |
| second `Connect` while active | no-op (one tunnel only, FR-013) |

## Reachability guarantees (from spec)

- After `Connect`, the server's VPN address `100.127.0.1` is reachable within 5 s (SC-001).
- Only VPN-range traffic is routed (split tunnel) — normal internet is unaffected (SC-002).
- An idle connection stays up via keepalive (SC-003) and shows online (feature 007).
- After `Disconnect`, VPN addresses are unreachable within a couple of seconds (SC-004).
- Reaching another device additionally requires shared-zone membership (feature 005).

## Out of scope (later)

- The full management panel (feature 011); auto-connect-on-launch and a background service.
