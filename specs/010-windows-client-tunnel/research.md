# Research: Windows Client Tunnel

**Feature**: 010-windows-client-tunnel | **Date**: 2026-06-05

The open questions are about the embedded VPN engine and how to test a real tunnel
headlessly. Decisions below resolve them; none remain as NEEDS CLARIFICATION.

## Decision 1 — Embedded user-space WireGuard (wireguard-go)

- **Decision**: Drive the tunnel with `golang.zx2c4.com/wireguard` (`device`, `conn`,
  `tun`), already an indirect dependency via `wgctrl`. The client creates a `tun` device,
  a `device.Device`, applies the configuration via the device's UAPI (`IpcSet`), and brings
  the address up.
- **Rationale**: DESIGN §9.1 mandates "嵌入 wireguard-go + WinTun". A pure-Go user-space
  engine ships in one binary (with `wintun.dll`), works without a kernel module on Windows,
  and — crucially — can be brought up on the Linux dev host for a real, automated handshake
  test. It is the same WireGuard the server speaks, so interop is guaranteed.
- **Alternatives considered**:
  - *Kernel WireGuard via wgctrl on the client*: not available on Windows without a kernel
    module; defeats the single-binary goal. Rejected.
  - *Shell out to the `wireguard.exe` tooling*: external dependency, fragile packaging.
    Rejected.

## Decision 2 — Profile = UAPI config built host-agnostically from the record + key

- **Decision**: A pure `profile` builder turns `state.Record` + the device private key into
  the wireguard-go UAPI configuration: `private_key` (hex), one peer with `public_key` (hex)
  = the server key, `endpoint` = the recorded server endpoint, `allowed_ip` = the recorded
  network range (split tunnel), and `persistent_keepalive_interval = 25`. WireGuard keys are
  stored base64 (wgtypes) and converted to the hex the UAPI expects.
- **Rationale**: The configuration assembly is the behavior-bearing, host-agnostic part; as
  a pure function it is fully unit-testable everywhere (no device, no OS). It encodes the
  split-tunnel and keepalive requirements (FR-004/FR-006) declaratively.
- **Alternatives considered**:
  - *Configure the device with `wgctrl`-style structs*: wireguard-go's in-process device is
    configured by the UAPI text protocol; building that string directly is simplest and
    unit-testable. Rejected the indirection.

## Decision 3 — Split tunnel and keepalive (DESIGN §6)

- **Decision**: `AllowedIPs = <network>` only (e.g. `100.127.0.0/16`), so only VPN-range
  traffic is routed; `PersistentKeepalive = 25 s`.
- **Rationale**: Matches DESIGN §6 — the user's normal internet is unaffected (FR-004), and
  an idle connection stays alive and visible as online (FR-006, ties to feature 007).
- **Alternatives considered**: full tunnel (`0.0.0.0/0`) — out of scope and contrary to the
  split-tunnel design. Rejected.

## Decision 4 — OS-specific adapter addressing behind build tags

- **Decision**: After the device is configured, assign the device address and route the VPN
  range using OS-specific, build-tagged files: `addr_linux.go` (netlink — reused from the
  server's approach) for the dev-host integration test; `addr_windows.go` (WinTun adapter
  addressing via `netsh`/`winipcfg`) for production.
- **Rationale**: Adapter addressing is the only genuinely OS-specific step; isolating it
  keeps the engine and profile portable and lets the Linux path run the automated test while
  the Windows path is validated on the target. Reusing netlink mirrors the server.
- **Alternatives considered**: a cross-platform addressing lib — none fits both WinTun and
  Linux tun cleanly; build tags are clearer. Rejected.

## Decision 5 — Connection state machine

- **Decision**: The engine exposes `Connect`, `Disconnect`, and an observable `State`
  (`Disconnected` → `Connecting` → `Connected`, and back to `Disconnected` on failure or
  teardown), guarded for concurrent access. Connect reaches `Connected` only once a handshake
  with the server succeeds (observed via the device's UAPI handshake time); a timeout returns
  a typed error and tears down.
- **Rationale**: FR-008 requires the shown state to match reality, and FR-001/FR-011 require
  a clean connecting→connected/failed flow. Driving `Connected` off a real handshake (not
  just "device created") makes the status honest.
- **Alternatives considered**: report connected as soon as the adapter exists — would lie
  about reachability. Rejected.

## Decision 6 — Real-tunnel integration test (no mocking)

- **Decision**: Under `unshare -rUn` (`RequireNetAdmin`), stand up the real server (existing
  kernel-WG + nftables harness), register the device's peer, then bring up a **real**
  user-space client tunnel (wireguard-go on a Linux `tun`), apply the profile (endpoint =
  the server's real listen port on `127.0.0.1`). The **primary reachability assertion is the
  completed WireGuard handshake** (the device's UAPI `last_handshake_time>0`): a completed
  handshake means encrypted packets flowed both ways, which *is* reachability at the tunnel
  layer. An ICMP `ping 100.127.0.1` is attempted as a **best-effort** confirmation and is
  skipped (not failed) when the `ping` tool is unavailable in the netns. Then Disconnect and
  assert the device/interface is gone.
- **Rationale (resolves analyze M1)**: This exercises the genuine WireGuard boundary end to
  end (Principle II) and mirrors the ROADMAP acceptance ("ping 服务器 100.127.0.1") without
  making the automated test hostage to the presence/behavior of the `ping` binary in a
  rootless network namespace. The handshake-completion signal is the reliable, deterministic
  proof of reachability; ping remains as a human-recognizable extra check.
- **Alternatives considered**:
  - *Assert only that the device was created*: proves nothing about reachability. Rejected.
  - *Two full clients pinging each other (same-zone)*: valuable but heavier; the
    server-reachability test already proves the tunnel works, and same-zone reachability is
    a manual quickstart scenario (needs zone setup + a second tunnel). Deferred to manual.

## Decision 7 — Elevation (UAC) and adapter lifecycle on Windows

- **Decision**: Creating the WinTun adapter requires administrator rights; the Windows build
  requests elevation (an app-manifest `requireAdministrator`, or a relaunch-as-admin on
  Connect). A denied elevation surfaces a clear error and stays disconnected. The user-space
  device runs in-process, so app exit (or `device.Close`) tears the adapter down; a leftover
  adapter from a prior crash is removed/reused on the next Connect.
- **Rationale**: FR-007/FR-012 — admin is the minimum needed and only for adapter creation;
  in-process lifecycle gives clean teardown for free. This path is Windows-specific and
  validated manually.
- **Alternatives considered**: a persistent privileged background service — out of scope for
  v1 (no background mode); manual connect keeps the model simple. Rejected for now.

## Decision 8 — Build isolation (keep the headless build green)

- **Decision**: `profile.go` and the state machine are pure and untagged (build/test
  everywhere). The engine + `addr_linux.go` + the integration test build on Linux. The Fyne
  Connect control stays behind `//go:build gui`; `addr_windows.go` behind `//go:build
  windows`. The headless `go build ./...` / `go test ./...` continue to exclude the GUI and
  Windows-only files, as in 009.
- **Rationale**: Preserves the green headless server build/test while adding the client
  tunnel, and keeps the OS/GUI specifics off the CI path.

## Resolved unknowns summary

| Topic | Resolution |
|-------|------------|
| VPN engine | wireguard-go user-space (`device`/`conn`/`tun`), one binary + `wintun.dll` |
| Config format | UAPI string built from `state.Record` + key (base64→hex), keepalive 25, allowed-ip=network |
| Split tunnel | `AllowedIPs = <network>` only |
| Adapter addressing | netlink (Linux/test) vs netsh/winipcfg (Windows), build-tagged |
| "Connected" definition | real handshake observed via device UAPI handshake time |
| Real test | user-space client tunnel ↔ real kernel-WG server under `unshare`, handshake + ping 100.127.0.1 |
| Elevation/adapter | Windows admin manifest / relaunch; in-process teardown; manual validation |
