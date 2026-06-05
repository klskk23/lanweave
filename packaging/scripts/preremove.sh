#!/bin/sh
# lanweave server pre-remove: on removal (or purge), stop and disable the service. On an
# upgrade, leave it running (the new package restarts it).
set -e

case "$1" in
    remove|purge)
        systemctl disable --now lanweaved.service >/dev/null 2>&1 || true
        ;;
esac

exit 0
