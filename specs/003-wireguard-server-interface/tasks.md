---
description: "Task list for 003-wireguard-server-interface"
---

# Tasks: WireGuard Server Interface

**Input**: Design documents from `/specs/003-wireguard-server-interface/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present). Builds on feature 001 (config + app lifecycle).

**Tests**: REQUIRED per constitution Principle II. **Two tiers**: pure-logic **unit**
tests run unprivileged everywhere; kernel-boundary **integration** tests are real
(NOT mocked) and run under root or a rootless user+net namespace (`unshare -rUn`),
**skipping with a clear message** when privilege is unavailable. Test tasks are
written FIRST and must FAIL before their implementation tasks. CI MUST include a
privileged job so the integration tier actually executes (research.md R8).

**Organization**: Tasks grouped by user story. US1+US2 are P1 (the data-plane MVP);
US3 (P2) hardens idempotency/failure-safety.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US3; Setup/Foundational/Polish carry no story label
- **(privileged)**: integration task needing root / `unshare -rUn`; skips otherwise

## Path Conventions

Single Go project `lanweave`. New packages `internal/server/wg` and
`internal/server/netfw`; wiring in `internal/server/app/app.go`.

---

## Phase 1: Setup

- [X] T001 Add dependencies and `go mod tidy`: `golang.zx2c4.com/wireguard/wgctrl` (+ `wgctrl/wgtypes`), `github.com/vishvananda/netlink`, `github.com/google/nftables`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared test capability used by every privileged integration test.

- [X] T002 Add `RequireNetAdmin(t *testing.T)` to `internal/testutil/netadmin.go`: probe whether the process can manage network state (e.g. attempt a throwaway netlink op or check euid/effective caps) and `t.Skip` with a clear message ("requires CAP_NET_ADMIN; run as root or `unshare -rUn`") when it cannot (research.md R8)

**Checkpoint**: `go build ./...` still green; privileged tests can guard themselves.

---

## Phase 3: User Story 1 — 稳定身份的隧道接口 (Priority: P1) 🎯 MVP

**Goal**: A `wg-lanweave` interface comes up with the configured name/port, the first
pool address, and a persistent server key (generated once, loaded thereafter, never
regenerated), with zero peers.

**Independent Test**: On a privileged host, start → interface exists with first pool
IP and listening port, reports a public key, has no peers; key file is `0600`; restart
→ same public key.

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

- [X] T003 [P] [US1] Unit test `internal/server/wg/addr_test.go` for `FirstUsableAddress`: `100.127.0.0/16`→`100.127.0.1/16`, a `/24` case, and `/31`/`/32` (and base-only ranges) → error — MUST FAIL
- [X] T004 [P] [US1] Unit test `internal/server/wg/key_test.go`: first call generates and writes `0600`; second call loads the SAME key (stable); a corrupt/garbage file → error with NO regeneration (FR-004); a `0644` file is tightened to `0600` (or fails) — MUST FAIL
- [X] T005 [P] [US1] (privileged) Integration test `internal/server/wg/iface_test.go` (guarded by `RequireNetAdmin`): `EnsureInterface` creates a `wireguard` link with the derived address, configured port, server pubkey, and ZERO peers; a second `EnsureInterface` adopts it (same ifindex, same pubkey) rather than recreating — MUST FAIL

### Implementation for User Story 1

- [X] T006 [P] [US1] Implement `FirstUsableAddress(cidr string) (netip.Addr, int, error)` in `internal/server/wg/addr.go` (net/netip; reserve base address; error if no usable host)
- [X] T007 [P] [US1] Implement `LoadOrGenerateKey(path string) (wgtypes.Key, generated bool, err error)` in `internal/server/wg/key.go`: generate+persist `0600` if absent; load+parse if present; corrupt → error, no regen; tighten broad perms (FR-001..004); never log the key
- [X] T008 [US1] Implement `Server` + `EnsureInterface(cfg, key, log)` in `internal/server/wg/iface.go`: `LinkByName` adopt-or-`LinkAdd` (`type wireguard`), `AddrReplace` first pool IP, `LinkSetUp`, and wgctrl `ConfigureDevice` (private key + listen port, no peers); wrong-type interface → conflict error; expose `PublicKey()` and `Close()` (closes wgctrl/netlink handles, leaves the interface up)
- [X] T009 [US1] Wire WireGuard setup into `internal/server/app/app.go`: after admin bootstrap, `LoadOrGenerateKey` + `EnsureInterface`; on failure return a clear error (block serving); on shutdown call `Server.Close()` but do NOT remove the interface; log key generated/loaded and interface created/adopted (no key value)

**Checkpoint**: US1 verifiable on a privileged host; unit tier green everywhere.

---

## Phase 4: User Story 2 — 转发与隔离骨架（默认拒绝） (Priority: P1) 🎯 MVP

**Goal**: IPv4 forwarding enabled; `inet lanweave` table exists with a single forward
chain whose policy is drop and which holds no rules/sets.

**Independent Test**: On a privileged host, start → `ip_forward`=1; the table exists
with `forward` chain policy drop and zero rules; restart rebuilds the same clean state.

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

- [X] T010 [P] [US2] (privileged) Test `internal/server/netfw/forward_test.go` (guarded): `EnableIPv4Forward` makes `/proc/sys/net/ipv4/ip_forward` read `1`; calling twice is idempotent — MUST FAIL
- [X] T011 [P] [US2] (privileged) Test `internal/server/netfw/nftables_test.go` (guarded): `Rebuild` yields a table with a `forward` chain of policy drop and NO rules/sets; running `Rebuild` again over the existing/stale table returns the same clean state (idempotent) — MUST FAIL

### Implementation for User Story 2

- [X] T012 [P] [US2] Implement `EnableIPv4Forward()` in `internal/server/netfw/forward.go` (write `1` to the proc file; idempotent; never disabled on shutdown)
- [X] T013 [US2] Implement `Manager` + `Rebuild(log)` in `internal/server/netfw/nftables.go` using `github.com/google/nftables`: delete the `inet <table>` if present, then add table + `forward` chain (`hook forward priority filter; policy drop`) in one flush; no sets/rules; structure `Rebuild` to accept future zone state (feature 005 seam) but build empty now
- [X] T014 [US2] Wire `netfw` into `internal/server/app/app.go` after WireGuard setup: `EnableIPv4Forward` then `Manager.Rebuild`; failure blocks serving; log forwarding-enabled and table-rebuilt

**Checkpoint**: US1 + US2 → full default-deny data plane on a privileged host.

---

## Phase 5: User Story 3 — 重启幂等且失败安全 (Priority: P2)

**Goal**: Restart adopts the live interface (no teardown); unsafe conditions fail
loudly (corrupt key never regenerated, missing privilege errors clearly).

**Independent Test**: Restart preserves the interface ifindex; unprivileged start
returns a clear privilege error; corrupt key aborts with no regeneration.

### Tests for User Story 3 (REQUIRED per constitution Principle II) ⚠️

- [X] T015 [P] [US3] Unit test (unprivileged) in `internal/server/wg/iface_test.go` or `internal/server/app/dataplane_test.go`: invoking the data-plane setup WITHOUT privilege returns a clear, typed error (no panic, no partial serving state) — runnable everywhere, asserts the failure path (FR-015/US3-3) — MUST FAIL
- [X] T016 [P] [US3] (privileged) Test in `internal/server/wg/iface_test.go`: two successive `EnsureInterface` calls keep the SAME ifindex (adopt, not recreate) so a live tunnel is preserved across restart (SC-006/FR-016) — MUST FAIL

### Implementation for User Story 3

- [X] T017 [US3] Harden failure modes: ensure `EnsureInterface`/`LoadOrGenerateKey`/`Rebuild` return clear typed errors for missing privilege, corrupt key (no regen — confirm with T004), wrong-type interface conflict, and port/name conflict; confirm `app.Run` surfaces them and that shutdown performs no teardown of interface/table/forwarding

**Checkpoint**: All three stories verifiable; failure paths safe.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T018 [P] Update `config.toml.example` comments to note `[wireguard]` and `[nftables]` are now consumed by the data-plane bring-up
- [X] T019 Run `make lint` (gofmt + `go vet` + staticcheck) clean; confirm unit-tier coverage on pure logic (`wg` key/addr, mapping); document that kernel-path coverage is only measured under the privileged job
- [X] T020 Execute `quickstart.md` on a privileged host (or `unshare -rUn`): verify US1–US3 plus SC-001 (stable key over restarts), SC-003 (key `0600`), SC-004 (policy drop, no rules), SC-005 (corrupt key fails safe), SC-006 (interface preserved); ensure CI is configured with a privileged/netns job for the integration tier

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: depends on Setup — provides the privileged-test guard used by every integration test.
- **US1 (Phase 3)**: depends on Foundational. Pure logic (T006/T007) and the interface (T008) then wiring (T009).
- **US2 (Phase 4)**: depends on Foundational; independent of US1's code but its app wiring (T014) runs after US1's (T009) in `app.Run` order.
- **US3 (Phase 5)**: depends on US1 (interface adoption) and US2 (table rebuild) existing; it hardens and tests their failure/idempotency properties.
- **Polish (Phase 6)**: after the targeted stories.

### Critical Path

```
Setup → Foundational → US1 ──┐
                        US2 ──┴─→ US3 → Polish
```

### Within Each User Story

- Test tasks (⚠️) written first and MUST FAIL before implementation.
- US1: address + key (pure) before interface; interface before app wiring.
- US2: forwarding + table before app wiring.

---

## Parallel Opportunities

- **US1**: T003 [P] (addr test), T004 [P] (key test), T005 [P] (iface test) are distinct files; T006 [P] (addr) and T007 [P] (key) implement in parallel; T008 then T009 sequence.
- **US2**: T010 [P] / T011 [P] (distinct test files); T012 [P] (forward) parallel with T013 (nftables).
- **US3**: T015 [P] / T016 [P] (unprivileged vs privileged) in parallel.
- **Polish**: T018 [P] independent.

### Parallel Example: US1 pure-logic tests + impl

```bash
Task T003: FirstUsableAddress unit test (wg/addr_test.go)
Task T004: key load/generate unit test (wg/key_test.go)
Task T006: FirstUsableAddress impl (wg/addr.go)
Task T007: LoadOrGenerateKey impl (wg/key.go)
```

---

## Implementation Strategy

### MVP (US1 + US2)

1. Setup + Foundational (deps + test guard).
2. US1 → interface with stable identity (pure logic unit-tested everywhere; interface on a privileged host).
3. US2 → forwarding + default-deny nftables table.
4. **STOP & VALIDATE** on a privileged host (or `unshare -rUn`): interface up with first pool IP, no peers, `ip_forward=1`, table policy drop — the data-plane foundation features 004/005 build on.

### Incremental Delivery

- Add US3 → prove restart-idempotency and safe failure (no key regen, clear privilege errors, no teardown).

---

## Implementation outcomes (analyze findings)

- **I1 (nftables priority)**: resolved using the library constants
  `nftables.ChainHookForward` + `nftables.ChainPriorityFilter` (not an `nft`-CLI
  "filter" string) in `netfw/nftables.go` — verified real via `TestRebuild`.
- **U1 (key drift on adopt)**: `EnsureInterface` overwrites the device's private
  key from the key file on adopt (idempotent); the key FILE is never regenerated
  (FR-004). Documented in `wg/iface.go`.
- **I2 (rootless forwarding)**: `forward_test.go` notes rootless runs may need
  `unshare -rUn --mount-proc`; in practice the plain netns proc was writable here.
- **App tests now privileged**: full `app.Run` brings up the kernel data plane, so
  the two booting acceptance tests gate on `RequireNetAdmin` (+ bring `lo` up in the
  netns) and skip in a bare unprivileged `go test`.
- **Verified for real** in this environment via `unshare -rUn`: WG interface
  create/adopt (stable ifindex + pubkey), nftables default-drop table, ip_forward,
  full `app.Run`, plus a binary smoke (`wg show`, `ip addr`, restart-stable pubkey,
  no key in logs).

## Notes

- [P] = different files, no dependency on an incomplete task.
- **No mocking** of netlink/wgctrl/nftables: integration tests run them for real (privileged) or skip with a clear message (constitution Principle II; research.md R8). CI MUST run the privileged tier.
- Reuses feature 001: config (`wireguard.*`, `nftables.table`, `data_dir`), structured logging + `Secret` redaction, `app.Run` lifecycle. The private key must never be logged (FR-017).
- No new SQLite tables and no new HTTP endpoint in this feature.
- Out of scope (later features): client peers (004), zone sets/accept rules (005), IPAM (004), `GET /server` public-key endpoint (004), userspace WireGuard fallback.
