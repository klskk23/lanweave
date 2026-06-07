# Quickstart: Invite Code Expiry

Audience: operator and developer verifying slice 026.

## Configure

`config.toml`, `[auth]` section:

```toml
[auth]
jwt_secret = "..."
jwt_ttl    = "2h"
invite_ttl = "24h"   # how long a newly issued invite code stays redeemable.
                     # "0" or omitted = codes never expire.
```

- New deployments start from `config.toml.example`, which ships `invite_ttl = "24h"`,
  so expiry is on out of the box.
- Existing deployments that don't add the key keep the old behavior (no expiry).
- A negative value (e.g. `"-1h"`) fails startup with a config error.

## Issue a code and see its expiry

```console
$ lanweavectl invite
Invite code: kQ2v...redacted...8sB
Expires: 2026-06-08T07:00:00Z
```

With `invite_ttl` set to `0`/empty:

```console
$ lanweavectl invite
Invite code: kQ2v...redacted...8sB
Expires: never
```

## Behavior to verify

1. **In-window redemption succeeds**: issue a code, register with it immediately → success.
2. **Expired redemption rejected**: a code past its window → registration returns the
   generic `422 invite_invalid` ("Invite code is invalid or already used."). The
   response is identical to using an unknown or already-used code — you cannot tell it
   apart.
3. **Disable = never expires**: set `invite_ttl = "0"`, issue a code, it stays
   redeemable with no deadline (`Expires: never`).
4. **Upgrade safety**: a deployment with unused codes issued before this slice keeps
   those codes redeemable after upgrade (their `expires_at` is NULL).

## Run the tests

Integration tests use real SQLite and run namespace-isolated:

```console
$ unshare -rUn go test ./internal/server/store/... -run Invite
$ unshare -rUn go test ./internal/server/api/...   -run Register
```

Expiry is checked by inserting invite rows whose `expires_at` is in the past — no test
sleeps on the wall clock.

## Migration

`store/migrations/0006_invite_expires.sql` adds the nullable `expires_at` column.
Applied automatically by goose at startup; existing rows become NULL (never-expire).
