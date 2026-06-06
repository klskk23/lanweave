# lanweave

**A self-hosted, invite-only mesh VPN: a Go server/relay + a Windows desktop client, with
WireGuard tunnels and nftables-enforced zone isolation.**

> 中文版: [docs/README.zh.md](docs/README.zh.md) · Full build/install/run guide:
> [docs/GUIDE.en.md](docs/GUIDE.en.md)

---

## What is lanweave

lanweave lets a small group of devices form a private overlay network. An operator runs one server
(which is also the WireGuard relay); users join with an **invite code**, register their devices
(nodes), and are placed on a `100.127.0.0/16` overlay. Devices only reach each other when they
share a **zone** (a password-protected group); the server enforces this with nftables. The server's
SQLite database is the single source of truth — WireGuard peers and firewall rules are rebuilt from
it.

## Features

- Invite-based registration, JWT sessions (argon2id password hashing)
- Per-node IP allocation (IPAM) on a `100.127.0.0/16` overlay
- WireGuard data plane (server interface + per-node peers)
- Zones: same-zone devices interconnect, cross-zone traffic is dropped (nftables)
- Zone-owner controls: change password, kick members, delete a zone
- Node online status from WireGuard handshakes
- Cascade deletes: removing a user cleans up nodes, IPs, peers, and zone membership
- Windows desktop client (Fyne): first-run wizard, connect/disconnect, node/zone management,
  automatic UAC self-elevation
- Packaging: Debian `.deb` (systemd, least-privilege `CAP_NET_ADMIN`) and an NSIS Windows installer
- CI/CD: GitHub Actions builds + drafts a release on every `v*` tag

## Architecture

Clients dial the server over WireGuard (UDP); all overlay traffic is relayed through the server,
where nftables applies zone rules. The Go server owns SQLite (state), the WireGuard interface, and
the nftables table; both derivative states are reconstructible from the database at any time. The
server runs on Linux as root with a narrowed `CAP_NET_ADMIN`; the client is Windows-only for v1.

## Quick start

- **Server (Debian/Ubuntu)**: install a release `.deb` (`sudo dpkg -i lanweave_<ver>_amd64.deb`) or
  build it with `make deb`. On first install it self-configures and starts `lanweaved.service`.
- **Windows client**: run the release installer (`lanweave-client-<ver>-setup.exe`), accept the UAC
  prompt, then follow the first-run wizard (server URL → invite code → device name).
- Get an invite code with `/usr/local/bin/lanweave-invite-codegen.sh` on the server.
- Full details (toolchains, ports, hardening, troubleshooting): **[docs/GUIDE.en.md](docs/GUIDE.en.md)**.

## Documentation

| Doc | Description |
|-----|-------------|
| [docs/GUIDE.en.md](docs/GUIDE.en.md) | Build, install & run guide (English) |
| [docs/GUIDE.zh.md](docs/GUIDE.zh.md) | 编译·安装·运行指南 (中文) |
| [DESIGN.md](DESIGN.md) | Frozen v1 design (中文) |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Feature slices & status |

## Status

v1 design is frozen. The 14 feature slices (server foundation through CI/CD) are implemented; the
Windows GUI/elevation paths and the release pipeline are validated manually (documented in each
feature's spec). Default ports: API `tcp/8443`, WireGuard `udp/51820`.

## Security

Self-host this on a server you control. Release artifacts are **unsigned** (verify with the
`SHA256SUMS` attached to each release; expect a Windows SmartScreen warning). The `.deb`'s
post-install **disables ufw** and uses a self-signed TLS cert by default — review both before
production. Accepted project-wide risks are listed in [DESIGN.md §11](DESIGN.md).

## License

**GPLv3.** The `LICENSE` file is added on GitHub (web UI) after the first push.
