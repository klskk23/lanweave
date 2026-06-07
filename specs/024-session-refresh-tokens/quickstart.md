# Quickstart: session-refresh-tokens (024)

Two parts: an **operator/API walkthrough** (curl, runs in the privileged test env) and a
**manual GUI matrix** for the Windows client (the parts under the Constitution II GUI/exec
exemption). Default server `https://localhost:8443`; `-k` skips TLS for the local self-signed cert.

## A. API walkthrough (curl)

### A1. Login now returns a refresh token

```sh
curl -sk https://localhost:8443/api/v1/login \
  -d '{"username":"alice","password":"<password>"}'
# → {"token":"<access-jwt>","refresh_token":"<opaque>"}
```
**Expect**: both fields present. Capture them:
```sh
RESP=$(curl -sk https://localhost:8443/api/v1/login -d '{"username":"alice","password":"<password>"}')
ACCESS=$(echo "$RESP" | jq -r .token)
RT=$(echo "$RESP" | jq -r .refresh_token)
```

### A2. Refresh exchanges the RT for a fresh access token

```sh
curl -sk -X POST https://localhost:8443/api/v1/refresh -d "{\"refresh_token\":\"$RT\"}"
# → {"token":"<fresh access-jwt>"}
```
**Expect**: 200 + a new `token`. The same `$RT` stays valid and its 30-day expiry slides. Run it
twice — both succeed (no rotation).

### A3. A refreshed access token actually works

```sh
NEW=$(curl -sk -X POST https://localhost:8443/api/v1/refresh -d "{\"refresh_token\":\"$RT\"}" | jq -r .token)
curl -sk https://localhost:8443/api/v1/me -H "Authorization: Bearer $NEW"
# → {"user_id":...,"username":"alice","is_admin":false}
```
**Expect**: 200 — the token from `/refresh` is a normal access token.

### A4. Logout revokes the RT (idempotent)

```sh
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/logout -d "{\"refresh_token\":\"$RT\"}"
# → 204
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/logout -d "{\"refresh_token\":\"$RT\"}"
# → 204   (idempotent: already revoked)
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/refresh -d "{\"refresh_token\":\"$RT\"}"
# → 401   (revoked RT no longer refreshes)
```
**Expect**: 204, 204, then 401.

### A5. Unknown / malformed inputs

```sh
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/refresh -d '{"refresh_token":"not-a-real-token"}'  # → 401
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/refresh -d '{}'                                     # → 400
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/logout  -d '{"refresh_token":"not-a-real-token"}'  # → 204 (idempotent)
```

### A6. Deleting a user invalidates their RTs (cascade)

```sh
# As admin: delete alice, then her previously captured RT must fail to refresh.
curl -sk -X DELETE https://localhost:8443/api/v1/admin/users/<alice_id> -H "Authorization: Bearer $ADMIN"
curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://localhost:8443/api/v1/refresh -d "{\"refresh_token\":\"$RT_ALICE\"}"  # → 401
```
**Expect**: after deletion, every one of alice's RTs returns 401 on refresh.

## B. Manual GUI matrix (Windows client)

These verify the end-user win and the keyring side of the flow (GUI/exec exemption, DESIGN §11).

| # | Action | Expected |
|---|--------|----------|
| B1 | Sign in via the wizard (create or existing account), reach the panel. | Panel loads; `lanweave-refresh-token` now present in Windows Credential Manager (alongside the session token + device key). |
| B2 | Keep the app open past the access-token lifetime (or temporarily shorten `jwt_ttl` server-side for the test), then perform a panel action (refresh devices / list zones). | Action succeeds with **no password prompt**. (Previously this prompted re-login.) |
| B3 | Close and reopen the client within 30 days. | Goes straight to the panel, no password prompt. |
| B4 | On the server, revoke this device's RT (`POST /logout` with its RT, or delete the user), then trigger a panel action after the access token expires. | The client's silent refresh fails and it falls back to the password sign-in screen — no stack trace, a human-readable prompt. |
| B5 | Click **Log out** in the panel. | Server-side RT is revoked (a subsequent `/refresh` with it → 401); `lanweave-refresh-token`, the session token, and the device key are all removed from Credential Manager; app returns to the wizard. |
| B6 | Register a brand-new account through the wizard. | Registration succeeds, the client logs in automatically, and an RT is stored — i.e. register itself issued no session, the follow-up login did. |

## C. Automated test pointers (Constitution II)

- Server store (`refresh_tokens_test.go`, real SQLite, injected clock): issue+validate;
  validate slides `expires_at`; expired→invalid; revoked→invalid; unknown→invalid; revoke
  idempotent; delete-user cascades RTs away.
- Server API (`auth_handlers_test.go`, real router+store): `/login` returns RT; `/refresh`
  valid→new token, expired/revoked/unknown→401, missing field→400; `/logout`→204 idempotent;
  delete-user then refresh→401. Assert no plaintext RT in logs.
- Client apiclient (`client_test.go`, httptest): authed 401 → silent `/refresh` → original call
  retried and succeeds; `/refresh` fails → surfaces `ErrSessionExpired`; `Logout()` posts the RT.
- Client onboard/panel (`*_test.go`, fake api): RT persisted after `Provision`/`SignIn`, restored
  by `LoadSession`, deleted by `Cleanup`/`Logout`.

**Run**: `unshare -rUn go test ./...` (real SQLite/no mocks per Constitution II).
