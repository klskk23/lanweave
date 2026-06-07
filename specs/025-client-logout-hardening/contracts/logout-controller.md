# Contract: Logout Controller (headless `panel.Controller`)

This slice exposes no new HTTP endpoint — it reuses `DELETE /api/v1/nodes/{id}`
(017) and `POST /api/v1/logout` (024). The contract under test is the **behavioral
interface** of the headless controller that the Fyne panel drives. It is the
single surface the acceptance tests in `internal/client/panel/panel_test.go`
exercise against a real `apiclient.Client` over `httptest`.

## Types

```go
// LogoutOutcome is the result classification of an attempted logout.
type LogoutOutcome int

const (
    LogoutDone       LogoutOutcome = iota // remote removed (or absent), local cleared
    LogoutBlocked                         // network-unreachable after retries; nothing changed
    LogoutNeedSignIn                      // session expired and refresh failed; prompt re-auth
)
```

## `Logout() (LogoutOutcome, error)`

Runs the remote-first hardened flow.

**Preconditions**: controller holds a (possibly expired) session; tunnel may be up
or down (removal uses the public API either way).

**Behavior**:
1. Resolve this machine's node id and `DELETE` it, retrying **only** on
   `apiclient.ErrUnreachable`, at most **3 attempts** with a **1 s** sleep between
   attempts (via the injected `sleep` seam).
2. If the node is confirmed removed, or already absent (404 →
   `apiclient.ErrZoneNotFound`, or not in the node list → `errNotRegistered`):
   - best-effort `api.Logout()` (revoke this device's RT),
   - `firewall.Clear()`,
   - delete keyring `SessionTokenName`, `RefreshTokenName`, `DeviceKeyName`,
   - `state.Clear(statePath)`,
   - return `(LogoutDone, joinedLocalErr)` — `joinedLocalErr` non-nil only on a
     local-delete failure (logout still considered complete).
3. If all 3 attempts failed with `ErrUnreachable`: return `(LogoutBlocked, nil)`
   and perform **no** local mutation and **no** `firewall.Clear()`.
4. If a `DELETE` (after lazy refresh) still surfaces `ErrSessionExpired` /
   `ErrRefreshFailed`: return `(LogoutNeedSignIn, nil)`, no local mutation.
5. A reachable non-network error that is not auth-related (5xx, cert change) is
   **not** blocked: it follows the same local-clear-and-return path as 017's prior
   behavior and returns `(LogoutDone, ErrRemoteMayLinger)`. `ErrRemoteMayLinger`
   is an exported sentinel joined into the returned error (the GUI tests it with
   `errors.Is`) — it is the "remote may still be registered" advisory, NOT a local
   teardown failure. The GUI shows `panel.logoutRemoteLinger` for it (preserved
   from the existing info-message branch). A genuine local-delete failure on the
   done path is joined alongside it; either makes the error non-nil.

**Postconditions by outcome**: see `data-model.md` INV-1..INV-4.

**Tunnel note**: the tunnel disconnect itself is performed by the GUI (`p.tn`),
not the controller (the controller has no tunnel handle). The controller's
contract is "do not require the tunnel for removal, and only signal `LogoutDone`
once it is safe for the GUI to disconnect + restart." The GUI disconnects on
`LogoutDone`/force, never on `LogoutBlocked`/`LogoutNeedSignIn`.

## `ForceLogout() error`

The escape hatch from the blocked prompt.

**Behavior**: unconditional full local teardown — best-effort `api.Logout()`,
`firewall.Clear()`, delete the three keyring entries, `state.Clear()`. Returns a
joined local error (non-nil only on local-delete failure). Always results in a
return-to-wizard at the GUI. Accepts a server-side orphaned node.

**Postcondition**: INV-5 — local state always cleared regardless of server
reachability.

## Injected seam (test-only)

```go
// sleep is the controller's delay between remote-removal retries. Defaults to
// time.Sleep; tests replace it with a no-op (or a recorder asserting it is called
// with 1*time.Second exactly twice) to keep the suite instant and deterministic
// (Constitution II — no wall-clock sleeps in tests).
sleep func(d time.Duration)
```

A constructor option / setter wires this; production uses `time.Sleep`.

## Acceptance mapping

| Test | Story | Asserts |
|------|-------|---------|
| `TestLogoutBlockedOnUnreachable` | US1 | fake `api` returns `ErrUnreachable` 3× → `LogoutBlocked`; keyring/state unchanged; `firewall.Clear` not called; `sleep` called 2×1s |
| `TestLogoutCleanRemovesAndRevokes` | US2 | real server: node deleted, keyring + state cleared → `LogoutDone`; RT revocation asserted **behaviorally** — a post-logout `apiclient.Refresh()` with the pre-logout RT fails (harness exposes no store) |
| `TestLogoutAlreadyAbsentIsDone` | US2 | node missing (404 / not in list) → `LogoutDone`, not blocked |
| `TestLogoutNeedSignInOnRefreshFail` | US2/edge | expired session + failing refresh → `LogoutNeedSignIn`, no local change |
| `TestForceLogoutClearsLocal` | US3 | `ForceLogout()` with unreachable server → all local cleared, returns to wizard |
| `TestLogoutRetryBoundedThenSucceeds` | US1 | `ErrUnreachable` twice then success → `LogoutDone`, `sleep` called 2× |
