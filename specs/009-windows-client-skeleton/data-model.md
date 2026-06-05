# Data Model: Windows Client Skeleton & First-Run Wizard

**Feature**: 009-windows-client-skeleton | **Date**: 2026-06-05

No server schema change. The client introduces local, client-side data only: a secret in
the OS vault, a non-secret JSON state record, and in-memory onboarding state.

## Local state record (`state.Record`, persisted JSON — NO secret)

Written atomically to the per-user path; its presence means "already set up".

| Field | JSON | Type | Notes |
|-------|------|------|-------|
| ServerURL | `server_url` | string | the server base URL the user entered |
| NodeName | `node_name` | string | this device's name (unique within the account) |
| IP | `ip` | string | server-assigned address, dotted `100.127.x.y` |
| ServerPublicKey | `server_public_key` | string | server's WireGuard public key (for the tunnel, feature 010) |
| Endpoint | `endpoint` | string | server WireGuard endpoint `host:port` |
| Network | `network` | string | VPN network range, e.g. `100.127.0.0/16` |
| SchemaVersion | `schema_version` | int | forward-compat marker (starts at 1) |

- **Path**: `%LOCALAPPDATA%\lanweave\state.json` on Windows; `os.UserConfigDir()/lanweave/
  state.json` otherwise (dev). Directory created with user-only permissions.
- **No secret**: the private key is NEVER in this file (FR-005). The session token is NOT
  persisted here (held in memory; re-auth happens when needed by later features).
- **Lifecycle**: absent → wizard runs; written on successful setup → wizard skipped;
  cleared on cancel/cleanup. Atomic temp-file + rename to avoid a torn record.

## Device secret (OS vault, via `keyring.Store`)

| Item | Where | Notes |
|------|-------|-------|
| Device private key | OS secure credential store | keyed by a stable name (e.g. `lanweave:<server_url>:<node_name>` or a fixed `lanweave-device-key`); never in a file or log (FR-005) |

`keyring.Store` interface: `Set(name string, secret []byte) error`,
`Get(name string) ([]byte, error)`, `Delete(name string) error`. Backends: Windows
Credential Manager (DPAPI), a non-Windows dev backend, and an in-memory test fake.

## Onboarding controller state (in-memory, `onboard`)

A small step model driving the wizard; not persisted.

| Field | Type | Notes |
|-------|------|-------|
| Step | enum | `Server` → `Auth` → `DeviceName` → `Provision` → `Done` |
| ServerURL | string | entered at the Server step |
| AuthMode | enum | `SignIn` or `CreateAccount` |
| token | string (in-memory) | session token after auth; never logged or persisted |
| NodeName | string | entered at the DeviceName step |
| keyPair | generated | private stored in the vault before registration; public sent to server |
| result | derived | assigned IP + server info, used to write the state record |

- **Transitions**: each step validates its input, then advances; Back returns to the
  previous step; Cancel aborts and triggers cleanup (delete vault key + any partial record).
- **Provision** performs the sequence in plan.md (auth → store key → register device →
  fetch server info → write record), including the pubkey-idempotent retry.

## Client-side typed errors (`apiclient`)

Mapped from server status + error envelope (`pkg/protocol`), so the UI can show a
specific message (FR-012).

| Error | From | UI meaning |
|-------|------|------------|
| `ErrUnreachable` | transport failure | "Can't reach the server — check the address/network." |
| `ErrUntrustedCert` | TLS verification failure | "The server's certificate isn't trusted." |
| `ErrAuthFailed` | 401 on sign-in | "Sign-in failed — check username and password." |
| `ErrInviteInvalid` | register error (invalid/used invite) | "That invite code is invalid or already used." |
| `ErrUsernameTaken` | register conflict | "That username is taken." |
| `ErrNodeNameTaken` | `409 node_name_taken` | "You already have a device with that name." |
| `ErrPubKeyTaken` | `409 pubkey_taken` | (internal) triggers idempotent recovery, not shown |
| `ErrServer` | 5xx / unexpected | "Something went wrong on the server — try again." |

## Relationships & invariants

- Exactly one state record and one device key per machine (one machine = one device).
- The state record and the vault key are written/cleared together: a complete setup has
  both; a cancelled/failed setup has neither (FR-008, SC-007).
- The state record references the server's identity (public key, endpoint, network) that
  feature 010 will use to build the tunnel; this feature only persists it.
- Device identity is matched server-side by name (for idempotent retry) and by public key
  (uniqueness); the public key is never returned by the server API.
