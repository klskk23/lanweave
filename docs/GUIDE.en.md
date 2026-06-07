# lanweave — Build, Install & Run Guide

> 中文版见 [GUIDE.zh.md](./GUIDE.zh.md).

This guide covers the three operational flows end to end: **building** the server and the Windows
client, **installing** them, and **running** them (including getting an invite code and
connecting a client).

> Conventions
> - Server: Debian/Ubuntu, runs as root with `CAP_NET_ADMIN` (kernel WireGuard + nftables).
> - Client: Windows 10/11 desktop (Fyne GUI + WinTun).
> - Default ports: API `tcp/8443`, WireGuard `udp/51820`; VPN network `100.127.0.0/16`
>   (server = `100.127.0.1`).

---

## Contents

1. [Building](#1-building)
   - 1.1 Server
   - 1.2 Windows client
   - 1.3 Windows installer (NSIS)
2. [Installing](#2-installing)
   - 2.1 Server `.deb`
   - 2.2 Windows client
3. [Running](#3-running)
   - 3.1 Server service
   - 3.2 Firewall & ports
   - 3.3 Getting an invite code
   - 3.4 Windows client first run
   - 3.5 Connect & verify
   - 3.6 Troubleshooting
4. [Uninstall & data retention](#4-uninstall--data-retention)

---

## 1. Building

> **Official releases are built automatically.** Pushing a `vX.Y.Z` tag runs GitHub Actions,
> which tests, builds the `.deb` + Windows installer, and drafts a GitHub Release with all
> artifacts and a `SHA256SUMS` file (reviewed, then published manually). The steps below are for
> local/manual builds.

### 1.1 Server

Requires Go 1.26. Build the static server binary, or build the Debian package (which also builds
the binary). `make deb` needs [`nfpm`](https://github.com/goreleaser/nfpm).

```sh
make build      # → ./lanweaved
make deb        # → dist/lanweave_<version>_amd64.deb   (requires nfpm)
```

Equivalent without make:

```sh
CGO_ENABLED=0 go build -ldflags "-X main.version=0.1.0" -o lanweaved ./cmd/lanweaved
```

### 1.2 Windows client

The GUI client uses Fyne, which needs **cgo**, so you must build **on Windows** with a C
toolchain. Install one of:

- **MSYS2 (recommended)** — install from <https://www.msys2.org>, open the **UCRT64** shell,
  then:
  ```bash
  pacman -Syu                                  # update, reopen shell if asked
  pacman -S mingw-w64-ucrt-x86_64-gcc          # install gcc
  ```
  Add `C:\msys64\ucrt64\bin` to your Windows **PATH**.
- **TDM-GCC** — install from <https://jmeubank.github.io/tdm-gcc/>, choose 64-bit, let it add
  itself to PATH.

Verify: `gcc --version` and `go version` both work in a fresh terminal.

Build the client:

> **First generate the icon resources**: run `make icons` (needs `rsvg-convert`, `icotool`, and
> a MinGW `windres`). It regenerates, from `packaging/icon.svg`, the gitignored
> `cmd/lanweave-client/resources_windows.syso` (the embedded EXE icon) and
> `internal/client/ui/icon.png` (the window icon). Skipping this yields a working but
> **unbranded** build.

```bat
set CGO_ENABLED=1
go build -tags gui -ldflags "-H windowsgui -X main.version=0.1.0" -o lanweave-client.exe .\cmd\lanweave-client
```

- `-tags gui` enables the real Fyne UI (without it you get a headless stub).
- `-H windowsgui` suppresses the console window.

Then place the matching **`wintun.dll`** (amd64) next to `lanweave-client.exe`. Download the
official zip from <https://www.wintun.net> and take `bin\amd64\wintun.dll`.

> **Gotcha — `error obtaining VCS status: exit status 128`**
> Go stamps git info into the binary. On Windows this often fails with git's "dubious ownership"
> guard. Fix either way:
> ```bat
> git config --global --add safe.directory "C:/path/to/lanweave"   :: root cause
> ```
> or add `-buildvcs=false` to the `go build` command (we already inject the version via
> `-ldflags`, so nothing is lost).

### 1.3 Windows installer (NSIS)

Package the client with [NSIS](https://nsis.sourceforge.io). Put the three files in **one
directory** (the script references the exe/dll by relative name):

```
packaging\windows\
├── lanweave-client.nsi
├── icon.ico                 # from `make icons` (copy of packaging\icon.ico)
├── lanweave-client.exe      # from 1.2
└── wintun.dll               # amd64
```

```bat
cd packaging\windows
makensis lanweave-client.nsi                 :: → lanweave-client-setup.exe (version 0.0.0-dev)
makensis /DVERSION=0.1.0 lanweave-client.nsi :: stamp a real version into Add/Remove Programs
```

- The installer requests admin only to install the WinTun driver / write to Program Files.
- The **installed app self-elevates (UAC) at runtime** to create the network adapter — no
  manifest is embedded (see `internal/client/winelevate`).
- The `.nsi` must stay **pure ASCII**; non-ASCII characters cause `Bad text encoding`.

---

## 2. Installing

### 2.1 Server `.deb`

```sh
sudo dpkg -i dist/lanweave_<version>_amd64.deb
# or, to also pull the openssl dependency:
sudo apt install ./dist/lanweave_<version>_amd64.deb
```

On a **first** install the post-install step makes the service runnable out of the box:

- creates `/var/lib/lanweave/` (root-only) for the database and server key;
- if `/etc/lanweave/config.toml` is absent, generates it from the example with a **random** admin
  password and JWT secret (`0600`, root);
- generates a **self-signed** `cert.pem` / `key.pem` via `openssl`;
- writes the generated admin password to **`/etc/lanweave/initial-admin-password`** (`0600`) — it
  is **not** printed to the terminal or journal;
- enables and starts `lanweaved.service`.

> ⚠️ **Firewall note**: the post-install script also runs **`ufw disable`**, turning off the host
> firewall. If you rely on ufw, re-enable it and open the ports in §3.2.

Harden before production (REQUIRED):

1. Read the initial admin password:
   ```sh
   sudo cat /etc/lanweave/initial-admin-password
   ```
2. Replace the self-signed cert at `/etc/lanweave/cert.pem` / `key.pem` with a trusted one
   (e.g. Let's Encrypt), or pre-install the self-signed CA on every client.
3. Change the admin password (via the client), then delete the file:
   ```sh
   sudo rm -f /etc/lanweave/initial-admin-password
   ```

Upgrade: `sudo dpkg -i <newer>.deb` — preserves your `config.toml` and `/var/lib/lanweave/` data
(the example config never overwrites your active config).

### 2.2 Windows client

Run `lanweave-client-setup.exe` and **accept the UAC prompt** (needed to install the WinTun driver
and write to Program Files). It installs `lanweave-client.exe` + `wintun.dll` to
`C:\Program Files\lanweave\` and creates Start-menu / desktop shortcuts.

---

## 3. Running

### 3.1 Server service

```sh
systemctl status lanweaved      # → active (running)
journalctl -u lanweaved -f      # follow logs
```

The service runs as root with `CapabilityBoundingSet=CAP_NET_ADMIN`, restarts on failure, and logs
to the journal. It also enables host IPv4 forwarding (`net.ipv4.ip_forward=1`) at startup, so you
do **not** need to set it manually.

### 3.2 Firewall & ports

If a firewall is active (the `.deb` disables ufw — see §2.1), open:

| Port        | Purpose |
|-------------|---------|
| `tcp/8443`  | HTTPS API (login, register, node/zone management) |
| `udp/51820` | WireGuard data plane |

Cloud security groups (AWS/GCP/etc.) must allow the same.

### 3.3 Getting an invite code

Invite codes are minted by an **admin** via the API; the client uses one to register.

1. Use the admin helper (installed by the `.deb`): `lanweavectl invite` logs in as the configured
   admin and prints a fresh code. The same tool also does `lanweavectl user list` and
   `lanweavectl user del <username>`.
2. Or mint one manually:

   ```sh
   # 1) admin password (generated on first install)
   sudo cat /etc/lanweave/initial-admin-password

   # 2) log in → JWT (self-signed cert → -k for local bootstrap)
   TOKEN=$(curl -sk https://localhost:8443/api/v1/login \
     -d '{"username":"admin","password":"<password>"}' | jq -r .token)

   # 3) mint a one-time invite code
   curl -sk -X POST https://localhost:8443/api/v1/admin/invites \
     -H "Authorization: Bearer $TOKEN" | jq -r .code
   # -k skips TLS verification (local admin bootstrap only); from another host use
   # --cacert /etc/lanweave/cert.pem and the cert's hostname.
   ```
3. Invite codes are **one-time**; list status with `GET /api/v1/admin/invites`.

### 3.4 Windows client first run

Launch the client the ordinary way (double-click the shortcut). Windows shows **one UAC prompt** —
the app self-elevates because creating the network adapter needs administrator rights; you do
**not** need to right-click → "Run as administrator". Accept it, then the first-run wizard walks
you through:

1. Server URL — e.g. `https://vpn.example.com:8443`
2. Log in, or register a new account with the **invite code** from §3.3
3. Device (node) name → the app generates a WireGuard keypair and registers the node

The private key is stored in the Windows Credential Manager; node IP / server info go to
`%LOCALAPPDATA%\lanweave\state.json`.

> If you decline the UAC prompt, the app shows a short message and closes (it cannot create the
> adapter without elevation).

### 3.5 Connect & verify

1. Click **Connect** in the panel.
2. `ipconfig` shows a `100.127.x.y` adapter.
3. `ping 100.127.0.1` reaches the server.
4. To reach other devices, create or join a **zone** (name + password) in the panel; only members
   of the same zone can talk to each other.

### 3.6 Troubleshooting

| Symptom | Cause & fix |
|---------|-------------|
| "could not set up the network adapter" | Not running as admin, or `wintun.dll` missing. The client self-elevates (accept UAC); ensure `wintun.dll` (amd64) sits next to the exe. |
| TLS / certificate not trusted | Self-signed cert. Trust the CA on the client, or (advanced) run the client with `--insecure`. |
| Can't reach the server | Firewall/security group blocking; open `tcp/8443` + `udp/51820` (§3.2). |
| Connected but can't ping another node | The two nodes are not in the same zone. |

---

## 4. Uninstall & data retention

### Server

| Command | Effect |
|---------|--------|
| `sudo apt remove lanweave` (or `dpkg -r`) | stops + disables the service, removes program files; **keeps** `/var/lib/lanweave` + `/etc/lanweave` so a reinstall resumes |
| `sudo apt purge lanweave` (or `dpkg -P`) | also **removes** `/var/lib/lanweave` + `/etc/lanweave` |

Back up `/var/lib/lanweave/db.sqlite` yourself (it holds all state):
`sqlite3 db.sqlite .backup backup.sqlite`.

### Windows client

Uninstall from **Add/Remove Programs** (or `C:\Program Files\lanweave\uninstall.exe`). It removes
the program files but **keeps** your device identity: the key + session token in the **Windows
Credential Manager** and `%LOCALAPPDATA%\lanweave\state.json`.

To fully purge your identity, after uninstalling delete `%LOCALAPPDATA%\lanweave\` and remove the
lanweave entries from Credential Manager.

---

## Manual acceptance (target OSes)

- **Server**: on a clean Debian, `dpkg -i` → `systemctl status lanweaved` is active; `kill -9` of
  the process auto-restarts; `apt remove` keeps data, `apt purge` removes it.
- **Windows**: the installer installs the app + driver + shortcut and prompts for elevation;
  launching the app self-elevates (one UAC prompt), reaches first-run setup, and Connect creates
  the `100.127.x.y` adapter; uninstall removes program files and keeps the user's secrets/state.
