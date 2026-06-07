# Phase 1 Data Model: Client Logout Hardening

This slice introduces **no persistent data entities** — no SQLite tables, no
columns, no client state-file fields. The "model" here is the **logout outcome
state machine** that the headless `panel.Controller` exposes and the GUI consumes.
It is documented because it is the testable contract every acceptance test asserts
against.

## Entities

None added. Existing entities merely referenced:

- **Device node** (server `nodes` row): the residue this slice prevents leaking.
  Removed via the existing `DELETE /api/v1/nodes/{id}` (017).
- **Refresh token** (server `refresh_tokens` row): revoked on the done/force
  paths via the existing `POST /api/v1/logout` (024). No schema change.
- **Local session material** (OS keyring entries `SessionTokenName`,
  `RefreshTokenName`, `DeviceKeyName` + the state file): cleared atomically on
  done/force, untouched on blocked. No new fields.

## Logout outcome (the new typed result)

`Controller.Logout()` returns one of three outcomes plus an optional local error:

| Outcome | Meaning | Local side effects | Next UI step |
|---------|---------|--------------------|--------------|
| `LogoutDone` | Remote node confirmed removed (or already absent); RT revoked (best-effort); local cleared | tunnel down, firewall cleared, keyring + state cleared | return to wizard |
| `LogoutBlocked` | All 3 remote-removal attempts failed with network-unreachable | **none** — tunnel, firewall, keyring, state all intact | show two-button blocked prompt (Cancel / Force) |
| `LogoutNeedSignIn` | Session expired and lazy refresh also failed | **none** | prompt fresh sign-in, then retry `Logout()` |

A non-nil `error` accompanies `LogoutDone` only when a *local* teardown step
failed (e.g. keyring delete) — the logout still completes; the UI shows the
"partial fail" information message (existing behavior).

`Controller.ForceLogout()` returns only `error` (local teardown failure). It has a
single outcome: full local teardown, server-side orphan accepted.

## State transitions

```text
                       confirmLogout (user confirms)
                                  │
                                  ▼
                    ┌─────────────────────────────┐
                    │ removeRemoteNode (≤3 tries,  │
                    │   1s between, infinite-prog) │
                    └─────────────┬───────────────┘
            ┌─────────────────────┼───────────────────────┐
            ▼                     ▼                         ▼
   removed / already-      network-unreachable        session expired &
   absent (2xx/404/not     after 3 tries              refresh failed
   in list)                       │                         │
            │                     ▼                         ▼
            ▼              LogoutBlocked            LogoutNeedSignIn
   revoke RT (best-effort)        │                         │
   disconnect tunnel        ┌─────┴──────┐            sign-in prompt
   firewall.Clear()         ▼            ▼            ┌────┴────┐
   clear keyring+state   Cancel      Force log out   ▼         ▼
            │            (no-op)     anyway        success   cancel
            ▼               │            │         (retry    (abort,
      LogoutDone            │            ▼         Logout)    no change)
       → wizard         stay signed  ForceLogout
                         in, no      → full local
                         change      teardown → wizard
                                     (orphan accepted)
```

## Validation / invariants

- **INV-1 (blocked = no-op)**: when `Logout()` returns `LogoutBlocked`, zero
  local mutations occurred — assertable by checking keyring entries and state
  file are byte-for-byte unchanged and `firewall.Clear()` was **not** called.
- **INV-2 (done = clean)**: when `Logout()` returns `LogoutDone`, the device node
  is gone server-side, the RT revoke was attempted, and all three keyring entries
  + state are cleared.
- **INV-3 (already-absent ⇒ done)**: a remote node missing (404 or absent from
  the list) is a success, never a block.
- **INV-4 (retry bound)**: `removeRemoteNode` issues at most 3 remote attempts and
  sleeps at most twice (1 s each) — assertable via the injected `sleep` seam.
- **INV-5 (force = full teardown)**: `ForceLogout()` always clears local state and
  returns to wizard regardless of server reachability.
