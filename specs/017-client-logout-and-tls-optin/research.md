# Phase 0 Research: Client Logout and TLS Opt-In

**Feature**: 017-client-logout-and-tls-optin | **Date**: 2026-06-06

The spec left two items "for the plan to confirm" (both turned out to already exist) plus
several design decisions about how to wire logout and the reactive insecure opt-in into the
current client. Each is resolved below.

## Decision 1 — Server-side device removal already exists with ownership enforcement

- **Decision**: Reuse the existing `DELETE /api/v1/nodes/{id}` endpoint for logout's
  deregistration. No server change.
- **Rationale**: `internal/server/api/router.go:58` registers
  `DELETE /api/v1/nodes/{id}` behind `AuthRequired`. The handler
  (`node_handlers.go:111 deleteNode`) resolves the node via
  `store.Nodes().GetOwned(userID, nodeID)` and deletes via `DeleteOwned(userID, nodeID)` — both
  scoped to the caller's `userID`, so a user can only delete their own node (a foreign or
  unknown id returns 404, never another user's node). On delete it removes the WireGuard peer
  and clears the node's address from every zone nft set (best-effort; startup rebuild
  reconciles). Returns `204 No Content`. This is exactly logout's required server behavior.
- **Alternatives considered**: A dedicated `POST /logout` endpoint — rejected: logout is a
  pure client-side concept (clear local credentials); the only server effect we want is node
  removal, which the existing endpoint already provides with the correct authorization.

## Decision 2 — Tunnel teardown is an existing idempotent call

- **Decision**: Logout tears the data plane down with `tunnel.Tunnel.Disconnect()`.
- **Rationale**: `internal/client/tunnel/tunnel.go:159` `Disconnect()` calls `teardown()`,
  which is idempotent (a no-op when already `Disconnected`). This cleanly covers the
  "logout while disconnected" edge case (FR; no error on absent connection). The control-plane
  API client reaches the server over its public HTTPS endpoint, **not** through the WG tunnel,
  so tearing the tunnel down does not break the `DeleteNode` call — order between them is
  irrelevant to connectivity.
- **Alternatives considered**: Skipping teardown and letting app-exit `Close()` handle it —
  rejected: logout returns the user to setup without exiting, so a lingering adapter would be
  visibly wrong (FR-003).

## Decision 3 — Logout always clears local state; remote removal is best-effort (FR-008)

- **Decision**: `panel.Controller.Logout() (remoteRemoved bool, err error)`. It (a) lists the
  caller's nodes to find this machine's id and calls `DeleteNode`; (b) **always** clears the
  local session token, the device private key, and the state record. `remoteRemoved` reports
  whether the server removal completed; `err` is non-nil only when a *local* clear fails.
  - `ListNodes` network failure → `remoteRemoved=false` (server unreachable).
  - `ListNodes` ok but this machine absent → already gone → `remoteRemoved=true`.
  - this machine present + `DeleteNode` ok → `true`; `DeleteNode` fails → `false`.
- **Rationale**: FR-008 requires logout to complete locally even when the server is
  unreachable, and to warn that the remote node may linger. Returning a boolean keeps the UI's
  warning decision simple and keeps the orchestration headless-testable with a fake `api`.
- **Alternatives considered**: Abort logout when the server can't be reached — rejected: it
  traps an offline user in a session they want to leave. Leaving the node on the server by
  design — rejected at the spec stage (would accumulate unremovable orphans now that this
  feature adds no separate delete-node UI).

## Decision 4 — Logout lives in the controller; the tunnel + navigation live in the UI

- **Decision**: `Controller.Logout` does the server call + local credential/state clears (it
  already holds `keys` and will hold `statePath`). The Fyne layer (`ui.Panel`) tears the
  tunnel down and, after `Logout` returns, navigates back to a fresh wizard at the server-URL
  step via an injected `restart func()` callback.
- **Rationale**: Keeps the destructive sequence in the Fyne-free, headless-tested controller
  (Principle II), while the GUI-only concerns (tunnel handle, window navigation) stay in the
  `//go:build gui` layer. `ui.Panel` has no tunnel→wizard path today (main.go decides once at
  startup); the smallest seam is a `restart` closure that both construction sites
  (`main.go` Home branch and `wizard.showHome`) already have the inputs to build:
  `func(){ NewWizard(win, statePath, keys, cliInsecure).Start() }`.
- **Alternatives considered**: A global app-navigator/state-machine object — rejected as
  premature abstraction (Principle I) for a single back-edge. Putting tunnel teardown inside
  the controller — rejected: the controller has no business depending on the data plane.

## Decision 5 — `restart` after logout uses the original CLI insecure flag, not the opted-in value

- **Decision**: The `restart` closure passes the process's original `--insecure` CLI value
  into the new wizard, **not** any insecure state the user opted into during the session.
- **Rationale**: FR-012 — insecure acceptance is per-session and never persisted. The safest
  reading is that switching accounts/servers via logout re-verifies by default unless the
  process was explicitly launched insecure. Avoids a "sticky insecure" that silently follows
  the user onto a different server.
- **Implementation note**: because a wizard's live `z.insecure` may have been flipped `true` by
  an opt-in (Decision 6), the wizard must retain the **original** CLI value separately (e.g. a
  second immutable field set in `NewWizard`) and pass *that* into the `restart` closure — not the
  flipped live value. `main.go`'s Home branch already has the CLI flag in scope directly.

## Decision 6 — Reactive insecure opt-in by rebuilding the client (insecure is fixed at construction)

- **Decision**: Treat "go insecure" as **rebuilding** the API client with
  `apiclient.WithInsecure()`, not flipping a field on the live client.
  - **Wizard**: `Wizard.insecure` is already a mutable field read when it builds clients in
    `runProvision`/`showHome`. On an `ErrUntrustedCert` from `runProvision`, show the opt-in
    dialog; on accept set `z.insecure = true` and re-run `runProvision` (which rebuilds the
    client insecure).
  - **Panel/Home**: add `Controller.UseInsecureClient(a api)` — the UI builds a bare insecure
    client (`apiclient.New(rec.ServerURL, apiclient.WithInsecure())`) and hands it in; the
    controller swaps `c.api`, sets `c.insecure = true`, and re-applies the cached token from
    the keyring. The UI then retries the failed operation.
- **Rationale**: `apiclient.New` bakes `InsecureSkipVerify` into the `http.Client` TLS config
  at construction (`client.go:70`); there is no way to flip it afterward. Rebuilding is the
  honest model. Handing the rebuilt client to the controller as the `api` interface keeps the
  controller test seam intact (a test passes a fake; production passes the real insecure
  client). The controller re-applies the token so the UI need not touch credentials.
- **Alternatives considered**: A shared mutable "session insecure" pointer threaded everywhere
  + a client factory — rejected as heavier than needed for two call sites. Making `apiclient`
  support runtime toggling of `InsecureSkipVerify` — rejected: it would require swapping the
  `http.Transport` under the hood and invites a half-verified client state.

## Decision 7 — Insecure state is reported for the persistent indicator (FR-013/FR-014)

- **Decision**: `Controller` carries an `insecure bool` (set at construction from the CLI flag,
  or flipped by `UseInsecureClient`) exposed via `Insecure() bool`; `ui.Panel` renders a
  persistent "⚠ certificate not verified" label in the top status area whenever it is true. The
  wizard renders the same warning line while `z.insecure` is true. `apiclient` also gains
  `Insecure() bool` for completeness.
- **Rationale**: FR-013 requires the indicator whenever verification is bypassed, and FR-014
  requires it for the CLI-flag entry point too. Sourcing it from a single `insecure` field that
  both entry points set keeps the indicator correct in every case.

## Decision 8 — No persistence / schema changes

- **Decision**: No SQLite migration, no `state.Record` schema bump (`SchemaVersion` stays 1),
  no new persisted field. The session insecure choice lives only in memory; logout *removes*
  local records (`state.Clear`, keyring deletes) rather than adding any.
- **Rationale**: Principle I (SQLite is the single source of truth; no hidden runtime state
  that must persist). The only new state is the in-memory per-session insecure flag, which by
  spec must not survive a restart.

## Decision 9 — `DeleteNode` on the API client + the panel `api` interface

- **Decision**: Add `func (c *apiclient.Client) DeleteNode(nodeID int64) error` issuing
  `DELETE /api/v1/nodes/{id}` (auth, expects `204`, maps non-2xx through the existing
  `mapError`). Add `DeleteNode(nodeID int64) error` to the `panel.api` interface and the test
  `fakeAPI`.
- **Rationale**: The client has no delete-node method today (`apiclient/client.go`); logout is
  its only caller. Routing it through the existing `do`/`mapError` path keeps error handling
  uniform.
- **Alternatives considered**: Also exposing `DeleteNode` on the `onboard.apiClient`
  interface — rejected: onboarding never deletes; only the panel controller needs it.

## Decision 10 — DESIGN.md amendment (Principle: DESIGN authority + §11 register)

- **Decision**: Amend `DESIGN.md` §275 and §360 (§11 accepted-risks register) in the same PR.
  §275 currently says certificate-skip is "CLI flag only, never in the UI"; §360 logs the risk
  as "CLI only, not in UI". The new wording keeps the intent (prevent mindless toggling) while
  permitting the **reactive** UI opt-in: only on a real verification failure, explicit
  confirmation, per-session/not persisted, with a persistent warning indicator; the `--insecure`
  CLI flag is retained.
- **Rationale**: Constitution "DESIGN.md authority" — a spec may not contradict DESIGN without
  amending it in the same PR; and `DESIGN.md §11` is the only place a project-wide security risk
  may be accepted. Exposing insecure in the UI is such a risk and must be re-registered with its
  mitigations.
