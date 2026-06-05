# Feature Specification: Deployment Packaging

**Feature Branch**: `012-deployment-packaging`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "012" (ROADMAP feature 012: deployment-packaging)

Scope drawn from ROADMAP.md feature 012 and DESIGN.md §10: package both halves of the
product for real installation. The server ships as a Debian package that installs the
service binary, an example configuration, and a managed-service definition into standard
locations and runs the service under least privilege; the Windows client ships as a
standard installer that also installs its required virtual-network driver. Both have a
clear, predictable uninstall (and data-retention) policy.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 运维一条命令装好服务端 (Priority: P1)

An operator on a clean Debian system installs the server from a single distributable
package. The install places the service binary, an example configuration, and the
service definition in standard system locations, registers the service, and the service
comes up as a managed, running service — restarting on failure and logging to the system
journal — under the least privilege it needs.

**Why this priority**: Operators won't adopt a tool they can't install and run as a normal
managed service. A one-command install that yields a running, journaled, auto-restarting
service is the core of server packaging. Independently testable by installing the package
on a clean system and checking the service status, files, privileges, and logs.

**Independent Test**: On a clean Debian system, install the package with the standard
package tool; confirm the binary, example config, and service definition are at their
documented paths, the service is active and journaled, it restarts after a crash, and it
runs with only the network-administration capability.

**Acceptance Scenarios**:

1. **Given** a clean Debian system, **When** the operator installs the server package, **Then** the service is registered and shown as active.
2. **Given** the installed service, **When** it is inspected, **Then** it runs with only the network-administration capability (no broader privileges) and sends its logs to the system journal.
3. **Given** the installed service, **When** its process crashes, **Then** the init system restarts it automatically.
4. **Given** the install, **When** the file layout is inspected, **Then** the binary, configuration, database, and key files are at the documented locations, with the configuration and key files readable only by their owner.

---

### User Story 2 — 终端用户用标准安装包装客户端 (Priority: P1)

An end-user on a clean Windows system runs a standard installer. It installs the
application and its required virtual-network driver into the standard program location and
creates a launch shortcut. Launching the app on a fresh machine goes straight into
first-run setup.

**Why this priority**: The Windows client is the end-user surface; a double-click installer
that bundles the driver and lands the user in setup is how the product reaches people.
Independently testable on a clean Windows machine.

**Independent Test**: On a clean Windows system, run the installer (granting the elevation
it requests); confirm the app and driver are installed under the standard program
location, a shortcut exists, and launching the app reaches the first-run wizard.

**Acceptance Scenarios**:

1. **Given** a clean Windows system, **When** the user runs the installer, **Then** the application and its virtual-network driver are installed and a launch shortcut is created.
2. **Given** the installed app, **When** the user launches it on a fresh machine, **Then** it enters first-run setup.
3. **Given** the installer needs to install a driver, **When** it runs, **Then** it requests administrator elevation and does not silently fail without it.

---

### User Story 3 — 干净卸载，数据策略明确 (Priority: P2)

Uninstalling either package removes the installed program files and, for the server, the
service registration. Persistent data — the server's database and keys, the client's
stored secrets and local state — is retained or removed according to a documented,
predictable policy, with a way to fully purge when retention is the default.

**Why this priority**: An uninstall that leaves orphaned services, or that silently
destroys (or silently keeps) sensitive data, erodes trust and causes operational
surprises. A clear policy makes removal safe and predictable.

**Independent Test**: Install, then uninstall; confirm program files and the service
registration are gone; confirm persistent data is retained or removed exactly per the
documented policy; confirm a purge option removes it when retention is the default.

**Acceptance Scenarios**:

1. **Given** an installed server, **When** it is uninstalled (standard removal), **Then** the program files and service registration are removed and the persistent data is handled per the documented policy.
2. **Given** an installed server, **When** it is fully purged, **Then** the persistent data (database, keys) is also removed.
3. **Given** an installed Windows client, **When** it is uninstalled, **Then** the program files are removed; the user's stored secrets and local state are handled per the documented policy.
4. **Given** persistent data was retained, **When** the package is reinstalled, **Then** the retained data is reused (no loss, no surprise re-initialization).

---

### Edge Cases

- **Upgrade over an existing install**: the operator's configuration and persistent data are preserved — the example config does not overwrite an active config, and the database/keys are untouched.
- **Install on a system missing prerequisites** (e.g., no supported init system): the install fails clearly without leaving a half-installed state.
- **Windows install without administrator rights**: the installer requests elevation; if denied, it does not partially install the driver.
- **Fresh-install default certificate is self-signed**: the operator is told to replace it for production (otherwise clients must be configured to trust it).
- **Service fails to start due to bad operator configuration**: the service status shows failed and the journal explains why (auto-restart does not hide a misconfiguration).
- **Reinstall after a purge**: starts fresh (a new default configuration), not from stale data.
- **Two installs of the server on one host**: unsupported (single-instance); the package does not attempt to run multiple service instances.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The server MUST be installable on a clean Debian system from a single distributable package using the standard system package tool.
- **FR-002**: Installing the server package MUST place the service binary, an example configuration, and the service definition at their documented standard locations and register the service with the init system.
- **FR-003**: The installed service MUST run with only the network-administration capability it needs (no broader privileges).
- **FR-004**: The installed service MUST restart automatically on failure and send its logs to the system journal.
- **FR-005**: A fresh install MUST yield a service that starts successfully with a safe default configuration (including a generated server certificate and an initial administrator credential); the operator MUST be guided to review and harden it.
- **FR-006**: Configuration and key files MUST be readable only by their owner; the example configuration MUST contain no real secrets and MUST NOT overwrite an operator's existing configuration on upgrade.
- **FR-007**: The Windows client MUST be installable on a clean Windows system from a standard installer that also installs the required virtual-network driver.
- **FR-008**: Installing the Windows client MUST place the application under the standard program location and create a launch shortcut; launching it on a fresh machine MUST enter first-run setup.
- **FR-009**: The Windows installer MUST request administrator elevation when it needs to install the driver and MUST NOT partially install if elevation is denied.
- **FR-010**: Uninstalling either package MUST remove the installed program files and (for the server) the service registration.
- **FR-011**: Persistent data MUST follow a documented, predictable uninstall policy (server database and keys; client secrets and local state) with a purge option available where retention is the default.
- **FR-012**: Upgrading an installed package MUST preserve the operator's configuration and persistent data (no loss, no clobbered config).
- **FR-013**: The installed file locations MUST match the documented conventions (server: binary, configuration, database, and key under the standard system locations; Windows client under the standard program location).

### Key Entities

- **Server package**: The Debian distributable that installs the server as a managed service.
- **Windows installer**: The distributable that installs the desktop client and its virtual-network driver.
- **Service definition**: The managed-service configuration governing privilege (the single network-administration capability), automatic restart, and journaled logging.
- **Install layout**: The set of documented file locations for binary, configuration, database, and keys (server) and the program directory (client).
- **Uninstall policy**: The documented rules for what is removed versus retained on removal and purge.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a clean Debian system, a single install command results in the server running as a managed service shown as active.
- **SC-002**: After install, the service runs with only the network-administration capability (verified against the service definition and the running process).
- **SC-003**: After install, the service automatically restarts after a crash and its logs appear in the system journal.
- **SC-004**: The server's configuration and key files are readable only by their owner (not world-readable) 100% of the time.
- **SC-005**: On a clean Windows system, the installer installs the app and driver and a launch shortcut, and launching the app reaches first-run setup.
- **SC-006**: Uninstalling removes the program files and the server's service registration; persistent data is retained or removed exactly per the documented policy, and a purge removes it — verifiable in 100% of runs.
- **SC-007**: Upgrading an installed package preserves the operator's configuration and persistent data with no loss or overwrite (100%).
- **SC-008**: Every file in the documented install layout is present at its documented path after install (100%).

---

## Assumptions

- Builds on all prior features: the server binary (`lanweaved`, features 001–008) and the
  Windows client (features 009–011) already exist; this feature packages them and adds no
  product behavior.
- Server target is Debian/Ubuntu with the systemd init system, packaged as a `.deb`
  (DESIGN §10). The Windows client is packaged as a standard installer (`.msi`/`.exe`)
  that bundles the WinTun driver.
- The service runs as root with its capability set narrowed to the network-administration
  capability (kernel nftables + WireGuard require it), restarts on failure, and logs to the
  journal (DESIGN §10.4, constitution Security & Operational Discipline).
- A fresh install produces a runnable default configuration — a generated self-signed
  certificate and an initial administrator credential — so the service starts out of the
  box; the operator is expected to harden it (install a trusted certificate, change the
  administrator password). The package ships `config.toml.example`; the install creates an
  active configuration only if none exists, and never overwrites an existing one on upgrade.
- Install layout (DESIGN §10.2, with the ROADMAP's Windows path): server binary at
  `/usr/bin/lanweaved`; configuration at `/etc/lanweave/config.toml` (example at
  `config.toml.example`); database at `/var/lib/lanweave/db.sqlite`; server key at
  `/var/lib/lanweave/wg_private`; service definition at
  `/lib/systemd/system/lanweaved.service`; Windows client under `C:\Program Files\lanweave\`,
  with the client's secrets/state in their per-user locations (feature 009).
- Uninstall policy: a standard server removal keeps `/var/lib/lanweave` (data) and the
  configuration; a purge removes them. The Windows uninstaller removes program files and
  leaves (or offers to remove) the user's stored secrets and local state. This policy is
  documented with the packages.
- Backups are the operator's responsibility (DESIGN §10.5) and are out of scope here.
- Testing: the Debian package is built and its contents, layout, permissions, and
  service-definition fields are asserted automatically on the build host; a live install
  (`dpkg -i` → service active) on a clean Debian system and the Windows installer (with the
  bundled driver and elevation) are validated manually on the target operating systems — a
  documented, unavoidable exception for OS-level install/driver behavior, consistent with
  the GUI/driver exceptions in features 009–011.
