# Contract: Local state file (`state.json`)

**Feature**: 009-windows-client-skeleton | **Date**: 2026-06-05

The client's local "already set up" record. Non-secret. Its presence makes the app skip
the first-run wizard.

## Location

- Windows: `%LOCALAPPDATA%\lanweave\state.json`
- Other (dev): `<os.UserConfigDir()>/lanweave/state.json`

Directory created with user-only permissions. Written atomically (temp file + rename).

## Schema

```json
{
  "schema_version": 1,
  "server_url": "https://vpn.example.com",
  "node_name": "my-laptop",
  "ip": "100.127.0.5",
  "server_public_key": "<server WG public key, base64>",
  "endpoint": "vpn.example.com:51820",
  "network": "100.127.0.0/16"
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `schema_version` | int | yes | starts at 1; lets future versions migrate |
| `server_url` | string | yes | the server base URL |
| `node_name` | string | yes | this device's name |
| `ip` | string | yes | server-assigned address `100.127.x.y` |
| `server_public_key` | string | yes | for the tunnel (feature 010) |
| `endpoint` | string | yes | server WireGuard endpoint `host:port` |
| `network` | string | yes | VPN range, e.g. `100.127.0.0/16` |

## Invariants

- **No secret**: the private key and the session token MUST NOT appear in this file
  (FR-005). The key lives only in the OS secure store.
- **Presence = onboarded**: a readable, schema-valid record makes the app skip the wizard
  (FR-009). A missing or unreadable record makes the app run the wizard (FR-001).
- **Atomicity**: a crash mid-write must not leave a torn record — write to a temp file and
  rename into place.
- **Cleared on cancel**: a cancelled or failed setup leaves no record (and no vault key),
  so the next launch starts fresh (FR-010, SC-007).

## Behavior

- **Load** at startup: present+valid → home placeholder; absent/invalid → wizard.
- **Save** only after the device is registered AND the vault holds the key (FR-008).
- **Clear** on cancel/cleanup (paired with deleting the vault key).
