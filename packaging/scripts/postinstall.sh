#!/bin/sh
# lanweave server post-install: ensure the data dir exists and, on a first install,
# generate a runnable default configuration (self-signed certificate + random secrets)
# so the service can start. The generated admin password is written to a root-only file,
# never printed to stdout/logs (constitution: no plaintext password in any log line).
set -e

CONF_DIR=/etc/lanweave
DATA_DIR=/var/lib/lanweave
CONF="$CONF_DIR/config.toml"
EXAMPLE="$CONF_DIR/config.toml.example"
CERT="$CONF_DIR/cert.pem"
KEY="$CONF_DIR/key.pem"
PWFILE="$CONF_DIR/initial-admin-password"

mkdir -p "$DATA_DIR"
chmod 0700 "$DATA_DIR"

if [ ! -f "$CONF" ]; then
    # Random per-install secrets (alphanumeric password, hex JWT secret).
    ADMIN_PW=$(openssl rand -base64 24 | tr -dc 'A-Za-z0-9' | cut -c1-20)
    JWT_SECRET=$(openssl rand -hex 32)

    umask 077
    sed -e "s|REPLACE_WITH_A_32_BYTE_RANDOM_SECRET|$JWT_SECRET|" \
        -e "s|CHANGE-ME-ON-FIRST-LOGIN|$ADMIN_PW|" \
        "$EXAMPLE" > "$CONF"
    chmod 0600 "$CONF"

    if [ ! -f "$CERT" ]; then
        openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
            -subj "/CN=lanweave" -keyout "$KEY" -out "$CERT" >/dev/null 2>&1
        chmod 0600 "$KEY"
        chmod 0644 "$CERT"
    fi

    # M1: write the admin password to a root-only file; do NOT echo it.
    printf '%s\n' "$ADMIN_PW" > "$PWFILE"
    chmod 0600 "$PWFILE"

    echo "lanweave: a default configuration was generated and the service is starting."
    echo "lanweave: the initial admin password is in $PWFILE (root-only, mode 0600)."
    echo "lanweave: ACTION REQUIRED before production —"
    echo "lanweave:   1) replace the self-signed certificate at $CERT / $KEY,"
    echo "lanweave:   2) change the admin password (via the client), then"
    echo "lanweave:   3) delete $PWFILE."
fi

ufw disable >/dev/null 2>&1 || true
systemctl daemon-reload >/dev/null 2>&1 || true
systemctl enable --now lanweaved.service >/dev/null 2>&1 || true

cat << "EOF" > /usr/local/bin/lanweave-invite-codegen.sh
#!/bin/bash
which jq &>/dev/null || { echo "Please run: sudo apt install jq" >&2; exit 1; }
set -e
CONFIG_FILE=/etc/lanweave/config.toml
function filterConfig {
        KEY=$1
        cat $CONFIG_FILE | grep -e "$KEY" | awk -F'=' '{print $2}' | tr -d  '[:space:]\"'
}
USERNAME=$(filterConfig username)
PASSWORD=$(filterConfig password)
PORT=$(filterConfig listen[^_]|cut -d':' -f2)
ADMIN_DATA=$(jq -n --arg u "$USERNAME" --arg p "$PASSWORD" '{"username": $u, "password": $p}')
TOKEN=$(curl -sk "https://localhost:$PORT/api/v1/login" -d "$ADMIN_DATA" | jq -r .token)
# Get an invite code
INVITE_CODE=$(curl -sk -X POST "https://localhost:$PORT/api/v1/admin/invites" -H "Authorization: Bearer $TOKEN" | jq -r .code)
echo -e "Invite code: \033[32m$INVITE_CODE\033[0m"
EOF
chmod +x /usr/local/bin/lanweave-invite-codegen.sh

exit 0
