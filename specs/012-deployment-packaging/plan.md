# Implementation Plan: Deployment Packaging

**Branch**: `012-deployment-packaging` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/012-deployment-packaging/spec.md`

## Summary

Package both halves of the product. The server ships as a Debian `.deb` (built with
`nfpm`) that installs `/usr/bin/lanweaved`, `/etc/lanweave/config.toml.example`, and a
production systemd unit, and whose maintainer scripts produce a runnable default
configuration on first install (a generated self-signed certificate + an initial admin
credential, owner-only perms) so the service comes up active under least privilege
(`CAP_NET_ADMIN` only), restarting on failure and logging to the journal. The Windows
client ships as an installer (NSIS) that bundles the WinTun driver, installs under
`C:\Program Files\lanweave\`, and adds a launch shortcut. Uninstall follows a documented
data policy (remove keeps data, purge removes it). The `.deb` build + layout/permissions/
unit-field assertions are automated on the build host; the live install and the Windows
installer are validated manually on the target OSes.

## Technical Context

**Language/Version**: Go 1.26 (the binaries already exist: `lanweaved`, `lanweave-client`).

**Primary Dependencies**:
- `nfpm` (already on the build host) to assemble the `.deb`; `dpkg-deb` + `fakeroot`
  (present) to inspect it in tests. The `.deb` declares `openssl` as a dependency (used by
  the postinstall to generate the self-signed certificate, M2).
- The existing `config.toml.example`, the server binary (`make build`), and the Windows
  client GUI binary (`-tags gui`, built on Windows).
- Windows installer: NSIS (`.nsi` script, built on Windows); bundles `wintun.dll`.
- `openssl` (or Go's crypto) in the post-install to generate a self-signed certificate.

**Storage**: No code/runtime storage change. Packaging artifacts only. Install layout
(DESIGN §10.2 + ROADMAP Windows path) is the "data model".

**Testing**: `go test`. The packaging test (`packaging/deb_test.go`, host-gated — skips if
`nfpm`/`dpkg-deb` are absent) builds the `.deb` and asserts: the documented files are
present at their paths (`dpkg-deb -c`), the systemd unit contains the required directives
(`User=root`, `CapabilityBoundingSet=CAP_NET_ADMIN`, `Restart=on-failure`, journal output),
the maintainer scripts exist, `config.toml.example` ships and contains no real secret, and
file modes are correct. A unit-file lint test asserts the production unit's fields. The live
`dpkg -i` → service active on a clean Debian host, and the Windows installer (+ WinTun +
elevation), are validated manually (documented exception).

**Target Platform**: Debian/Ubuntu server (`.deb` + systemd); Windows 10/11 client
(installer). The package build runs on the Linux build host.

**Project Type**: Packaging/ops over the existing single Go module.

**Performance Goals**: N/A (packaging). The packaged service still meets the server's own
budgets (features 001–008); the install itself is a one-shot operation.

**Constraints**: Service runs as root with `CapabilityBoundingSet=CAP_NET_ADMIN` only; the
config and key files are owner-only; the example config carries no real secret; upgrades
never clobber an operator's config or data; uninstall follows the documented policy.

**Scale/Scope**: One server package + one Windows installer; a handful of maintainer
scripts and the unit file.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality**: Small, declarative artifacts — one `nfpm.yaml`, one production unit
  file, three short maintainer scripts, one NSIS script — plus a Makefile `deb` target and
  a host-gated packaging test. No product code changes (the binaries are unchanged). Clear,
  reversible, single-purpose files. **PASS**
- **II. Testing Standards (NON-NEGOTIABLE)**: Nothing is mocked — the packaging test builds
  a **real** `.deb` with the real `nfpm` and inspects it with the real `dpkg-deb`, asserting
  the actual layout, permissions, unit fields, and maintainer scripts. Each user story has
  coverage: US1 (server package layout + unit privilege/restart/journal + perms), US2/US3
  partly automated (package metadata, uninstall-script behavior) with the live OS-level
  install and the Windows installer validated manually. **The live `dpkg -i`/`systemctl`
  on a clean Debian host and the Windows installer + WinTun are a documented manual
  exception** (no systemd-as-PID1 or Windows runner here), recorded in Complexity Tracking;
  the package's buildable, inspectable substance is automated. **PASS with documented
  exception.**
- **III. User Experience Consistency**: The operator experience (one install command →
  active service; clear hardening guidance printed; predictable uninstall) and the end-user
  experience (double-click installer → driver + shortcut → first-run setup) are the spec's
  FRs. The post-install prints human-readable next steps (replace the self-signed cert,
  change the admin password). **PASS**
- **IV. Performance Requirements**: N/A for packaging; the packaged server retains its own
  budgets. **PASS**
- **Security & Operational Discipline**: The unit narrows `CapabilityBoundingSet` to
  `CAP_NET_ADMIN` (constitution mandate); config/key files are `0600`/`0700`, root-owned;
  the example config holds no real secret; the generated admin credential and JWT secret are
  random per-install; the self-signed cert is flagged for replacement. No secret in the
  package or in git. **PASS**

One documented exception (live install + Windows installer manual validation) recorded in
Complexity Tracking; no principle diluted.

## Project Structure

### Documentation (this feature)

```text
specs/012-deployment-packaging/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── install-layout.md     # documented file locations + permissions (server + client)
│   └── service-unit.md       # the systemd unit contract + maintainer-script behavior
└── checklists/
    └── requirements.md
```

### Source Code (repository root)

```text
packaging/
├── nfpm.yaml                 # NEW: .deb definition (contents, scripts, metadata)
├── scripts/
│   ├── postinstall.sh        # NEW: create data dir; generate self-signed cert + config (if absent); enable+start; print hardening notes
│   ├── preremove.sh          # NEW: stop + disable the service
│   └── postremove.sh         # NEW: on purge, remove /var/lib/lanweave + /etc/lanweave
├── windows/
│   └── lanweave-client.nsi   # NEW: NSIS installer script (bundles wintun.dll; Program Files; shortcut; elevation) — built on Windows
├── doc.go                    # untagged package stub so `go build ./...` stays clean
└── deb_test.go               # NEW host-gated test: build the .deb + assert layout/unit/perms/scripts

deploy/systemd/
└── lanweaved.service         # NEW production unit (User=root, CapabilityBoundingSet=CAP_NET_ADMIN, Restart=on-failure, journal)

Makefile                      # + `deb` target (build binary → nfpm package); doc the client build
config.toml.example           # reused (ships in the .deb)
INSTALL.md                    # NEW: install/uninstall + hardening + data-retention policy (operator docs)
```

**Structure Decision**: Packaging artifacts live under `packaging/` and `deploy/systemd/`;
the only Go is a host-gated test (`deb_test.go`) plus a `doc.go` stub. No product code
changes; the server/client binaries are packaged as built.

### Fresh-install flow (reference for tasks)

1. `dpkg -i` lays down the binary, `config.toml.example`, and the unit.
2. `postinstall.sh`: create `/var/lib/lanweave` (`0700`, root); if `/etc/lanweave/config.toml`
   is absent, generate it from the example with a random admin password + JWT secret
   (`0600`, root) and generate a self-signed `cert.pem`/`key.pem` (`0600`, via `openssl` —
   declared as a package dependency, M2); **write the generated admin password to a root-only
   file `/etc/lanweave/initial-admin-password` (`0600`) and print only its path — never echo
   the password to stdout/logs (M1, constitution §Security)**; `daemon-reload`, `enable`,
   `start`; print "replace the cert / change the admin password then delete that file"
   guidance.
3. Upgrade: do not overwrite an existing `config.toml` or the data.
4. `preremove`/`postremove`: stop+disable; on **purge**, remove data + config.

## Complexity Tracking

> One documented exception (live OS-level install + Windows installer manual validation
> under Principle II), recorded per the constitution's process. No principle diluted.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| The live `dpkg -i` → `systemctl active` on a clean Debian host, and the Windows installer + WinTun + elevation, are validated manually rather than by automated tests | The build host has no systemd-as-PID1 and no Windows runner; installing system-wide is invasive and not reproducible in CI here | A throwaway Debian VM / Windows runner is out of scope for v1; the package's contents, layout, permissions, unit fields, and maintainer scripts — the substance that determines install behavior — are fully asserted by building and inspecting the real `.deb`, so the manual step is limited to the OS actually executing the verified package |
