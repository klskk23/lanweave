# Data Model: Client Logout and TLS Opt-In

**Feature**: 017-client-logout-and-tls-optin | **Date**: 2026-06-06

This feature adds **no** database table, **no** migration, and **no** schema field. It removes
and re-creates existing records and introduces one in-memory, per-session flag. This document
records the entities it touches and how each changes across the logout and insecure flows.

## Entities

### Device registration (node) — server-side, existing

- **What**: A row in the server `nodes` table plus its derived WireGuard peer and nftables zone
  set memberships. Created during onboarding (`POST /api/v1/nodes`).
- **Change in this feature**: Logout removes *this device's own* node via the existing
  `DELETE /api/v1/nodes/{id}` handler, which (in one place, already tested by 004/008):
  releases the IP, removes the WG peer, and clears the node's address from every zone set.
- **Ownership rule**: Removal is scoped to the caller (`GetOwned`/`DeleteOwned` by `userID`); a
  user can only delete their own node. No new authorization logic is added.

### Local session token — client-side, existing (keyring)

- **What**: The cached JWT under `keyring.SessionTokenName` (`"lanweave-session-token"`), used to
  reuse a sign-in across launches.
- **Change**: Logout deletes it. `UseInsecureClient` re-applies it (read from the keyring) onto a
  rebuilt insecure client so the session survives the client swap.

### Device private key — client-side, existing (keyring)

- **What**: The WireGuard private key under `keyring.DeviceKeyName`
  (`"lanweave-device-private-key"`), never written to a file.
- **Change**: Logout deletes it, so re-onboarding generates a fresh keypair and thus a new device
  identity (FR-007). No reuse.

### Local setup state (state.json) — client-side, existing

- **What**: `state.Record` (`SchemaVersion`, `ServerURL`, `NodeName`, `IP`, `ServerPublicKey`,
  `Endpoint`, `Network`). Its presence routes the app to Home instead of the wizard.
- **Change**: Logout calls `state.Clear(path)` to remove the file, so startup routes back to the
  wizard. **Schema is unchanged — `SchemaVersion` stays 1.** No field is added or interpreted
  differently.

### Certificate-verification state (per session) — client-side, NEW, in-memory only

- **What**: A boolean "this session is bypassing TLS certificate verification".
- **Where it lives**: `Wizard.insecure` (already a field) for the setup flow; a new
  `panel.Controller.insecure` field for the running panel. Exposed via `Controller.Insecure()`
  and reflected by `apiclient.Client.Insecure()`.
- **Lifecycle**:
  - Initialized from the `--insecure` CLI flag at process start (FR-014).
  - Flipped to `true` when the user accepts the reactive opt-in after `ErrUntrustedCert`
    (wizard sets `z.insecure = true` + rebuilds; panel calls `UseInsecureClient`).
  - **Never persisted** (FR-012): there is no on-disk representation. A restart resets it to the
    CLI flag value. Logout's `restart` seeds the new wizard with the original CLI value, not the
    opted-in value (research Decision 5).
- **Drives**: the persistent "⚠ certificate not verified" indicator (FR-013) whenever true.

## State transitions

### Logout (US1)

```
Home (set up, maybe connected)
  └─ user confirms Logout
       ├─ tunnel.Disconnect()                      (idempotent; ok if already down)
       ├─ Controller.Logout():
       │    ├─ ListNodes → find this machine's id
       │    │     ├─ network error → remoteRemoved=false
       │    │     ├─ not found     → remoteRemoved=true (already gone)
       │    │     └─ found → DeleteNode(id) → ok? remoteRemoved=true : false
       │    └─ ALWAYS: keyring.Delete(session), keyring.Delete(deviceKey), state.Clear()
       └─ restart() → Wizard.stepServer            (re-enter server URL)
```
On `remoteRemoved=false`, the UI shows an informational notice that the device may still be
registered on the server before navigating.

### Insecure opt-in (US2)

```
connect attempt → apiclient.ErrUntrustedCert
  └─ UI shows "certificate could not be verified — continue insecurely?"
       ├─ decline → no connection (state unchanged, still verifying)
       └─ accept  → rebuild client WithInsecure (+re-apply token), set insecure=true,
                    retry the operation, show persistent "not verified" indicator
restart of the process → verification enforced again by default
```

## Validation rules (unchanged, reused)

- Server-side node deletion authorization: caller must own the node (existing `GetOwned`).
- `state.Load` still rejects incomplete records; logout removes the file entirely rather than
  writing a partial one.
- No new field validation is introduced (no new fields).
