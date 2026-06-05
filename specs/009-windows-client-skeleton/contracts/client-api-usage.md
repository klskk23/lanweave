# Contract: Client → Server API usage

**Feature**: 009-windows-client-skeleton | **Date**: 2026-06-05

This feature adds **no server endpoints**. It is a consumer of existing endpoints
(features 002 and 004). This contract pins which calls the onboarding flow makes, the
DTOs it reuses from `pkg/protocol`, and how responses map to client behavior.

## Calls made during onboarding (in order)

### 1. Authenticate — one of:

- `POST /api/v1/register` — body `protocol.RegisterRequest{invite_code, username, password}`
  → `201` `protocol.RegisterResponse`. Then sign in to obtain a token (or the client
  follows registration with a login call).
  - invalid/used invite or duplicate username → mapped to `ErrInviteInvalid` /
    `ErrUsernameTaken`.
- `POST /api/v1/login` — body `protocol.LoginRequest{username, password}` →
  `200` `protocol.LoginResponse{token}`.
  - `401` → `ErrAuthFailed` (no field-level hint).

The token is sent as `Authorization: Bearer <token>` on the calls below.

### 2. Register this device

- `POST /api/v1/nodes` — body `protocol.RegisterNodeRequest{name, wg_pubkey}` →
  `201` `protocol.NodeResponse{id, name, ip, ...}` (the client uses `name` and `ip`).
  - `409 node_name_taken` → `ErrNodeNameTaken` (user picks another name).
  - `409 pubkey_taken` → idempotent recovery (our own prior attempt): call step 2a.
  - `503 pool_exhausted` → a clear "no addresses available" message.

### 2a. Recover the assigned address after `pubkey_taken` (idempotent retry only)

- `GET /api/v1/nodes` → `protocol.NodeListResponse{nodes[]}`; find the entry whose `name`
  equals the chosen device name; use its `ip`. (The public key is intentionally not
  returned by the API, so the name is the match key within the authenticated user's own
  devices.)

### 3. Fetch server connection details

- `GET /api/v1/server` → `protocol.ServerInfoResponse{public_key, endpoint, network, mtu}`.
  The client stores `public_key`, `endpoint`, `network` in the state record (the tunnel,
  feature 010, will consume them).

## Reused DTOs (`pkg/protocol`, unchanged)

`LoginRequest`, `LoginResponse`, `RegisterRequest`, `RegisterResponse`,
`RegisterNodeRequest`, `NodeResponse`, `NodeListResponse`, `ServerInfoResponse`,
`ErrorResponse`.

## Transport rules

- HTTPS only; certificate verified against the system trust store by default.
- The `--insecure` CLI flag (never in the UI) sets skip-verify for troubleshooting.
- The client may be constructed with an optional trust root (a specific CA/cert pool) so
  the verify-on path can be exercised against a test server's self-signed certificate
  **without** disabling verification; this is mutually exclusive with `--insecure` and is
  used by the integration test, not the shipped UI.
- Transport failure → `ErrUnreachable`; TLS verification failure → `ErrUntrustedCert`;
  unexpected 5xx → `ErrServer`. All surface as plain-language messages (FR-012).

## Out of scope (later features)

- No tunnel calls (no WireGuard bring-up) — feature 010.
- No zone/management calls — feature 011.
