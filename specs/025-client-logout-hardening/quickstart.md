# Quickstart: Client Logout Hardening

Two verification surfaces: **headless** (automated, the bulk of coverage) and
**GUI** (manual, the two-button dialog on the Mesa-OpenGL Windows VM).

## Part A — Headless acceptance (automated)

The `panel.Controller` logout flow is tested against a **real** `apiclient.Client`
over an `httptest` server (and a fake `api` boundary for the pure retry/branch
timing). Loopback must be UP inside the netns:

```sh
unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/panel/... ./internal/client/apiclient/...'
```

Expected: all green. Key cases (see `contracts/logout-controller.md`):

1. **US1 — block on unreachable**: fake `api.DeleteNode` returns
   `apiclient.ErrUnreachable` three times → `Logout()` returns `LogoutBlocked`;
   assert the keyring entries (`SessionTokenName`, `RefreshTokenName`,
   `DeviceKeyName`) and the state file are unchanged and `firewall.Clear` was
   never called; assert the injected `sleep` seam was called exactly twice with
   `1 * time.Second` (no real sleeping).
2. **US2 — clean logout**: against a real server with a registered node, `Logout()`
   deletes the node, calls `POST /api/v1/logout`, and clears local material →
   `LogoutDone`. Verify server-side the node is gone and the device's
   `refresh_tokens` row has `revoked_at` set.
3. **US2 — already absent**: with no matching node (404 / not in list), `Logout()`
   returns `LogoutDone` (treated as success), not blocked.
4. **Edge — 401 then refresh fails**: expired session and a failing refresh →
   `LogoutNeedSignIn`, no local change.
5. **US3 — force logout**: `ForceLogout()` against an unreachable server clears all
   local material and returns to the wizard path (orphan accepted).
6. **Retry bound**: `ErrUnreachable` twice then success → `LogoutDone`, `sleep`
   called twice.

Full suite + vet + fmt before commit:

```sh
gofmt -l internal/client
go vet ./...
unshare -rUn bash -c 'ip link set lo up && go test ./...'
```

## Part B — GUI two-button dialog (manual, Mesa-OpenGL VM)

On the Hyper-V Win11 test VM with `opengl32.dll` from mesa-dist-win present
(see memory: GUI test VM needs Mesa OpenGL), build the `gui`-tagged client and:

1. **Reachable server**: sign in, connect, click **Log out** → confirm. Expect the
   infinite-progress indicator briefly, then the app returns to the wizard. On the
   server, confirm the node is gone and the device's refresh token shows revoked.
2. **Unreachable server (block)**: with the device signed in + connected, make the
   control API unreachable (stop the server, or block its endpoint at the host
   firewall — the tunnel state is irrelevant). Click **Log out** → confirm. Expect
   ~3 s of progress (3 attempts × 1 s), then a **two-button** prompt:
   *Cancel* (default) / *Force log out anyway*.
   - Choose **Cancel** → app stays on the panel, still connected, still signed in;
     re-opening the menu and logging out again works.
   - Re-trigger and choose **Force log out anyway** → tunnel disconnects, firewall
     rule (if any) is removed, local credentials cleared, app returns to the
     wizard. The server still lists the (now orphaned) node — expected.
3. **i18n**: switch language to English and repeat step 2; confirm the prompt
   title, body, and both buttons render correctly in English, then in 简体中文.

## Notes

- The control API is reached over the public HTTPS endpoint independent of the
  tunnel, so the remote node removal happens **before** the tunnel is torn down.
- Blocking is triggered **only** by network-layer unreachability
  (`apiclient.ErrUnreachable`); a 5xx or a changed certificate does **not** block
  (falls back to the 017 "clear local + warn residue" path).
