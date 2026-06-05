# Quickstart: Deployment Packaging

**Feature**: 012-deployment-packaging | **Date**: 2026-06-06

Validates that the server `.deb` builds and contains the right layout/unit/scripts, and
documents the live install and the Windows installer (validated on the target OSes).

## Automated checks (build host)

```bash
# Build the .deb and assert layout, permissions, unit fields, and maintainer scripts.
go test ./packaging/...

# (equivalently) build the package directly:
make deb   # → dist/lanweave_<version>_amd64.deb
```

The packaging test skips when `nfpm`/`dpkg-deb` are unavailable.

## Scenario A — server package contents (US1, automated)

The test runs `make deb`, then:
- `dpkg-deb -c` lists the package → assert `/usr/bin/lanweaved`,
  `/etc/lanweave/config.toml.example`, `/lib/systemd/system/lanweaved.service` are present.
- `dpkg-deb -e` extracts control + scripts → assert the unit has `User=root`,
  `CapabilityBoundingSet=CAP_NET_ADMIN`, `Restart=on-failure`, journal output; assert
  `postinst`/`prerm`/`postrm` exist and are executable.
- assert `config.toml.example` contains no real secret (placeholders only).

## Scenario B — server live install (US1, manual on a clean Debian host)

```bash
sudo dpkg -i lanweave_<version>_amd64.deb
systemctl status lanweaved          # → active (running)
journalctl -u lanweaved             # → startup logs
systemctl show lanweaved -p CapabilityBoundingSet   # → CAP_NET_ADMIN
ls -l /etc/lanweave/config.toml     # → -rw------- root
sudo kill -9 $(pidof lanweaved); sleep 3; systemctl is-active lanweaved  # → active (auto-restart)
```

## Scenario C — uninstall / purge (US3, manual)

```bash
sudo apt remove lanweave     # service gone; /var/lib/lanweave kept
ls /var/lib/lanweave         # data still present
sudo apt purge lanweave      # data + config removed
ls /var/lib/lanweave         # gone
```

## Scenario D — upgrade preserves config (US3, manual)

Install, edit `/etc/lanweave/config.toml`, install a newer package → the edited config and
the database are unchanged.

## Scenario E — Windows installer (US2, manual on Windows)

Run the installer (NSIS-built) → accept the UAC prompt → confirm `lanweave-client.exe` +
`wintun.dll` under `C:\Program Files\lanweave\`, a launch shortcut, and that launching the
app enters the first-run wizard. Uninstall → program files removed, user secrets/state kept.

## Success

- Scenario A passes automatically (build + inspect the real `.deb`).
- Scenarios B–E pass by manual inspection on a clean Debian host and a clean Windows machine
  (the documented OS-level install / driver exception).
