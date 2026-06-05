# Tasks: Deployment Packaging

**Feature**: 012-deployment-packaging | **Branch**: `012-deployment-packaging`
**Input**: Design documents in `/specs/012-deployment-packaging/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED per constitution Principle II. Nothing is mocked — the packaging test
builds a **real** `.deb` with the real `nfpm` and inspects it with the real `dpkg-deb`,
asserting the actual layout, permissions, unit fields, and maintainer scripts. The live
`dpkg -i` → service active on a clean Debian host and the Windows installer (+ WinTun +
elevation) are validated manually (documented exception).

**Build isolation**: the only Go added is a host-gated test (`packaging/deb_test.go`, skipped
when `nfpm`/`dpkg-deb` are absent) plus a `packaging/doc.go` stub, so default `go build ./...`
/ `go test ./...` stay green. No product code changes.

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`

---

## Phase 1: Setup

- [X] T001 Create the `packaging/` directory with an untagged `packaging/doc.go` (`package packaging`) stub so `go build ./...` stays clean, and create an `INSTALL.md` skeleton at the repo root

---

## Phase 2: Foundational (the buildable package — blocks US1/US3 tests)

- [X] T002 [P] Create the production systemd unit `deploy/systemd/lanweaved.service`: `[Service]` with `User=root`, `CapabilityBoundingSet=CAP_NET_ADMIN`, `AmbientCapabilities=CAP_NET_ADMIN`, `ExecStart=/usr/bin/lanweaved -config /etc/lanweave/config.toml`, `Restart=on-failure`, `RestartSec=2`, `StandardOutput=journal`, `StandardError=journal`; `[Install] WantedBy=multi-user.target`; `[Unit]` After/Wants `network-online.target`
- [X] T003 [P] Create the maintainer scripts: `packaging/scripts/postinstall.sh` (create `/var/lib/lanweave` `0700` root; if `/etc/lanweave/config.toml` absent → generate it from `config.toml.example` with a random admin password + random JWT secret `0600` root, and generate a self-signed `cert.pem`/`key.pem` via `openssl`, `0600`; **write the generated admin password to a root-only file `/etc/lanweave/initial-admin-password` (`0600`) — do NOT print the password to stdout/logs (M1, constitution §Security)**; `systemctl daemon-reload` + `enable --now`; print only the password-file *path* + "replace the self-signed cert / change the admin password then delete this file" guidance); `packaging/scripts/preremove.sh` (`systemctl stop` + `disable` on remove); `packaging/scripts/postremove.sh` (on `purge` only, remove `/var/lib/lanweave` and `/etc/lanweave`)
- [X] T004 Create `packaging/nfpm.yaml`: package `lanweave`, version from env, contents = `/usr/bin/lanweaved` (`0755`), `/etc/lanweave/config.toml.example` (`0644`, conffile), `/lib/systemd/system/lanweaved.service` (`0644`); wire `scripts.postinstall`/`preremove`/`postremove`; **declare `depends: [openssl]`** so the fresh-install certificate generation works on a minimal system (M2)
- [X] T005 Add a `deb` target to `Makefile`: build the server binary (`make build`), then `nfpm pkg --packager deb --config packaging/nfpm.yaml --target dist/` (creating `dist/`); document the Windows client build (`-tags gui` on Windows) in a comment

**Checkpoint**: `make deb` produces a real `.deb` with the unit + scripts + example config.

---

## Phase 3: User Story 1 — 运维一条命令装好服务端 (P1) 🎯 MVP

**Goal**: the server `.deb` installs the binary, example config, and a least-privilege,
auto-restarting, journaled service, yielding an active service.

**Independent test**: build the `.deb` and assert its contents, the unit's privilege/restart/
journal fields, the maintainer scripts, owner-only example perms, and a secret-free example
config. (Live `dpkg -i` → active is the manual scenario.)

- [X] T006 [US1] Create the host-gated test `packaging/deb_test.go` (skip if `nfpm`/`dpkg-deb` absent): run `make deb`; `dpkg-deb -c` → assert `/usr/bin/lanweaved`, `/etc/lanweave/config.toml.example`, `/lib/systemd/system/lanweaved.service` are present; extract and assert the unit contains `User=root`, `CapabilityBoundingSet=CAP_NET_ADMIN`, `Restart=on-failure`, `StandardOutput=journal`, and the `-config /etc/lanweave/config.toml` ExecStart; `dpkg-deb -e` → assert `postinst`/`prerm`/`postrm` exist and are executable, the control file's `Depends` includes `openssl` (M2), and `postinst` does **not** echo the admin password (writes it to the root-only file instead — M1); assert `config.toml.example` contains a placeholder (no real 32-byte secret)

**Checkpoint**: US1 is demonstrable — the package's substance is asserted (MVP).

---

## Phase 4: User Story 2 — 终端用户用标准安装包装客户端 (P2)

**Goal**: a Windows installer installs the client + WinTun driver + shortcut and lands the
user in first-run setup.

**Independent test**: on a clean Windows system, run the installer (accepting elevation) →
the app + driver are under `C:\Program Files\lanweave\`, a shortcut exists, and launching
enters the wizard.

- [X] T007 [US2] Create `packaging/windows/lanweave-client.nsi`: an NSIS installer that requests administrator elevation, installs `lanweave-client.exe` + `wintun.dll` to `C:\Program Files\lanweave\`, creates a Start-menu/desktop shortcut, and registers an uninstaller; bail out cleanly if elevation is denied
- [X] T008 [US2] Document the Windows build + manual validation in `INSTALL.md`: build the GUI client (`-tags gui` on Windows), place `wintun.dll`, build the installer with NSIS, and the manual acceptance (UAC → install → shortcut → wizard)

**Checkpoint**: the Windows installer script exists and is documented (built/validated on Windows).

---

## Phase 5: User Story 3 — 干净卸载，数据策略明确 (P2)

**Goal**: uninstall removes program files + service registration; data is retained or purged
per a documented policy.

**Independent test**: assert `prerm` stops/disables and `postrm` removes data + config only on
`purge`; the policy is documented.

- [X] T009 [US3] Extend `packaging/deb_test.go`: assert `prerm` calls `systemctl stop`/`disable`; assert `postrm` removes `/var/lib/lanweave` and `/etc/lanweave` and does so **only** on the `purge` argument (the script branches on `$1 = purge`); assert the scripts are wired in `nfpm.yaml`
- [X] T010 [US3] Document the uninstall / purge / data-retention policy in `INSTALL.md`: server `remove` keeps `/var/lib/lanweave` + config, `purge` removes them; the Windows uninstaller removes program files and keeps the user's secure-store secrets + `%LOCALAPPDATA%\lanweave` state (with a manual purge note)

**Checkpoint**: uninstall behavior is asserted and the data policy is documented.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T011 [P] Run `gofmt -w` on `packaging/`; `go vet ./...`; run `shellcheck` on the maintainer scripts if available and fix findings; confirm `go build ./...` (headless) still succeeds
- [X] T012 [P] Run `go test ./packaging/...` (builds + inspects the real `.deb`) and confirm it passes; confirm `make deb` produces `dist/lanweave_<version>_amd64.deb`
- [X] T013 Validate quickstart Scenario A automatically (package contents); record Scenarios B–E (live `dpkg -i` → active, crash auto-restart, `remove`/`purge`, upgrade-preserves-config, and the Windows installer + WinTun + elevation) as the manual target-OS checks

---

## Dependencies & Execution Order

- **Setup (T001)** blocks everything.
- **Foundational (T002–T005)**: T002 (unit) ∥ T003 (scripts) are independent; T004 (nfpm.yaml)
  needs T002 + T003; T005 (`make deb`) needs T004. Block the US1/US3 tests.
- **US1 (T006)**: needs T005 (a buildable `.deb`).
- **US2 (T007–T008)**: independent of the server package; can proceed in parallel.
- **US3 (T009–T010)**: T009 extends the US1 test (after T006) + T003's scripts; T010 docs.
- **Polish (T011–T013)**: after all artifacts/tests.

### File coordination (sequential within a file)

- `packaging/deb_test.go`: T006 → T009.
- `INSTALL.md`: T001 → T008 → T010.
- `packaging/nfpm.yaml`: T004 (references T002/T003 artifacts).

## Parallel Execution Examples

- **Foundational**: T002 (`lanweaved.service`) ∥ T003 (`scripts/*.sh`).
- **Across stories**: US2 (T007 NSIS) ∥ the server-package work (T002–T006).
- **Polish**: T011 (lint) ∥ T012 (test/build).

## Implementation Strategy

**MVP** = Setup + Foundational + US1 (T001–T006): `make deb` builds a real `.deb` whose
contents, least-privilege/restart/journal unit, maintainer scripts, and secret-free example
config are asserted by the host-gated test. US2 adds the Windows installer script (built on
Windows), US3 the uninstall/purge policy + assertions. The package substance is fully
automated; the live OS-level install and the Windows installer are validated manually on the
target operating systems.
