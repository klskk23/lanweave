# Quickstart: Client Logout and TLS Opt-In

**Feature**: 017-client-logout-and-tls-optin

This feature's acceptance has two layers: **automated** (headless unit + real-server
integration, run in CI) and **manual** (the Fyne dialogs/indicator on a Windows desktop, per the
project's standing GUI exception). Use this as the verification procedure referenced by `spec.md`
(US1/US2) and `plan.md` (Constitution II).

## Automated checks (run in the CI test gate)

```bash
# Full headless + integration gate (real SQLite/nftables/wireguard-go inside the netns):
unshare -rUn bash -c 'ip link set lo up && go test ./...'

# Focused:
unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/panel/... ./internal/client/apiclient/...'
```

What these cover:

- **`apiclient.DeleteNode`** (`client_test.go`, httptest): a `204` returns `nil` and the wire
  request is `DELETE /api/v1/nodes/{id}` with the bearer token; a non-2xx maps to an error.
- **`panel.Controller.Logout`** (unit, fake `api`): with this machine's node present and
  `DeleteNode` ok → `remoteRemoved=true`; with `DeleteNode` failing → `remoteRemoved=false`; in
  both cases the keyring session token, the device key, and `state.json` are cleared.
- **`panel.Controller.UseInsecureClient` / `Insecure()`** (unit, fake `api`): the client is
  swapped, `Insecure()` flips true, and the cached token is re-applied.
- **Logout integration** (`panel_integration_test.go`, real server): onboard a node, then
  `Logout()` → the node is gone from `ListNodes`, its WireGuard peer is removed, and its address
  is gone from any zone set; local key + state cleared. This is the US1 end-to-end acceptance
  test (Principle II — no mocked SQLite/nftables/WireGuard).

## Manual verify matrix (Windows desktop) — the GUI acceptance gate

Build/install the client (per release pipeline or local `go build -tags gui ...`). Stand up a
test server; for the insecure rows use a server with a **self-signed** certificate.

| # | Surface | How to check | Pass criterion | Spec ref |
|---|---------|--------------|----------------|----------|
| 1 | Logout button present | Open the running panel (already set up) | A "Log out" control is visible, away from the connect/zone primary controls | US1 / FR-001 |
| 2 | Logout confirmation | Click Log out | A confirm dialog **names this device + server** and states it will disconnect, remove this device's node, and require re-entering the server address; nothing happens until confirmed | US1 / FR-002 |
| 3 | Logout effect (connected) | Connect, then confirm Log out | Tunnel drops (`ipconfig` adapter gone); app returns to the **server-URL** step | US1 / FR-003,006 |
| 4 | Server node removed | After #3, on another signed-in client (or via server) list nodes | The logged-out device's node is gone server-side | US1 / FR-004, SC-002 |
| 5 | Local credentials cleared | After #3, inspect: re-launch the client | App opens to the wizard (not Home); no auto sign-in; re-setup produces a **new** device identity | US1 / FR-005,007, SC-003,004 |
| 6 | Logout while offline | Stop the server, then Log out | Local logout still completes and returns to setup; an informational notice says the remote node may still be registered | US1 / FR-008 |
| 7 | Cert prompt appears | Point setup at a self-signed server; proceed to connect | An explicit "certificate could not be verified — continue insecurely?" prompt appears (no opaque failure, no silent connect) | US2 / FR-009,010 |
| 8 | Decline insecure | On the prompt, choose not to continue | No connection is made | US2 / FR-011 |
| 9 | Accept insecure + indicator | On the prompt, choose to continue | Connection proceeds and a persistent "⚠ certificate not verified" indicator is shown | US2 / FR-009,013, SC-005,006 |
| 10 | Not persisted | After #9, restart the client and connect again | Verification is enforced again by default (the prompt reappears; no silent insecure) | US2 / FR-012, SC-007 |
| 11 | CLI flag still works | Launch with `--insecure` against the self-signed server | Connects without the prompt, and the "not verified" indicator is still shown | US2 / FR-014 |
| 12 | No casual toggle | Browse the panel and wizard normally (trusted server) | There is no always-on insecure checkbox anywhere; the option only ever appears after a real cert failure | US2 / FR-010 |

## Done when

- The automated gate is green, including the logout integration test (US1) and the controller
  unit tests (`unshare -rUn ... go test ./...`).
- All 12 manual rows pass on a Windows desktop (US1 + US2 surfaces).
- `DESIGN.md` §275 and §360 are amended in the same PR to reflect the reactive UI opt-in, and the
  `docs/ROADMAP.md` 017 row is checked off in the merge commit.
- The non-graphical stub build still compiles: `CGO_ENABLED=0 go build ./cmd/lanweave-client`.
