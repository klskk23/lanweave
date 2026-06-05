# Quickstart: Invites and User Auth

Builds on the feature-001 quickstart (same build, cert, config, run steps). This
adds the auth/invite flow verification. Assumes the server is running on
`https://localhost:8443` with the bootstrap admin `admin` / `<your-admin-password>`.

Set a helper for the self-signed cert:
```bash
CURL="curl -sk"   # -k because the dev cert is self-signed
BASE=https://localhost:8443/api/v1
```

## US1 — admin logs in and identifies

```bash
# Login → token
TOKEN=$($CURL -X POST $BASE/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your-admin-password>"}' | jq -r .token)
echo "token: ${TOKEN:0:16}..."

# /me with token → 200 identity
$CURL $BASE/me -H "Authorization: Bearer $TOKEN"
# → {"user_id":1,"username":"admin","is_admin":true}

# /me without token → 401
$CURL -o /dev/null -w "%{http_code}\n" $BASE/me            # 401
# /me with garbage token → 401
$CURL -o /dev/null -w "%{http_code}\n" $BASE/me -H "Authorization: Bearer not.a.jwt"   # 401
```

### US1 — no user enumeration

```bash
# wrong password and unknown user return the SAME 401 + body
$CURL -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"wrong"}'
$CURL -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"ghost","password":"whatever"}'
# both → {"error":"invalid_credentials","message":"..."}  with status 401
```

## US2 — admin issues and lists invites

```bash
# Generate a code (admin token)
CODE=$($CURL -X POST $BASE/admin/invites -H "Authorization: Bearer $TOKEN" | jq -r .code)
echo "invite: $CODE"

# List → the new code shows as unused
$CURL $BASE/admin/invites -H "Authorization: Bearer $TOKEN" | jq .

# Non-admin / unauthenticated are refused
$CURL -o /dev/null -w "%{http_code}\n" -X POST $BASE/admin/invites   # 401 (no token)
```

## US3 — invitee registers, then logs in

```bash
# Register with the code
$CURL -X POST $BASE/register -H 'Content-Type: application/json' \
  -d "{\"invite_code\":\"$CODE\",\"username\":\"bob\",\"password\":\"bobs-strong-pw\"}" \
  -w "\n%{http_code}\n"
# → {"username":"bob","is_admin":false}  201

# The new user can log in (and is NOT admin)
BOB=$($CURL -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"bob","password":"bobs-strong-pw"}' | jq -r .token)
$CURL $BASE/me -H "Authorization: Bearer $BOB"
# → {"user_id":2,"username":"bob","is_admin":false}

# bob is not admin → invite endpoints forbidden
$CURL -o /dev/null -w "%{http_code}\n" -X POST $BASE/admin/invites -H "Authorization: Bearer $BOB"   # 403
```

### US3 — one-time code cannot be reused

```bash
# The code bob used is now consumed
$CURL -X POST $BASE/register -H 'Content-Type: application/json' \
  -d "{\"invite_code\":\"$CODE\",\"username\":\"carol\",\"password\":\"carols-strong-pw\"}" \
  -w "\n%{http_code}\n"
# → {"error":"invite_invalid",...}  422 ; no carol account created

# Nonexistent code → also refused
$CURL -X POST $BASE/register -H 'Content-Type: application/json' \
  -d '{"invite_code":"totally-made-up","username":"dave","password":"daves-strong-pw"}' \
  -w "\n%{http_code}\n"   # 422

# Taken username (with a fresh code) → 409, code stays unused
CODE2=$($CURL -X POST $BASE/admin/invites -H "Authorization: Bearer $TOKEN" | jq -r .code)
$CURL -X POST $BASE/register -H 'Content-Type: application/json' \
  -d "{\"invite_code\":\"$CODE2\",\"username\":\"bob\",\"password\":\"another-strong-pw\"}" \
  -w "\n%{http_code}\n"   # 409 username_taken
```

## US4 — security boundaries

```bash
# One-time race: fire many registrations with ONE code, expect exactly one 201
CODE3=$($CURL -X POST $BASE/admin/invites -H "Authorization: Bearer $TOKEN" | jq -r .code)
seq 1 50 | xargs -P 25 -I{} curl -sk -o /dev/null -w "%{http_code}\n" -X POST $BASE/register \
  -H 'Content-Type: application/json' \
  -d "{\"invite_code\":\"$CODE3\",\"username\":\"racer{}\",\"password\":\"racer-strong-pw\"}" \
  | sort | uniq -c
# → exactly one 201, the rest 422 (invite_invalid)

# No secrets in logs: scan the server log for the admin password / a token / a code
grep -E 'bobs-strong-pw|'"$CODE"'|'"${TOKEN:0:20}" /path/to/server.log && echo "LEAK!" || echo "no secrets in logs ✓"
```

## Automated tests

```bash
go test ./...            # unit + integration (real temp SQLite incl. one-time race) + acceptance
make lint                # gofmt + vet + staticcheck
```

All three tiers must be green before this feature is done (constitution Principle II).
