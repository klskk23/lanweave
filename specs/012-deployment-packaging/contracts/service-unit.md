# Contract: systemd service unit & maintainer-script behavior

**Feature**: 012-deployment-packaging | **Date**: 2026-06-06

The managed-service definition and the install/uninstall behavior. The unit file's fields are
asserted by a test; the live behavior is validated manually on a Debian host.

## `lanweaved.service` (required directives, asserted by the test)

```ini
[Unit]
Description=lanweave VPN relay server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
CapabilityBoundingSet=CAP_NET_ADMIN
AmbientCapabilities=CAP_NET_ADMIN
ExecStart=/usr/bin/lanweaved -config /etc/lanweave/config.toml
Restart=on-failure
RestartSec=2
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

| Field | MUST be | Why |
|-------|---------|-----|
| `User` | `root` | kernel nftables + WireGuard need it |
| `CapabilityBoundingSet` | `CAP_NET_ADMIN` | least privilege (constitution) |
| `Restart` | `on-failure` | resilience (DESIGN §10.4) |
| `StandardOutput` / `StandardError` | `journal` | journaled logs |
| `ExecStart` | runs `/usr/bin/lanweaved -config /etc/lanweave/config.toml` | the installed binary + active config |

## Behavioral guarantees (from spec)

- **Active after install (SC-001)**: a fresh `dpkg -i` yields a service shown active (the
  postinstall generated a runnable config + cert).
- **Least privilege (SC-002)**: the running service has only `CAP_NET_ADMIN`.
- **Restart + journal (SC-003)**: a crash is auto-restarted; logs appear in `journalctl -u
  lanweaved`.
- **Owner-only secrets (SC-004)**: `config.toml`, `key.pem`, and the data dir are `0600`/
  `0700` root-owned.
- **Upgrade-safe (SC-007/FR-012)**: an upgrade keeps an existing `config.toml` and the data.
- **Uninstall policy (SC-006/FR-011)**: `remove` keeps data; `purge` removes it.

## Manual validation (documented exception)

- `dpkg -i lanweave_*.deb` → `systemctl status lanweaved` shows active, on a clean Debian
  host (needs systemd as PID 1).
- The Windows installer installs the app + WinTun + shortcut, requests elevation, and the
  app reaches first-run setup.
