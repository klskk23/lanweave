# Data Model: Windows Client Tunnel

**Feature**: 010-windows-client-tunnel | **Date**: 2026-06-05

No server schema change and no new persisted client data. The tunnel is assembled at
connect time from the existing 009 inputs. The "model" here is the in-memory connection
profile and the engine's state.

## Inputs (existing, from feature 009)

| Input | Source | Used for |
|-------|--------|----------|
| Device private key | OS secure store (`keyring`, `DeviceKeyName`) | the local end of the tunnel |
| Device address (`ip`) | `state.Record` | the address brought up on the virtual adapter |
| Server public key | `state.Record.ServerPublicKey` | the single peer's identity |
| Server endpoint | `state.Record.Endpoint` | where the peer is reached |
| Network range | `state.Record.Network` | the routed (allowed) IPs — split tunnel |

## Connection profile (in-memory, `tunnel`)

The host-agnostic assembled configuration. Holds the private key only transiently in
memory while connecting; never written to disk or logs.

| Field | Type | Notes |
|-------|------|-------|
| PrivateKey | string (base64 in, hex to the engine) | device key from the vault |
| Address | string | device VPN address `100.127.x.y` |
| ServerPublicKey | string | the peer key |
| Endpoint | string | `host:port` of the server |
| AllowedIPs | string | the network range only (split tunnel), e.g. `100.127.0.0/16` |
| KeepaliveSeconds | int | fixed at 25 (DESIGN §6) |

- **Validation**: address, server key, endpoint, and network must be present and
  well-formed; a missing/invalid input fails profile assembly with a clear error (and, per
  the spec, an empty record routes the user back to setup rather than a tunnel error).
- **Encoding**: WireGuard keys are stored base64 and converted to hex for the engine's UAPI
  configuration.

## Connection state (in-memory, `tunnel`)

| State | Meaning |
|-------|---------|
| Disconnected | no tunnel; no adapter/address active |
| Connecting | adapter created + profile applied; awaiting the first handshake |
| Connected | handshake with the server succeeded; address active, server reachable |

- **Transitions**: `Disconnected → Connecting` on Connect; `Connecting → Connected` once a
  handshake is observed; `Connecting/Connected → Disconnected` on Disconnect, app exit, or
  failure (with a typed error). Guarded for concurrent access; exposed for the UI to render.
- **One tunnel only**: a second Connect while already connected/connecting is a no-op (one
  machine = one device).

## Engine errors (typed, `tunnel`)

| Error | Condition | UI meaning |
|-------|-----------|------------|
| `ErrServerUnreachable` | no handshake within the connect timeout | "Couldn't reach the server — check your connection." |
| `ErrElevationDenied` | admin elevation refused (Windows) | "lanweave needs administrator rights to create the network adapter." |
| `ErrAdapter` | virtual adapter could not be created/addressed | "Couldn't set up the network adapter." |
| `ErrNoSetup` | the device key or setup record is missing | (routes back to first-run setup) |

## Relationships & invariants

- Exactly one tunnel/profile/adapter per machine; the engine owns the device lifecycle.
- The private key flows from the vault → profile → engine in memory only; it is never
  persisted by this feature or written to any log.
- `Connected` implies a real, completed handshake with the server (honest status, FR-008).
- Disconnect/app-exit always returns to `Disconnected` with the adapter removed — no orphan
  (FR-009/FR-012), and is idempotent.
- The state record (009) is the source of the server/peer details; this feature reads it and
  adds nothing to it.
