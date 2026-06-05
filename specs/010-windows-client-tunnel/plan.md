# Implementation Plan: Windows Client Tunnel

**Branch**: `010-windows-client-tunnel` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/010-windows-client-tunnel/spec.md`

## Summary

Give the set-up client a Connect / Disconnect control that brings the VPN tunnel up and
down. A **host-agnostic profile builder** assembles the tunnel configuration from the
device's stored private key plus the recorded server details (server public key, endpoint,
network range, device address) — split-tunnel (route only the VPN range) with a 25 s
keepalive. A **tunnel engine** drives an embedded user-space WireGuard implementation: it
creates the virtual adapter, applies the profile, brings the device's address up, and tears
everything down on Disconnect or app exit. OS-specific adapter addressing and the elevation
prompt are isolated; the engine's handshake-and-reachability is integration-tested for real
against the existing server, while the Windows WinTun/UAC path is validated manually.

## Technical Context

**Language/Version**: Go 1.26 (module `lanweave`, shared with the server and 009 client).

**Primary Dependencies**:
- User-space WireGuard: `golang.zx2c4.com/wireguard` (`device`, `conn`, `tun`) — already an
  indirect dependency via `wgctrl`; promoted to direct.
- Virtual adapter: WinTun on Windows (via the `tun` package's Windows backend + the bundled
  `wintun.dll`); the Linux `tun` backend is used for the automated integration test.
- Addressing/routing: `github.com/vishvananda/netlink` on Linux (already used by the
  server); Windows addressing via a windows-tagged adapter file (`netsh`/`winipcfg`).
- Reuses 009: `internal/client/state` (the setup record) and `internal/client/keyring` (the
  device private key); `internal/client/ui` (Fyne) gains the Connect control.

**Storage**: None added. The tunnel is assembled at connect time from the existing state
record + the secret in the OS store. No server change.

**Testing**: `go test`. Unit (non-privileged): the profile builder (record + key → tunnel
config, key encoding, allowed-IP = network, keepalive = 25) and the connection state
machine. Integration (privileged, `unshare -rUn`, `RequireNetAdmin`): bring up a **real**
user-space client tunnel and connect it to a **real** server (the existing kernel-WireGuard
+ nftables harness); register the device's peer; assert a real handshake completes and the
server's VPN address (`100.127.0.1`) is reachable; then disconnect and assert teardown.
Acceptance/smoke: the built Windows client — adapter creation, UAC elevation, split-tunnel,
and same-zone reachability are validated manually on Windows.

**Target Platform**: Windows desktop (production); the host-agnostic core + the user-space
tunnel integration test run on the Linux dev host.

**Project Type**: Extends the existing client (009) in the same Go module.

**Performance Goals**: Connect to reachable server ≤ 5 s (SC-001); disconnect/teardown
within a couple of seconds (SC-004); first handshake ≤ 3 s (constitution §IV); idle
connection stays up via keepalive (SC-003).

**Constraints**: One machine = one device = one tunnel. Split tunnel (only the VPN range is
routed). Private key never leaves the machine or appears in logs. Adapter creation needs
admin (elevation). Clean teardown — no orphaned adapter.

**Scale/Scope**: A single tunnel with one peer (the server), hub-and-spoke.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**: New `internal/client/tunnel` package with a clear split: a pure
  profile builder (no engine/OS deps) and the engine lifecycle; OS-specific addressing in
  build-tagged files. Reuses existing `state`/`keyring`/`netlink`. No premature abstraction
  beyond the engine seam needed for testing. Errors as values; typed connection errors.
  `gofmt`/`vet` clean on the host-buildable packages. **PASS**
- **II. Testing Standards (NON-NEGOTIABLE)**: WireGuard is **not** mocked — the integration
  tier stands up a real user-space client tunnel and connects it to the real kernel-WG
  server, asserting a genuine handshake and reachability to `100.127.0.1`. The profile
  builder and state machine are unit-tested. Each user story gets coverage (US1 connect +
  reachability; US2 disconnect/teardown; US3 failure/elevation surfaced via typed errors +
  status). **The Windows WinTun adapter, routing, and UAC elevation are validated manually
  on Windows** — a documented, unavoidable OS-driver/UAC exception (consistent with 009),
  recorded in Complexity Tracking; all host-agnostic and handshake-bearing logic is
  automated. **PASS with documented exception.**
- **III. User Experience Consistency**: One Connect/Disconnect control plus an always-visible
  connection-state indicator that matches reality (FR-008); elevation is explained and a
  denial leaves a clear, recoverable state (FR-007/FR-011); the address renders as
  `100.127.x.y`. These trace to the constitution's connection-state-visible and
  human-readable-errors rules. **PASS**
- **IV. Performance Requirements**: First handshake ≤ 3 s and connect ≤ 5 s are met by a
  direct user-space WireGuard bring-up; keepalive 25 s keeps idle connections alive within
  the online-status budget (feature 007). **PASS**
- **Security & Operational Discipline**: The private key is read from the OS store only at
  connect time, fed straight into the engine, and never logged or written to a file. Split
  tunnel limits exposure to the VPN range. Admin is requested only for adapter creation
  (minimum needed). **PASS**

One documented exception (WinTun/UAC manual validation) recorded in Complexity Tracking; no
principle diluted.

## Project Structure

### Documentation (this feature)

```text
specs/010-windows-client-tunnel/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── tunnel-profile.md     # the assembled tunnel configuration + state machine contract
└── checklists/
    └── requirements.md
```

### Source Code (repository root)

```text
internal/client/
├── tunnel/                       # NEW tunnel engine
│   ├── profile.go                # host-agnostic: state.Record + private key → tunnel config (UAPI), keepalive=25, allowed-ip=network
│   ├── profile_test.go           # unit: config assembly, key encoding, split-tunnel, keepalive (non-priv)
│   ├── tunnel.go                 # engine: Connect/Disconnect/State over the user-space WG device; state machine; teardown
│   ├── tunnel_test.go            # unit: state-machine transitions + error surfacing (non-priv, no real device)
│   ├── addr_linux.go             # //go:build linux — assign address + route via netlink (used by the integration test)
│   ├── addr_windows.go           # //go:build windows — WinTun adapter addressing/route (netsh/winipcfg)
│   └── tunnel_integration_test.go# //go:build linux — privileged: real user-space tunnel ↔ real server, handshake + ping
├── ui/                           # 009 Fyne shell (gui-tagged) — gains the Connect control
│   ├── home.go                   # //go:build gui — home with Connect/Disconnect + status (replaces home_placeholder)
│   └── ...                        # wizard.go unchanged
└── (state, keyring, apiclient, onboard, wgkey from 009 — unchanged)

cmd/lanweave-client/main.go       # //go:build gui — wire the tunnel engine into the home view
```

**Structure Decision**: The profile builder and state machine are framework- and
OS-agnostic and unit-tested on the host; the engine + Linux addressing run the privileged
integration test on the host; Windows addressing + the Fyne control are isolated behind
`windows`/`gui` build tags. No server code changes.

### Connection sequence (reference for tasks)

1. Read the device private key from the OS store and the setup record from disk.
2. Build the tunnel profile: device address; one peer = server public key + endpoint, with
   `AllowedIPs = network` (split tunnel) and `PersistentKeepalive = 25`.
3. Create the virtual adapter (admin/elevation on Windows); create the user-space WG device;
   apply the profile; bring the device's address up and route the VPN range.
4. Reach connected state once the handshake with the server succeeds; surface status.
5. Disconnect / app exit → bring the device down, remove the adapter, return to disconnected
   (idempotent; no orphan).
6. On failure (unreachable server, elevation denied, adapter error) → typed error, clean
   teardown, back to disconnected (retryable).

## Complexity Tracking

> One documented exception (WinTun adapter + UAC manual validation under Principle II),
> recorded per the constitution's process. No principle diluted.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| The Windows virtual-adapter (WinTun), routing, and UAC elevation are validated manually on Windows rather than by automated tests | These are OS-driver and OS-privilege behaviors with no headless equivalent on the Linux build host; the handshake-and-reachability that proves the tunnel works is automated against a real server via a user-space tunnel | A Windows CI runner is unavailable for v1; faking the adapter/driver would prove nothing about WinTun or UAC and would violate the no-mocking-of-real-boundaries stance |
