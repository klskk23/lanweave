# Contracts: Client Logout and TLS Opt-In

**Feature**: 017-client-logout-and-tls-optin | **Date**: 2026-06-06

This feature introduces **no new server endpoint**. It consumes one existing endpoint and adds
client-internal Go interface surface. Both are documented here so `/speckit-tasks` can map work
to verifiable contracts.

## A. Reused external interface (server) — no change

### `DELETE /api/v1/nodes/{id}` (existing, feature 004)

- **Auth**: `AuthRequired` (bearer JWT). Registered at `router.go:58`.
- **Authorization**: caller may delete **only their own** node. The handler resolves via
  `store.Nodes().GetOwned(userID, id)` and deletes via `DeleteOwned(userID, id)`. A foreign or
  unknown id → `404 not_found` (never another user's node).
- **Side effects (already implemented + tested)**: release IP; remove the WireGuard peer; clear
  the node's address from every zone nft set; DB is authoritative, startup rebuild reconciles
  best-effort gaps.
- **Responses**: `204 No Content` on success; `404 {"error":"not_found"}` if the node isn't the
  caller's; `401 unauthorized` if the token is missing/expired.
- **Consumed by**: logout (the only new caller). No change to the endpoint or its tests.

## B. New client REST method

### `apiclient.Client.DeleteNode(nodeID int64) error`

- **Behavior**: issues `DELETE /api/v1/nodes/{id}` with the bearer token; returns `nil` on
  `204`; otherwise the error from the shared `mapError` (e.g. `ErrSessionExpired` on 401, a
  generic `ErrServer`-wrapped error on 404/5xx). Network/cert failures surface as `ErrUnreachable`
  / `ErrUntrustedCert` like every other call.
- **Contract test** (`client_test.go`, httptest): a `204` → `nil` and the request was
  `DELETE /api/v1/nodes/42` with `Authorization: Bearer <token>`; a non-2xx status → a non-nil
  mapped error.

### `apiclient.Client.Insecure() bool`

- Returns whether this client was built with verification disabled. Used for the persistent
  indicator (FR-013/FR-014).

## C. Client-internal interface deltas (`panel` package)

### `panel.api` interface — add one method

```go
DeleteNode(nodeID int64) error
```
`*apiclient.Client` satisfies it (compile-time `var _ api`); the test `fakeAPI` implements it and
records the id + a programmable error.

### `panel.New` — signature change

```go
func New(a api, record state.Record, keys keyring.Store, statePath string, insecure bool) *Controller
```
`statePath` lets `Logout` clear `state.json`; `insecure` seeds the indicator for the CLI-flag
case. Callers updated: `cmd/lanweave-client/main.go` (Home branch), `internal/client/ui/wizard.go`
(`showHome`), and the test helper `newController`.

### `panel.Controller.Logout() (remoteRemoved bool, err error)`

- **Postconditions (always)**: local session token deleted, device private key deleted, state
  record cleared. `err` is non-nil only if a local clear failed (errors joined).
- **`remoteRemoved`**: `true` if the server confirmed removal or the node was already absent;
  `false` if the server was unreachable or `DeleteNode` failed. Drives the UI's "may still be
  registered" notice (FR-008).
- **Does not** touch the tunnel (the UI tears it down) and does not navigate.
- **Unit test** (fake `api`): with a node present + `DeleteNode` ok → `remoteRemoved=true`,
  keyring + state cleared; with `DeleteNode` returning an error → `remoteRemoved=false` but local
  clears still happened.

### `panel.Controller.UseInsecureClient(a api)` and `Insecure() bool`

- `UseInsecureClient` swaps in the supplied (insecure) client, sets the controller's `insecure`
  flag, and re-applies the cached session token from the keyring so the session survives the swap.
- `Insecure()` reports the current value.
- **Unit test** (fake `api`): after `UseInsecureClient(fake2)`, subsequent calls route to `fake2`,
  `Insecure()` is `true`, and the cached token was applied to `fake2`.

## D. Client-internal UI seam (`ui` package, `//go:build gui`, manually verified)

### `ui.NewPanel(... , restart func())`

- Adds a `restart` callback the panel invokes after a completed logout to return to the wizard's
  first step. Production wiring: `func(){ NewWizard(win, statePath, keys, cliInsecure).Start() }`
  supplied by both `main.go` (Home branch) and `wizard.showHome`.
- Not unit-tested (GUI build tag); exercised via the manual matrix in `quickstart.md`.
