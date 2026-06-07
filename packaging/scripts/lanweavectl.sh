#!/bin/bash
# lanweavectl — lanweave server admin helper.
#
# Runs on the server host as root. It authenticates to the local control-plane
# API using the admin credentials from the root-only config, and reads the local
# SQLite database to resolve usernames to ids (there is no list-users API).
#
#   lanweavectl invite              mint a one-time invite code (this is how you
#                                   "add a user": hand them the code, they register)
#   lanweavectl user list           list users (id / username / admin / created)
#   lanweavectl user del <username> delete a user and ALL their nodes/zones
set -euo pipefail

CONFIG_FILE=/etc/lanweave/config.toml

die() { echo "lanweavectl: $*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 \
        || die "missing dependency '$1' (install: sudo apt install $1)"
}

require_root() {
    [ "$(id -u)" -eq 0 ] \
        || die "must run as root (needs the root-only config and database)"
}

usage() {
    cat >&2 <<'EOF'
lanweavectl — lanweave server admin helper (run as root on the server)

Usage:
  lanweavectl invite               Generate a one-time invite code (to add a user)
  lanweavectl user list            List users (id, username, admin, created)
  lanweavectl user del <username>  Delete a user and all their nodes/zones (confirms first)
EOF
}

# config_get KEY — print a top-level TOML value. The key is anchored at the start
# of the line (leading whitespace allowed) so comment lines (`# tls = ...`) and
# longer keys (`tls_cert`) never match. Quoted values are unquoted; a bare value
# has any trailing inline `# comment` stripped. Empty output = key absent.
config_get() {
    local key=$1 line val
    line=$(grep -E "^[[:space:]]*${key}[[:space:]]*=" "$CONFIG_FILE" | head -n1) || true
    [ -n "$line" ] || return 0
    val=${line#*=}                     # everything past the first '=' (value may contain '=')
    val=${val#"${val%%[![:space:]]*}"} # ltrim
    case $val in
        '"'*) val=${val#\"}; val=${val%%\"*} ;;          # "double quoted"
        "'"*) val=${val#\'}; val=${val%%\'*} ;;          # 'single quoted'
        *)    val=${val%%#*}; val=${val%"${val##*[![:space:]]}"} ;; # bare: drop inline comment + rtrim
    esac
    printf '%s' "$val"
}

db_path() {
    local data_dir
    data_dir=$(config_get data_dir)
    printf '%s' "${data_dir:-/var/lib/lanweave}/db.sqlite"
}

# sql_quote VALUE — escape a string for inlining into a single-quoted SQLite
# literal (doubles embedded single quotes).
sql_quote() { local s=$1; printf "%s" "${s//\'/\'\'}"; }

# auth — read admin credentials + listen settings from the config, log in, and
# set BASE_URL and AUTH_TOKEN. Requires root (config is mode 0600) and jq.
auth() {
    require_root
    need jq
    [ -r "$CONFIG_FILE" ] || die "cannot read $CONFIG_FILE"

    local username password port tls proto body
    username=$(config_get username)
    password=$(config_get password)
    port=$(config_get listen); port=${port##*:}
    tls=$(config_get tls)
    [ -n "$username" ] || die "could not read admin username from $CONFIG_FILE"
    [ -n "$port" ]     || die "could not read listen port from $CONFIG_FILE"
    if [ "$tls" = "false" ]; then proto=http; else proto=https; fi
    BASE_URL="$proto://localhost:$port/api/v1"

    body=$(jq -n --arg u "$username" --arg p "$password" '{username:$u,password:$p}')
    AUTH_TOKEN=$(curl -sk --max-time 10 -H 'Content-Type: application/json' \
        "$BASE_URL/login" -d "$body" | jq -r '.token // empty') || true
    [ -n "${AUTH_TOKEN:-}" ] \
        || die "admin login failed (check credentials in $CONFIG_FILE / server status)"
}

cmd_invite() {
    auth
    local resp code expires
    resp=$(curl -sk --max-time 10 -X POST "$BASE_URL/admin/invites" \
        -H "Authorization: Bearer $AUTH_TOKEN") || true
    code=$(printf '%s' "$resp" | jq -r '.code // empty')
    [ -n "$code" ] || die "failed to create invite code"
    # expires_at is omitted when the code never expires.
    expires=$(printf '%s' "$resp" | jq -r '.expires_at // empty')
    printf 'Invite code: \033[32m%s\033[0m\n' "$code"
    if [ -n "$expires" ]; then
        printf 'Expires: %s\n' "$expires"
    else
        printf 'Expires: never\n'
    fi
}

cmd_user_list() {
    require_root
    need sqlite3
    local db rows
    db=$(db_path)
    [ -r "$db" ] || die "cannot read database $db"
    rows=$(sqlite3 -separator '|' "$db" \
        "SELECT id, username, CASE is_admin WHEN 1 THEN 'yes' ELSE 'no' END, created_at
         FROM users ORDER BY id;") || die "database query failed"
    printf '%-6s %-24s %-6s %s\n' ID USERNAME ADMIN CREATED
    [ -n "$rows" ] || return 0
    while IFS='|' read -r id name admin created; do
        printf '%-6s %-24s %-6s %s\n' "$id" "$name" "$admin" "$created"
    done <<<"$rows"
}

cmd_user_del() {
    local username=${1:-}
    [ -n "$username" ] || die "usage: lanweavectl user del <username>"
    require_root
    need sqlite3
    local db row id is_admin adminnote ans
    db=$(db_path)
    [ -r "$db" ] || die "cannot read database $db"

    # Pre-check: resolve the username (UNIQUE COLLATE NOCASE) to its id.
    row=$(sqlite3 -separator '|' "$db" \
        "SELECT id, is_admin FROM users WHERE username = '$(sql_quote "$username")' COLLATE NOCASE;") \
        || die "database query failed"
    [ -n "$row" ] || die "no such user: $username"
    id=${row%%|*}; is_admin=${row##*|}
    adminnote=""; [ "$is_admin" = "1" ] && adminnote=" [ADMIN]"

    printf 'About to delete user "%s" (id=%s)%s and ALL their nodes/zones. This is irreversible.\n' \
        "$username" "$id" "$adminnote"
    printf 'Type y to confirm: '
    read -r ans || true
    [ "$ans" = "y" ] || die "aborted"

    # Deletion goes through the API so the cascade + data-plane sync run server-side.
    auth
    local status
    status=$(curl -sk --max-time 10 -o /dev/null -w '%{http_code}' \
        -X DELETE "$BASE_URL/admin/users/$id" \
        -H "Authorization: Bearer $AUTH_TOKEN") || true
    case $status in
        204) echo "deleted user \"$username\" (id=$id)";;
        403) die "server refused: cannot delete your own account";;
        409) die "server refused: cannot delete the last administrator";;
        404) die "server: user not found (id=$id)";;
        401) die "authentication failed";;
        *)   die "delete failed (HTTP ${status:-?})";;
    esac
}

main() {
    local cmd=${1:-}
    shift || true
    case $cmd in
        invite) cmd_invite "$@" ;;
        user)
            local sub=${1:-}
            shift || true
            case $sub in
                list) cmd_user_list "$@" ;;
                del)  cmd_user_del "$@" ;;
                *)    usage; exit 1 ;;
            esac
            ;;
        ''|-h|--help|help) usage ;;
        *) usage; exit 1 ;;
    esac
}

main "$@"
