# Implementation Plan: WireGuard Server Interface

**Branch**: `003-wireguard-server-interface` | **Date**: 2026-06-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-wireguard-server-interface/spec.md`

## Summary

At startup the relay brings up its data plane: load-or-generate a persistent
WireGuard server key (owner-only, never silently regenerated), create-or-adopt the
`wg-lanweave` interface addressed with the first pool IP and listening on the
configured port, enable IPv4 forwarding, and (re)build the `inet lanweave` nftables
table with a single default-drop forward chain and no rules. No client peers
(feature 004) and no zone rules (feature 005) are created. Setup is part of startup
and blocks serving on failure; a routine restart adopts the live interface rather
than tearing it down.

## Technical Context

**Language/Version**: Go 1.23+ (existing module `lanweave`)

**Primary Dependencies** (new vs inherited):
- **NEW** `golang.zx2c4.com/wireguard/wgctrl` (+ `.../wgctrl/wgtypes`) — generate keys and configure the WireGuard device (private key, listen port, peers) over netlink.
- **NEW** `github.com/vishvananda/netlink` — create/adopt the link (`type wireguard`), assign the address, bring it up.
- **NEW** `github.com/google/nftables` — build the `inet lanweave` table + forward chain over netlink (no dependency on the `nft` CLI binary, which may be absent even when `nf_tables` is loaded).
- Inherited from 001: config (`wireguard.*`, `nftables.table`, `data_dir`), structured logging with secret redaction, `app.Run` lifecycle.

**Storage**: No new SQLite tables. The server private key is a file at
`<data_dir>/wg_private` (owner-only). nftables/WireGuard state is derived (DB is the
source of truth; in this feature the derived state is empty).

**Testing**: Two tiers. **Unit (unprivileged, runs everywhere)**: key load/generate
+ permission enforcement, first-usable-address derivation, config→desired-state
mapping. **Integration (privileged, real kernel — NOT mocked)**: interface
create/adopt + address + wgctrl config, nft table build, ip_forward toggle. The
privileged tests run for real under root or a rootless user+net namespace
(`unshare -rUn`) and **skip with a clear message** when neither is available.

**Target Platform**: Linux, kernel WireGuard + nftables, x86-64 (Debian/Ubuntu).

**Project Type**: Single Go project — adds server-side data-plane packages.

**Performance Goals**: interface + table bring-up within the 3 s cold-start budget (SC-002).

**Constraints**:
- Server key is stable across restarts; a present-but-corrupt key file aborts startup and is **never** regenerated (FR-004/SC-005).
- Default-deny: the forward chain has policy drop and zero rules at all times in this feature (FR-012/SC-004).
- Routine restart must not drop the live interface (FR-016/SC-006).
- The private key value never appears in logs (FR-017).
- Requires root/`CAP_NET_ADMIN`; missing privilege fails clearly (FR-015).

**Scale/Scope**: Single relay instance per host. This feature: 2 new packages
(`wg`, `netfw`), 3 new dependencies, 0 new tables, 0 new HTTP endpoints.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Applies? | How this plan honors it |
|-----------|----------|-------------------------|
| **I. Code Quality** | Yes | Cohesive packages: `wg` (key + interface), `netfw` (forwarding + nftables). Pure helpers (`FirstUsableAddress`) are separately testable. SQLite stays the source of truth; nft/WG state is derived and rebuilt at startup. Config loaded once (001). Errors are values; the only fatal path is `app.Run` startup. The nft builder takes a desired-state seam so feature 005 extends it without a rewrite — minimal, not speculative. |
| **II. Testing Standards (NON-NEGOTIABLE)** | Yes (with documented privilege gap) | Pure logic is fully unit-tested unprivileged. Kernel-boundary behavior (netlink/wgctrl/nftables) is exercised by **real, non-mocked** integration tests that run under root or `unshare -rUn`; they **skip with a clear signal** where privilege is unavailable. This honors "no mocking of system boundaries": we never fake netlink — we either run it for real or skip. **CI MUST provide a privileged job** for these (see research.md R8). Each user story has a test; US3's corrupt-key and unprivileged-failure cases are explicitly covered. |
| **III. UX Consistency** | **N/A** | No end-user (Windows client) surface and no HTTP API in this feature. Operator-facing artifacts (interface attributes, nft table shape, key-file perms) are specified as an observable contract (contracts/system-state.md) so they stay consistent. |
| **IV. Performance Requirements** | Yes | Bring-up is a handful of netlink calls + one key op → far under the 3 s cold-start budget (SC-002), smoke-checked in quickstart. |
| **Security & Operational Discipline** | Yes | Private key is owner-only (FR-002), never logged (FR-017), and never silently regenerated on corruption (FR-004). Runs as root with `CAP_NET_ADMIN` (constitution Security; dev systemd unit from 001). Default-deny forward posture is established before any peer exists (no "briefly open" window). No new primitives — keys via wgtypes, all transport over netlink. |

**Result**: PASS. The privileged-test gap is a documented environmental limitation
(not a mock, not a principle dilution); recorded in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/003-wireguard-server-interface/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output (state/file model; no SQL tables)
├── quickstart.md        # Phase 1 output (privileged + rootless-netns verification)
├── contracts/
│   └── system-state.md  # Observable post-startup contract (interface, nft, key file)
├── checklists/
│   └── requirements.md  # From /speckit-specify
└── tasks.md             # /speckit-tasks output (NOT created here)
```

### Source Code (repository root) — new and changed

```text
internal/server/
├── wg/
│   ├── key.go            # NEW: LoadOrGenerateKey(path) — generate+persist 0600, or load; corrupt => error
│   ├── key_test.go       # NEW (unprivileged): generate, reload-stable, perms, corrupt => error, broad-perms tighten
│   ├── addr.go           # NEW: FirstUsableAddress(cidr) (net/netip) — pure
│   ├── addr_test.go      # NEW (unprivileged): /16, /24, /31, /32 edge cases
│   ├── iface.go          # NEW: Server (wgctrl client) + EnsureInterface(cfg, key): link create/adopt, addr, up, configure
│   └── iface_test.go     # NEW (privileged, skip if not): create, adopt-not-recreate, zero peers, pubkey stable
├── netfw/
│   ├── forward.go        # NEW: EnableIPv4Forward() via /proc/sys; idempotent
│   ├── nftables.go       # NEW: Manager.Rebuild() — inet lanweave table + forward chain policy drop, empty
│   ├── forward_test.go   # NEW (privileged for write; reads unprivileged)
│   └── nftables_test.go  # NEW (privileged, skip if not): table+chain exist, policy drop, no rules, rebuild idempotent
└── app/
    └── app.go            # CHANGED: after admin bootstrap, run wg.EnsureInterface + netfw setup; block serve on failure; no teardown on shutdown
```

**Structure Decision**: Single Go project, continuing the 001/002 layout. Two new
packages split by concern: `wg` owns the server identity and the tunnel interface;
`netfw` owns host forwarding and the nftables isolation table. `FirstUsableAddress`
is a pure function in `wg` (the only consumer this feature); feature 004's IPAM may
later lift it to a shared helper — not abstracted prematurely now. The nftables
builder exposes a `Rebuild` entry point that feature 005 will grow to take zone
sets/rules; in 003 it produces only the empty default-deny skeleton. Data-plane
setup is wired into `app.Run` after admin bootstrap and before serving, so a failure
blocks the service from coming up (FR-014); shutdown deliberately leaves the
interface, forwarding, and table in place (FR-010/FR-016).

## Complexity Tracking

> One documented environmental limitation, not a constitution violation.

| Item | Why | Mitigation / Note |
|------|-----|-------------------|
| Privileged integration tests skip when unprivileged | Creating a kernel WireGuard interface and nftables table fundamentally requires `CAP_NET_ADMIN`; the dev host runs as uid 1000 with no `nft` binary. | Tests are **real, not mocked** (Principle II honored). They run under root or `unshare -rUn`; CI must include a privileged job. Pure logic (key/perms/address) is covered unprivileged everywhere. Documented in research.md R8 and quickstart.md. |
