# Contract: `config.toml` Schema & Validation

The single configuration file consumed once at startup (FR-001..004). Field layout
mirrors DESIGN.md §10.3. Fields marked **(this feature)** are actively used now;
fields marked **(reserved)** are only validated for presence/format and consumed by
a later feature.

## Schema

```toml
[server]
listen   = "0.0.0.0:8443"              # (this feature) HTTPS bind address host:port
tls_cert = "/etc/lanweave/cert.pem"    # (this feature) PEM cert path, must exist & be readable
tls_key  = "/etc/lanweave/key.pem"     # (this feature) PEM private key path, must exist & be readable
data_dir = "/var/lib/lanweave"         # (this feature) dir for db.sqlite; must exist & be writable

[log]
level = "info"                         # (this feature) one of: debug|info|warn|error  (default: info)

[ratelimit]
rps   = 100                            # (this feature) tokens/sec (default: 100)
burst = 200                            # (this feature) bucket capacity (default: 200)

[wireguard]
network     = "100.127.0.0/16"         # (reserved) must parse as a valid CIDR
listen_port = 51820                    # (reserved) 1..65535
interface   = "wg-lanweave"            # (reserved)
mtu         = 1420                      # (reserved)

[auth]
jwt_secret = "REPLACE_WITH_32B_RANDOM" # (reserved) must be present, ≥ 32 bytes
jwt_ttl    = "2h"                       # (reserved) must parse as a Go duration

[admin]
username = "admin"                     # (this feature) non-empty, ≤ 64 chars
password = "change-me"                 # (this feature) non-empty plaintext (hashed at bootstrap)
```

## Validation rules (`config.Validate`)

`Validate()` collects **all** failures and returns them joined, so the operator can
fix the file in one pass (research.md R7). Startup aborts with a non-zero exit code
on any failure (FR-003, FR-012, FR-016).

| Field | Rule | Spec ref |
|-------|------|----------|
| `server.listen` | non-empty; parses as `host:port` | FR-002/003 |
| `server.tls_cert` | non-empty; file exists; readable; valid PEM cert | FR-003, US1-3 |
| `server.tls_key` | non-empty; file exists; readable; valid PEM key; pairs with cert | FR-003, US1-3 |
| `server.data_dir` | non-empty; directory exists; writable | FR-003, edge: no write perm |
| `log.level` | empty → default `info`; else one of debug/info/warn/error | FR-020 |
| `ratelimit.rps` | empty/0 → default 100; else > 0 | FR-022 |
| `ratelimit.burst` | empty/0 → default 200; else ≥ rps | FR-022 |
| `wireguard.network` | non-empty; parses via CIDR | FR-002/003 |
| `auth.jwt_secret` | non-empty; ≥ 32 bytes | FR-002 |
| `auth.jwt_ttl` | empty → default 2h; else valid duration | FR-002 |
| `admin.username` | non-empty; ≤ 64 chars | FR-002, FR-013 |
| `admin.password` | non-empty | FR-002, FR-016, US2-3 |

## Secret handling

- `auth.jwt_secret` and `admin.password` are secrets. Their struct fields implement
  `slog.LogValuer` returning `"[REDACTED]"`, so a stray `slog.Any("config", cfg)`
  cannot leak them (FR-019). A test asserts neither value appears in captured logs.
- The file is the operator's responsibility to `chmod 600` and keep out of git
  (DESIGN.md §11, constitution Security — accepted risk).

## Config-path resolution (single override bridge)

1. `--config <path>` flag (highest precedence).
2. else `LANWEAVE_CONFIG` env var.
3. else default `/etc/lanweave/config.toml`.

No other environment variables are read anywhere (Principle I).
