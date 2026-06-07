# Research: session-refresh-tokens (024)

All major decisions were resolved up front during the design grill and recorded in
`docs/ROADMAP.md` §024. This file restates them in Decision / Rationale / Alternatives form and
grounds each in the current code. No open `NEEDS CLARIFICATION` remain.

## D1 — Mechanism: server-side refresh token (not stored password, not longer JWT)

- **Decision**: Issue a separate, server-side, revocable refresh token (RT). The access JWT
  stays short-lived (2h) and stateless; the RT is the long-lived, stateful, revocable credential.
- **Rationale**: The friction is the ~2h re-login (`internal/server/config/config.go:183` →
  `jwt_ttl="2h"`; client `panel.LoadSession` → `promptSignIn` on `ErrSessionExpired`). A separate
  RT removes the prompt without weakening the access token's short revocation window.
- **Alternatives considered**:
  - *Store the password in the keyring and silently re-login* — rejected: account-level credential
    on every device, far larger blast radius than a per-device revocable RT; diverges from
    DESIGN §7.3 ("client holds access token, not the password").
  - *Just raise `jwt_ttl`* — rejected: a stateless JWT cannot be revoked, so a longer TTL directly
    widens the un-revocable window; treats the symptom, not the cause.

## D2 — RT form & server storage: opaque random, store only the hash

- **Decision**: RT = `crypto/rand` 32 bytes → base64url, opaque. Server stores **only** the
  SHA-256 hash (`refresh_tokens.token_hash`, UNIQUE). The plaintext RT is returned to the client
  once at issuance and never persisted server-side in plaintext, never logged.
- **Rationale**: A stolen database dump yields only hashes, not usable tokens. SHA-256 (a fast
  hash) is correct here because the RT is high-entropy random — unlike `users.password_hash`,
  there is nothing to brute-force, so argon2id would add cost with no security gain. No new crypto
  primitive (Constitution Security: stdlib only). Constitution v1.0.1 clarifies that the argon2id
  MUST applies to low-entropy passwords; high-entropy `crypto/rand` tokens may use SHA-256, so this
  choice now aligns with the letter of the constitution, not just its spirit (resolves analyze C1).
  A deterministic SHA-256 also keeps the `token_hash` UNIQUE index usable for O(1) validate/revoke —
  a salted argon2id digest would force a selector/verifier split or a full-table scan per refresh.
- **Alternatives considered**:
  - *Store RT plaintext* — rejected: DB compromise = full session takeover.
  - *Make the RT a second JWT* — rejected: a JWT is self-validating and therefore **not**
    revocable without server state anyway; we specifically need server-side revocation, so an
    opaque DB-backed token is simpler and meets the requirement directly.
  - *argon2id the RT* — rejected: unnecessary work for a high-entropy secret; SHA-256 suffices.

## D3 — Lifetime: 30-day sliding window, validated against the DB

- **Decision**: `expires_at = now + 30d` at issue; each successful `/refresh` slides it to
  `now + 30d`. Validation rejects an RT whose `expires_at` is in the past or whose `revoked_at`
  is set. Fixed 30 days, not configurable this slice.
- **Rationale**: Active users never hit the wall (every refresh extends it); an abandoned device
  stops working after 30 idle days, bounding exposure. The single SQLite writer makes the
  read-validate-update safe without extra locking.
- **Alternatives considered**:
  - *Absolute (non-sliding) expiry* — rejected: would force re-login on a fixed cadence even for
    active users, re-introducing the very friction we are removing.
  - *Configurable TTL* — deferred: no operator has asked; a constant keeps config surface flat
    (Constitution I). Easy to promote to config later if needed.

## D4 — No rotation, no replay detection

- **Decision**: An RT is reusable until it expires or is explicitly revoked. `/refresh` does not
  mint a new RT or invalidate the presented one.
- **Rationale**: Rotation (one-time-use RTs) requires detecting and handling reuse races —
  concurrent in-flight requests on the client (several queued calls can all hit 401 at once, see
  `apiclient.do`) would each try to refresh and would invalidate each other under rotation. Without
  rotation, concurrent refreshes are idempotent and safe, which matches the lazy-refresh design.
- **Alternatives considered**:
  - *Rotating RTs with reuse detection* — rejected for v1: adds a race-prone state machine for
    marginal benefit on a self-hosted, small-group tool; can be added later if a stolen-RT threat
    model demands it.

## D5 — Client refresh trigger: lazy on 401, retry once; no timer

- **Decision**: Refresh is lazy. An authenticated call that returns 401 (mapped to
  `ErrSessionExpired` in `apiclient.mapError`) triggers a single silent `POST /refresh` using the
  stored RT; on success the new access token is written back to the keyring and the **original**
  request is retried exactly once. If `/refresh` fails (RT expired/revoked/unknown), the session is
  cleared and the caller surfaces the existing "please sign in" path. No proactive/scheduled timer.
- **Rationale**: Lazy keeps the common path zero-cost (no background work, no clock management) and
  puts the single retry at the one chokepoint every authenticated call already flows through
  (`apiclient.do`). Retrying *once* avoids loops if the new token is somehow also rejected.
- **Alternatives considered**:
  - *Proactive timer that refreshes before expiry* — rejected: needs a background goroutine and
    expiry bookkeeping in the client; lazy achieves the same UX with less moving state.
  - *Refresh inside `LoadSession` only* — rejected: would still drop the session mid-run when the
    2h token expires while the app is open; the `do()`-level retry covers both startup and runtime.

## D6 — `/refresh` and `/logout` authenticate via the RT, not the access JWT

- **Decision**: Both new endpoints are **public** routes (no `AuthRequired` middleware) and
  authenticate using the RT carried in the request body. `/refresh` → `{token}` (200) or 401;
  `/logout` → 204 always (idempotent revoke).
- **Rationale**: At refresh time the access JWT is, by definition, expired — so it cannot gate the
  refresh. The RT is the credential. Logout is idempotent (unknown/already-revoked still 204) so a
  client can always "log out" without first proving a live session — important for slice 025's
  hardened logout.
- **Alternatives considered**:
  - *Gate `/refresh` behind the (expired) access JWT* — rejected: impossible by construction.
  - *`/logout` returns 404 for unknown RT* — rejected: leaks token validity and complicates the
    025 logout flow; idempotent 204 is simpler and safe.

## D7 — `register` unchanged; RT obtained via the post-register `/login`

- **Decision**: `POST /register` still returns only `{username, is_admin}` (see
  `auth_handlers.go:register`). The client logs in afterward (already does, `onboard.Authenticate`)
  and obtains the RT from `/login`.
- **Rationale**: Keeps register's responsibility narrow (account creation, not session issuance);
  the onboarding flow already calls `Login` right after `Register`, so the RT lands without a new
  code path.
- **Alternatives considered**:
  - *Have register issue a session + RT* — rejected: widens register's contract for no gain since
    login runs immediately after anyway.

## D8 — Revocation surface this slice

- **Decision**: Provide per-RT revocation via `/logout`, and whole-user revocation via the
  existing user-delete cascade (`refresh_tokens.user_id … ON DELETE CASCADE`, mirroring
  `nodes`/`zones`). No admin "log everyone out" endpoint.
- **Rationale**: These two cover the real needs (a device logs itself out; an operator removes a
  user). The FK cascade reuses the proven pattern in `users.DeleteCascade`.
- **Alternatives considered**:
  - *Admin global force-logout* — deferred: deleting a user already invalidates that user's RTs;
    a fleet-wide logout has no current driver.

## D9 — DESIGN.md amendments (Constitution: DESIGN.md authority, same PR)

- **§4.1** data-model table: add a `refresh_tokens` row
  (`id, user_id, token_hash UNIQUE, expires_at, revoked_at, created_at`).
- **§7.3** "会话：JWT 无状态": split into stateless short access JWT (2h) **plus** a stateful,
  revocable RT (30-day sliding, server stores hash). Correct the line "重启服务 = 换签名密钥 =
  全用户被踢": after this change a restart only invalidates access tokens; RTs validate against the
  DB and survive, so a restart no longer logs everyone out.
- **§8.1** API table: `/login` response becomes `{token, refresh_token}`; add `POST /api/v1/refresh`
  (`{refresh_token} → {token}`) and `POST /api/v1/logout` (`{refresh_token} → 204`).
- **§11** risk register: update the "JWT 不可吊销" row to note RTs are revocable but access tokens
  remain un-revocable for ≤2h; add "重启不再全员吊销（RT 经 DB 存活），全员吊销需删用户或清表";
  add "客户端本地多存一个长期 RT（DPAPI 保护）".

## Open questions

None. All spec items have reasonable, recorded defaults; the spec carries no `NEEDS CLARIFICATION`.
