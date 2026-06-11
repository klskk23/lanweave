# lanweave-routerd on OpenWrt

Headless lanweave client for OpenWrt routers (feature 031): one binary, a
tunnel daemon plus CLI subcommands. Requires kernel WireGuard
(`kmod-wireguard`, present on all current OpenWrt releases) and root.

## Install

```sh
# on your build machine
make routerd-cross
scp dist/lanweave-routerd-<arch> root@router:/usr/bin/lanweave-routerd

# on the router
chmod +x /usr/bin/lanweave-routerd
```

Pick `<arch>`: `arm64` (most modern routers), `mipsle` (MT76xx etc.), `amd64`
(x86 boxes). Needs ~12 MB free on the overlay (64 MB+ flash devices).

## First-time setup

```sh
lanweave-routerd setup --server https://vpn.example.com
# self-signed server? the next command prints the certificate fingerprint —
# verify it out-of-band, then: lanweave-routerd trust <sha256>
echo -n 'YOUR_PASSWORD' | lanweave-routerd login --username alice
lanweave-routerd register --name home-router
```

State and credentials live in `/etc/lanweave/` (root-only, survives reboots
and sysupgrade if you add the directory to `/etc/sysupgrade.conf`).

## Run at boot

```sh
cp lanweave-routerd.init /etc/init.d/lanweave-routerd
chmod +x /etc/init.d/lanweave-routerd
/etc/init.d/lanweave-routerd enable
/etc/init.d/lanweave-routerd start
lanweave-routerd status
```

## Everyday commands

```sh
echo -n 'zone-password' | lanweave-routerd zone create homelab   # auto-joins
echo -n 'zone-password' | lanweave-routerd zone join otherzone
lanweave-routerd zone members homelab
lanweave-routerd status
lanweave-routerd logout            # deregisters this device, wipes local state
```

`logout` refuses when the server is unreachable (it would leave an orphan
node); `logout --force` wipes locally anyway and warns.
