# Research: Deployment Packaging

**Feature**: 012-deployment-packaging | **Date**: 2026-06-06

The questions are about packaging tooling, how to make a fresh install runnable, and how to
test packaging without a throwaway VM. Decisions below resolve them; none remain as NEEDS
CLARIFICATION.

## Decision 1 — Server `.deb` built with `nfpm`

- **Decision**: Build the Debian package with `nfpm` (a declarative `nfpm.yaml`), which is
  already installed on the build host. It bundles `/usr/bin/lanweaved`,
  `/etc/lanweave/config.toml.example`, the systemd unit, and the maintainer scripts.
- **Rationale**: ROADMAP offers "nfpm 或 dpkg-deb"; `nfpm` is declarative, reproducible,
  scriptable from the Makefile, and present here so the build is automatable. It marks the
  example config as a conffile and runs `postinstall`/`postremove` scripts.
- **Alternatives considered**:
  - *Hand-rolled `dpkg-deb` tree*: more boilerplate (control files, conffiles by hand);
    `nfpm` does this declaratively. Rejected.
  - *`goreleaser`*: heavier, oriented at release pipelines; overkill for one package.
    Rejected.

## Decision 2 — Production systemd unit (least privilege)

- **Decision**: Ship `deploy/systemd/lanweaved.service` with `User=root`,
  `CapabilityBoundingSet=CAP_NET_ADMIN` (and `AmbientCapabilities=CAP_NET_ADMIN`),
  `Restart=on-failure`, `StandardOutput=journal`/`StandardError=journal`, and
  `ExecStart=/usr/bin/lanweaved -config /etc/lanweave/config.toml`, plus light hardening
  (`NoNewPrivileges` is incompatible with needing CAP_NET_ADMIN at exec, so it is omitted;
  `ProtectSystem=full`, `ReadWritePaths=/var/lib/lanweave /etc/lanweave` where compatible).
- **Rationale**: Constitution Security & Operational Discipline mandates narrowing
  `CapabilityBoundingSet` to the minimum (`CAP_NET_ADMIN` for nftables + WireGuard);
  DESIGN §10.4 lists the unit essentials (root, restart, journal). Root is required because
  the kernel data plane needs it; the bounding set limits what root can do.
- **Alternatives considered**:
  - *Run as a non-root user with ambient `CAP_NET_ADMIN`*: WireGuard/nftables setup and the
    data dir make full non-root operation fragile for v1; DESIGN §10.4 specifies `User=root`.
    Rejected.

## Decision 3 — Runnable default on first install (so "install → active")

- **Decision**: The `postinstall` script, only when `/etc/lanweave/config.toml` is absent,
  generates a config from `config.toml.example` with a **random** admin password and JWT
  secret, generates a **self-signed** `cert.pem`/`key.pem` (via `openssl`, declared as a
  package dependency — M2), sets `0600`/`0700` root-owned perms, writes the generated admin
  password to a **root-only file** `/etc/lanweave/initial-admin-password` (`0600`) — **not**
  to stdout/logs (M1) — then `daemon-reload` + `enable` + `start`, and prints only that
  file's path plus guidance to replace the certificate, change the password, and delete the
  file.
- **Rationale**: The server loads an operator-supplied TLS cert/key and does not self-
  generate; the ROADMAP acceptance ("`dpkg -i` → service active") therefore requires the
  package to produce a runnable config. Random per-install secrets + owner-only perms keep
  this safe-by-default; the self-signed cert is flagged for replacement (clients must trust
  it otherwise). **The generated admin password goes to a root-only file rather than the
  installer's stdout/journal, honoring the constitution's "no plaintext password in any log
  line" rule (M1).** On upgrade the script does nothing destructive (the config exists).
- **Alternatives considered**:
  - *Ship an active config with fixed secrets*: a shared default admin password / JWT secret
    is a security hole. Rejected.
  - *Leave the service stopped until configured*: contradicts the ROADMAP "active"
    acceptance and worsens first-run UX. Rejected.

## Decision 4 — Uninstall data policy (Debian convention)

- **Decision**: Follow the Debian convention — `remove` stops/disables the service and keeps
  `/var/lib/lanweave` (database, key) and `/etc/lanweave` (config); `purge` removes them.
  The Windows uninstaller removes the program files and leaves the user's secure-store
  secrets and local state (a documented "remove app, keep your identity" default; a manual
  purge is documented). Documented in `INSTALL.md`.
- **Rationale**: FR-011 wants a predictable, documented policy with a purge path; the Debian
  convention is exactly that (`remove` vs `purge`) and is least-surprising for operators.
  Keeping client secrets on uninstall avoids destroying a user's device identity by accident.
- **Alternatives considered**:
  - *Always delete data on remove*: risks accidental loss of the whole server state. Rejected.

## Decision 5 — Windows installer via NSIS (built on Windows)

- **Decision**: Provide an NSIS `.nsi` script that installs `lanweave-client.exe` +
  `wintun.dll` under `C:\Program Files\lanweave\`, creates a Start-menu/desktop shortcut,
  and requests administrator elevation (needed to install the driver). The installer is
  built on Windows (or with a Windows cross-toolchain); the script is the deliverable here.
- **Rationale**: ROADMAP allows NSIS or WiX; NSIS is lightweight and scriptable. The GUI
  binary (`-tags gui`) and the installer are produced on Windows, consistent with the
  client's manual-on-Windows validation in 009–011.
- **Alternatives considered**:
  - *WiX/MSI*: heavier authoring; NSIS suffices for a single-app installer with a driver.
    Rejected for v1.

## Decision 6 — Testing without a VM: build + inspect the real `.deb`

- **Decision**: A host-gated Go test (`packaging/deb_test.go`, skipped if `nfpm`/`dpkg-deb`
  are absent) runs the real `make deb`, then uses `dpkg-deb -c`/`-e`/`-x` to assert: the
  documented files exist at their paths; the unit file contains the required directives;
  `config.toml.example` ships and has no real secret; the maintainer scripts are present and
  reference the right paths; and modes are correct. A separate test asserts the unit file's
  fields directly. The live `dpkg -i` + `systemctl active` and the Windows installer are
  validated manually.
- **Rationale**: This exercises the **real** packaging tools and the **real** package
  contents (Principle II — no mocking), which is what determines install behavior, without
  needing systemd-as-PID1 or a Windows runner. The remaining manual step is just the OS
  executing the already-verified package.
- **Alternatives considered**:
  - *Spin up a Debian container with systemd*: heavy and environment-specific; the
    content/layout assertions already cover what we can verify deterministically. Deferred to
    a manual VM check.

## Resolved unknowns summary

| Topic | Resolution |
|-------|------------|
| Server package tool | `nfpm` → `.deb` (declarative, on the build host) |
| Service unit | root + `CapabilityBoundingSet=CAP_NET_ADMIN` + restart + journal (DESIGN §10.4) |
| Install → active | postinstall generates a self-signed cert + random-secret config if absent |
| Uninstall policy | Debian `remove` keeps data, `purge` removes; client keeps secrets (documented) |
| Windows installer | NSIS `.nsi` bundling `wintun.dll`, Program Files, shortcut, elevation (built on Windows) |
| Testing | build + inspect the real `.deb` (host-gated); live install + Windows installer manual |
