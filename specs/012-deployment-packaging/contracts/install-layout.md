# Contract: Install layout & package contents

**Feature**: 012-deployment-packaging | **Date**: 2026-06-06

The durable contract the package establishes on a host. The packaging test asserts these
against the **real** built `.deb` (via `dpkg-deb -c`/`-e`/`-x`).

## Files the `.deb` MUST contain (asserted by `dpkg-deb -c`)

| Path in package | Mode |
|-----------------|------|
| `/usr/bin/lanweaved` | `0755` |
| `/etc/lanweave/config.toml.example` | `0644` |
| `/lib/systemd/system/lanweaved.service` | `0644` |

## Maintainer scripts the `.deb` MUST carry (asserted by `dpkg-deb -e`)

| Script | Behavior |
|--------|----------|
| `postinst` | create `/var/lib/lanweave` (`0700`); if `config.toml` absent, generate it (random admin pw + JWT secret, `0600`) + a self-signed cert via `openssl` (`0600` key); **write the generated admin password to `/etc/lanweave/initial-admin-password` (`0600`), not to stdout/logs (M1)**; `daemon-reload` + `enable` + `start`; print only the password-file path + hardening notes |
| `prerm` | stop + disable the service |
| `postrm` | on `purge`, remove `/var/lib/lanweave` and `/etc/lanweave` |

The package **`Depends` on `openssl`** (used by `postinst` for the self-signed certificate, M2).

## Content assertions (the test)

- `config.toml.example` ships and contains **no real secret** (it has placeholder/comment
  values, not a real password/JWT secret) — grep for the placeholder markers.
- `postinst` references `/var/lib/lanweave`, `/etc/lanweave/config.toml`, and the cert paths,
  and generates random secrets (not a hardcoded password).
- `postinst` does **not** echo the admin password — it writes it to
  `/etc/lanweave/initial-admin-password` (M1).
- The package's control file `Depends` includes `openssl` (M2).
- The maintainer scripts are executable.

## Windows installer contract (validated manually on Windows)

| Item | Requirement |
|------|-------------|
| Install dir | `C:\Program Files\lanweave\` |
| Files | `lanweave-client.exe` + `wintun.dll` |
| Shortcut | Start-menu and/or desktop launch shortcut |
| Elevation | requests administrator (driver install); no partial install if denied |
| First launch | enters the first-run wizard (feature 009) |

## Out of scope

- The server's runtime data files (`db.sqlite`, `wg_private`) are created by the service at
  first run, not shipped in the package.
