# Quickstart: Windows Client Tunnel

**Feature**: 010-windows-client-tunnel | **Date**: 2026-06-05

Validates that Connect brings a real tunnel up (handshake + server reachable) and Disconnect
tears it down. Builds on 009 (a set-up device with its key + setup record).

## Automated checks (build host)

```bash
# Host-agnostic profile builder + state machine.
go test ./internal/client/tunnel/...

# Real user-space client tunnel ↔ real server: handshake + ping 100.127.0.1.
unshare -rUn go test ./internal/client/tunnel/... -run Integration
```

Unprivileged hosts skip the privileged integration via `testutil.RequireNetAdmin`. The Fyne
Connect control and the Windows WinTun/UAC path build/validate on the desktop toolchain.

## Scenario A — connect and reach the server (US1, automated, privileged)

The integration test, without a GUI:

1. Stand up a real server (kernel-WireGuard + nftables harness) over a real listen port;
   register a device peer (address `100.127.0.2`).
2. Build the tunnel profile from that device's key + a record whose endpoint is the server's
   real `127.0.0.1:<port>`; bring up a real user-space tunnel on a Linux `tun`.
3. Assert the WireGuard handshake with the server completes (`State()==Connected`, driven by
   the device's `last_handshake_time>0`) — the reliable proof of reachability; `ping
   100.127.0.1` is attempted as a best-effort extra check (skipped if `ping` is unavailable).

## Scenario B — disconnect tears down (US2, automated, privileged)

From Scenario A's connected state, Disconnect → assert the device/address is gone and
`100.127.0.1` no longer answers; a second Disconnect is a no-op.

## Scenario C — state machine & failures (US3, automated, non-privileged)

- `Connect` against an unreachable endpoint → `Connecting` then `ErrServerUnreachable`, back
  to `Disconnected` (no orphan).
- A profile assembled from an incomplete record fails with a clear error.
- State transitions are observable and consistent; a second Connect while connected is a
  no-op.

## Scenario D — Windows adapter, split tunnel, UAC, same-zone (manual, target OS)

On Windows with the built client:
- Click Connect → accept the UAC prompt → `ipconfig` shows the `100.127.x.y` adapter; `ping
  100.127.0.1` works; normal internet still works (split tunnel); deny UAC → clear message,
  stays disconnected.
- With a second device joined to the same zone (feature 005) and connected, ping its
  `100.127.x.y` address.
- Click Disconnect → the adapter disappears; close the app while connected → no adapter is
  left behind.

## Success

- Scenarios A–C pass automatically (A/B privileged; C non-privileged).
- Scenario D passes by manual inspection on Windows (WinTun + UAC — the documented manual
  exception).
