# lanweave — Install & Uninstall

This covers installing the **server** (Debian `.deb`) and the **Windows client**
(installer), plus the hardening steps and the uninstall / data-retention policy.

---

## Server (Debian/Ubuntu)

### Build the package

```sh
make deb        # → dist/lanweave_<version>_amd64.deb   (requires nfpm)
```

### Install

```sh
sudo dpkg -i dist/lanweave_<version>_amd64.deb
# (or: sudo apt install ./dist/lanweave_<version>_amd64.deb  — pulls the openssl dependency)
```

On a **first** install the post-install step makes the service runnable out of the box:

- creates `/var/lib/lanweave/` (root-only) for the database and server key;
- if `/etc/lanweave/config.toml` does not exist, generates it from
  `config.toml.example` with a **random** admin password and JWT secret (`0600`, root);
- generates a **self-signed** certificate at `/etc/lanweave/cert.pem` / `key.pem`;
- writes the generated admin password to **`/etc/lanweave/initial-admin-password`**
  (`0600`, root) — it is **not** printed to the terminal or the journal;
- enables and starts `lanweaved.service`.

```sh
systemctl status lanweaved          # → active (running)
journalctl -u lanweaved             # → startup logs
```

The service runs as `root` with `CapabilityBoundingSet=CAP_NET_ADMIN` (the kernel
nftables + WireGuard data plane needs it), restarts on failure, and logs to the journal.

### Harden before production (REQUIRED)

1. **Read the initial admin password**, then plan to delete the file:
   ```sh
   sudo cat /etc/lanweave/initial-admin-password
   ```
2. **Replace the self-signed certificate** at `/etc/lanweave/cert.pem` / `key.pem` with a
   trusted one (e.g. Let's Encrypt), or pre-install the self-signed CA on every client.
3. **Change the admin password** (through the client), then:
   ```sh
   sudo rm -f /etc/lanweave/initial-admin-password
   ```
4. Review `/etc/lanweave/config.toml` (`listen`, `network`, etc.).

### Upgrade

```sh
sudo dpkg -i dist/lanweave_<newer>_amd64.deb
```

An upgrade **preserves** your `/etc/lanweave/config.toml` and `/var/lib/lanweave/` data —
the example config never overwrites your active config, and the database/keys are untouched.

### Uninstall — data-retention policy

| Command | Effect |
|---------|--------|
| `sudo apt remove lanweave` (or `dpkg -r`) | stops + disables the service and removes the program files; **keeps** `/var/lib/lanweave` (database, key) and `/etc/lanweave` (config, certs) so a reinstall resumes |
| `sudo apt purge lanweave` (or `dpkg -P`) | also **removes** `/var/lib/lanweave` and `/etc/lanweave` |

Backups are your responsibility — `/var/lib/lanweave/db.sqlite` holds all state
(`sqlite3 db.sqlite .backup ...`).

---

## Windows client

### Build the installer (on Windows)

1. Build the GUI client:
   ```
   go build -tags gui -o lanweave-client.exe ./cmd/lanweave-client
   ```
2. Place the matching `wintun.dll` (amd64) next to `lanweave-client.exe`.
3. Build the installer with NSIS:
   ```
   makensis packaging/windows/lanweave-client.nsi   ->  lanweave-client-setup.exe
   ```

### Install

Run `lanweave-client-setup.exe` and **accept the UAC prompt** (administrator rights are
needed to install the WinTun driver and write to Program Files). It installs
`lanweave-client.exe` + `wintun.dll` to `C:\Program Files\lanweave\` and creates Start-menu
and desktop shortcuts. Launching the app on a fresh machine enters the **first-run wizard**.

### Uninstall — data-retention policy

Uninstall from **Add/Remove Programs** (or `C:\Program Files\lanweave\uninstall.exe`). It
removes the program files and shortcuts but **keeps** your device identity:

- the device private key + session token in the **Windows Credential Manager**, and
- `%LOCALAPPDATA%\lanweave\state.json`.

To fully purge your identity (e.g. before disposing of the machine), after uninstalling
delete `%LOCALAPPDATA%\lanweave\` and remove the lanweave entries from Credential Manager.

---

## Manual acceptance (target OSes)

- **Server**: on a clean Debian, `dpkg -i` → `systemctl status lanweaved` is active; a
  `kill -9` of the process is auto-restarted; `apt remove` keeps data, `apt purge` removes it.
- **Windows**: the installer installs the app + driver + shortcut, prompts for elevation,
  and the app reaches first-run setup; uninstall removes program files and keeps the user's
  secrets/state.
