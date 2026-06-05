# Data Model: Deployment Packaging

**Feature**: 012-deployment-packaging | **Date**: 2026-06-06

No application data model. The "model" for a packaging feature is the **install layout** and
the **package metadata** — the durable contracts the package establishes on a host.

## Server install layout (Debian)

| Path | Owner / mode | Source | Notes |
|------|--------------|--------|-------|
| `/usr/bin/lanweaved` | root, `0755` | package | the service binary |
| `/etc/lanweave/config.toml.example` | root, `0644` | package (conffile) | example; no real secret |
| `/etc/lanweave/config.toml` | root, `0600` | postinstall (if absent) | active config: random admin pw + JWT secret |
| `/etc/lanweave/cert.pem` | root, `0644` | postinstall (if absent) | self-signed; replace for production |
| `/etc/lanweave/key.pem` | root, `0600` | postinstall (if absent) | self-signed key |
| `/etc/lanweave/initial-admin-password` | root, `0600` | postinstall (if absent) | the generated admin password, written here — not printed to logs (M1); delete after changing it |
| `/var/lib/lanweave/` | root, `0700` | postinstall | data dir |
| `/var/lib/lanweave/db.sqlite` | root, `0600` | created by the service | SQLite database |
| `/var/lib/lanweave/wg_private` | root, `0600` | created by the service | server WireGuard key |
| `/lib/systemd/system/lanweaved.service` | root, `0644` | package | the managed-service unit |

## Windows client install layout

| Path | Source | Notes |
|------|--------|-------|
| `C:\Program Files\lanweave\lanweave-client.exe` | installer | the desktop client |
| `C:\Program Files\lanweave\wintun.dll` | installer | bundled virtual-network driver |
| Start-menu / desktop shortcut | installer | launch shortcut |
| `%LOCALAPPDATA%\lanweave\state.json` | the app (feature 009) | local state (not installed) |
| OS secure store (DPAPI) | the app (feature 009) | device key + session token (not installed) |

## Package metadata (the `.deb`)

| Field | Value |
|-------|-------|
| Package | `lanweave` (server) |
| Version | from the build (`VERSION`) |
| Maintainer scripts | `postinstall`, `preremove`, `postremove` |
| Conffiles | `/etc/lanweave/config.toml.example` |
| Depends | `openssl` (postinstall self-signed cert, M2); systemd present on the target |

## Service unit (the managed-service definition)

| Directive | Value | Why |
|-----------|-------|-----|
| `User` | `root` | kernel nftables + WireGuard require it |
| `CapabilityBoundingSet` | `CAP_NET_ADMIN` | least privilege (constitution mandate) |
| `Restart` | `on-failure` | resilience (DESIGN §10.4) |
| `StandardOutput`/`StandardError` | `journal` | journaled logs (DESIGN §10.4) |
| `ExecStart` | `/usr/bin/lanweaved -config /etc/lanweave/config.toml` | run with the active config |

## Uninstall policy (documented)

| Action | Server | Windows client |
|--------|--------|----------------|
| remove | stop + disable; **keep** `/var/lib/lanweave` + `/etc/lanweave` | remove program files; **keep** user secrets/state |
| purge | also remove `/var/lib/lanweave` + `/etc/lanweave` | (manual) remove `%LOCALAPPDATA%\lanweave` + secure-store entries |

## Invariants

- The active config, key, and data are owner-only (`0600`/`0700`, root); the example config
  carries no real secret (FR-006/SC-004).
- Per-install secrets (admin password, JWT secret) are random; the self-signed cert is
  flagged for replacement (FR-005).
- An upgrade never overwrites an existing `config.toml` or the data (FR-012).
- `remove` is non-destructive to data; only `purge` removes it (FR-011).
- Every path in this layout is present after install (SC-008).
