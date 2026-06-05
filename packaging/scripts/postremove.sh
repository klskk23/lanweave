#!/bin/sh
# lanweave server post-remove. A plain `remove` keeps the data and config (so a reinstall
# resumes); a `purge` removes the data directory and the configuration (database, keys,
# certificates, and the generated config). This is the documented data-retention policy.
set -e

systemctl daemon-reload >/dev/null 2>&1 || true

if [ "$1" = "purge" ]; then
    rm -rf /var/lib/lanweave /etc/lanweave
fi

exit 0
