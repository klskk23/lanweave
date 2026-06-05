# Tasks: Windows Client Tunnel

**Feature**: 010-windows-client-tunnel | **Branch**: `010-windows-client-tunnel`
**Input**: Design documents in `/specs/010-windows-client-tunnel/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED per constitution Principle II. WireGuard is **not** mocked — the
integration tier brings up a real user-space client tunnel against the real kernel-WG
server and asserts a genuine handshake + reachability. The engine's `engine` seam (a small
in-package interface) is faked only for non-privileged state-machine unit tests; the real
wireguard-go-backed engine is what the integration test drives. The Windows WinTun adapter
and UAC elevation are validated manually on Windows (documented exception).

**Build isolation** (as in 009): `profile.go` and the state machine are pure/untagged
(build+test everywhere); the real engine + `addr_linux.go` + the integration test build on
Linux; `addr_windows.go` is `//go:build windows`; the Fyne Connect control is
`//go:build gui`. Default headless `go build ./...` / `go test ./...` stay green.

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`

---

## Phase 1: Setup

- [X] T001 Promote `golang.zx2c4.com/wireguard` to a direct dependency (it is the user-space WireGuard engine: `device`, `conn`, `tun`); run `go mod tidy`; confirm `go build ./...` still succeeds headless

---

## Phase 2: Foundational (block all stories)

- [X] T002 [P] Create `internal/client/tunnel/profile.go`: host-agnostic `BuildUAPIConfig(rec state.Record, privKeyBase64 string) (string, error)` producing the wireguard-go UAPI text — `private_key`/`public_key` as hex (decode base64 → hex), one peer = server key, `endpoint` = `rec.Endpoint`, single `allowed_ip` = `rec.Network` (split tunnel), `persistent_keepalive_interval=25`; validate that address/server-key/endpoint/network are present and well-formed (else a clear error)
- [X] T003 [P] Create the OS adapter-addressing seam: `internal/client/tunnel/addr_linux.go` (`//go:build linux`) implementing `configureAdapter(ifName string, ip netip.Addr, network netip.Prefix) error` and `teardownAdapter(ifName string) error` via `netlink` (assign the device address, route the VPN range, bring it up; teardown removes them); and `internal/client/tunnel/addr_windows.go` (`//go:build windows`) implementing the same two functions for the WinTun adapter (address + route via `netsh`/`winipcfg`)
- [X] T004 Create `internal/client/tunnel/tunnel.go`: the engine + state machine. Define an in-package `engine` interface (`up(uapiConfig string) error`, `handshaked() bool`, `close() error`) and a real implementation backed by wireguard-go (`tun.CreateTUN` → `device.NewDevice` → `IpcSet`); a `Tunnel` type with `Connect()`, `Disconnect()`, and an observable `State()` (`Disconnected`/`Connecting`/`Connected`) guarded by a mutex; `Connect` builds the profile (T002), creates the device, calls `configureAdapter` (T003), then waits for `handshaked()` before declaring `Connected`; second `Connect` while active is a no-op

**Checkpoint**: the engine compiles headless; profile + state machine are unit-testable in isolation.

---

## Phase 3: User Story 1 — 一键连接，加入网络 (P1) 🎯 MVP

**Goal**: Connect brings up a real tunnel and the server's VPN address becomes reachable.

**Independent test**: against a real server, connect a real user-space tunnel and confirm a
handshake completes and `100.127.0.1` answers.

- [X] T005 [P] [US1] Unit test `internal/client/tunnel/profile_test.go` (non-privileged): `BuildUAPIConfig` produces hex keys, exactly one `allowed_ip` equal to the network (never `0.0.0.0/0`), `persistent_keepalive_interval=25`, the server endpoint; and returns a clear error for an incomplete/invalid record
- [X] T006 [US1] Privileged integration test `internal/client/tunnel/tunnel_integration_test.go` (`//go:build linux`, `testutil.RequireNetAdmin`, `unshare -rUn`): stand up the real server (`wg.EnsureInterface` + `netfw`), register a device peer (`srv.AddPeer(pub, 100.127.0.2)`); read the server's real listen port (`wgctrl Device(name).ListenPort`); build a `state.Record` with `Endpoint=127.0.0.1:<port>`, `Network=100.127.0.0/16`, `IP=100.127.0.2`, `ServerPublicKey=<srv pub>`; `Connect()` a real `Tunnel`; **primary reachability assertion** = the WireGuard handshake completes (`State()==Connected`, driven by the device's UAPI `last_handshake_time>0`) — a completed handshake means bidirectional packets flowed, i.e. the server is reachable (M1); **then** attempt `ping -c1 -W2 100.127.0.1` as a best-effort confirmation that is skipped (not failed) when the `ping` tool is unavailable in the netns

**Checkpoint**: US1 proves a real client↔server tunnel + reachability (MVP).

---

## Phase 4: User Story 2 — 一键断开，干净退出 (P1)

**Goal**: Disconnect (and app exit) tear the tunnel down with no orphan.

**Independent test**: from connected, Disconnect → `100.127.0.1` unreachable and the adapter
is gone; a second Disconnect is a no-op.

- [X] T007 [US2] In `internal/client/tunnel/tunnel.go`, implement clean teardown: `Disconnect()` calls `teardownAdapter` + `engine.close()`, returns to `Disconnected`, is idempotent (no-op when already disconnected); ensure app-exit teardown via a `Close()`/`defer` path that removes the adapter
- [X] T008 [US2] Extend `internal/client/tunnel/tunnel_integration_test.go`: after Connect, `Disconnect()` → **primary teardown assertion** = `State()==Disconnected` and the tun device/interface is gone (look up the interface name → not found); a best-effort `ping 100.127.0.1` (if `ping` is available) now fails; calling `Disconnect()` again is a no-op (no error)

**Checkpoint**: clean, idempotent teardown verified on the real tunnel.

---

## Phase 5: User Story 3 — 连接状态清晰、提权可控、失败可恢复 (P2)

**Goal**: honest, observable state; typed/recoverable failures; elevation explained.

**Independent test**: connect to an unreachable endpoint → `ErrServerUnreachable` and back to
`Disconnected`; state transitions are observable; the home area shows the live state.

- [X] T009 [US3] In `internal/client/tunnel/tunnel.go`, add typed errors (`ErrServerUnreachable`, `ErrNoSetup`, `ErrAdapter`, `ErrElevationDenied`) and a connect timeout: if no handshake within the timeout, tear down and return `ErrServerUnreachable` (state back to `Disconnected`); a missing key/record → `ErrNoSetup`
- [X] T010 [P] [US3] Unit test `internal/client/tunnel/tunnel_test.go` (non-privileged) using a **fake `engine`** (never handshakes / programmable): assert `Disconnected→Connecting→Connected` on success, a connect timeout → `ErrServerUnreachable` + back to `Disconnected`, `Disconnect` idempotency, and a second `Connect` while active is a no-op — all without a real device
- [X] T011 [US3] Add the Fyne home with the connection control: `internal/client/ui/home.go` (`//go:build gui`) — a Connect/Disconnect button + an always-visible status label bound to `Tunnel.State()`, mapping typed errors to human-readable messages; wire it (and the engine) in `cmd/lanweave-client/main.go` (`//go:build gui`), including the Windows elevation request for adapter creation (manifest/relaunch, validated manually). Replace the 009 `home_placeholder.go` usage

**Checkpoint**: state is honest and observable; failures are typed and recoverable; the UI exposes Connect/Disconnect.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T012 [P] Run `gofmt -w`, `go vet`, and `staticcheck` on the host-buildable tunnel packages; confirm `go build ./...` (headless, no gui tag) succeeds and `go build -tags gui ./cmd/lanweave-client/` builds where the toolchain is available
- [X] T013 [P] Run `go test ./internal/client/tunnel/...` (non-privileged) and `unshare -rUn go test ./internal/client/tunnel/... -run Integration` (privileged); confirm the host-agnostic tunnel core (`profile` + state machine) reaches ≥ 70% coverage, noting the WinTun/UAC manual exception
- [X] T014 Validate quickstart Scenarios A–C automatically (A/B privileged, C non-privileged); record Scenario D (Windows WinTun adapter, split tunnel, UAC, same-zone reachability) as the manual target-OS check

---

## Dependencies & Execution Order

- **Setup (T001)** blocks everything.
- **Foundational (T002–T004)**: T002 (profile) ∥ T003 (addr) are independent; T004 (engine) needs both and defines the `engine` seam. Block all stories.
- **US1 (T005–T006)**: T005 needs T002; T006 needs T004 + the real engine + the server harness.
- **US2 (T007–T008)**: T007 extends `tunnel.go` (after T004); T008 extends the integration test (after T006). Depends on US1.
- **US3 (T009–T011)**: T009 extends `tunnel.go` (after T004/T007); T010 needs T004's `engine` seam (fake); T011 needs T004's `Tunnel` API. Depends on US1.
- **Polish (T012–T014)**: after all implementation/tests.

### File coordination (sequential within a file)

- `internal/client/tunnel/tunnel.go`: T004 → T007 → T009.
- `internal/client/tunnel/tunnel_integration_test.go`: T006 → T008.

## Parallel Execution Examples

- **Foundational**: T002 (`profile.go`) ∥ T003 (`addr_linux.go` + `addr_windows.go`).
- **US1**: T005 (`profile_test.go`) can run alongside T004's completion.
- **Polish**: T012 (lint/build) ∥ T013 (test/coverage).

## Implementation Strategy

**MVP** = Setup + Foundational + US1 (T001–T006): a real user-space client tunnel connects
to the real server and reaches `100.127.0.1`, proven end to end (privileged). US2 adds clean
teardown; US3 adds honest state, typed/recoverable failures, and the Connect/Disconnect UI.
The host-agnostic profile + state machine are fully automated; the Windows WinTun adapter and
UAC elevation are validated manually on Windows.
