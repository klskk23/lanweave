# Research: Windows Client Main Panel

**Feature**: 011-windows-client-main-panel | **Date**: 2026-06-06

The management surface consumes existing server operations, so the questions are about
session handling, how to mark "this machine", and keeping the panel testable. Decisions
below resolve them; none remain as NEEDS CLARIFICATION.

## Decision 1 — Fyne-free panel controller, thin Fyne view

- **Decision**: A `panel.Controller` (no Fyne) assembles the view data (devices, zones,
  members) and performs every operation; `internal/client/ui/panel.go` is a thin Fyne view
  bound to it.
- **Rationale**: Same split that made 009/010 testable — the management logic and server
  interactions are unit/integration-tested on the host against a real server, while the GUI
  stays a thin shell validated manually. Keeps each package small (Principle I).
- **Alternatives considered**: logic inside Fyne callbacks — couples correctness to the
  untestable GUI; rejected.

## Decision 2 — Reuse the existing server endpoints + DTOs

- **Decision**: The panel calls the endpoints that already exist (005/006/007): create/list
  zones, join/leave, members, change-password/kick/delete, and `GET /nodes` (online +
  last-seen). The `apiclient` (009) is extended with one method per endpoint, reusing the
  `pkg/protocol` DTOs (`CreateZoneRequest`, `ZoneListResponse`, `ZoneMembersResponse`, …).
- **Rationale**: No server work is needed; the DTOs are already shared. Keeps the client a
  pure consumer.
- **Alternatives considered**: a new aggregate "panel" endpoint server-side — unnecessary
  surface; the existing endpoints suffice. Rejected.

## Decision 3 — Session token cached in the OS secure store (DESIGN §8)

- **Decision**: Cache the session token in the secure store under a new key
  (`keyring.SessionTokenName`). On panel start, load it, build the apiclient with it, and
  validate via `GET /me`. If absent/expired (401), prompt a sign-in (username + password),
  `Login`, and cache the new token. A 401 mid-use re-triggers the prompt.
- **Rationale**: DESIGN §8 mandates storing the access token in the credential manager (not a
  plain file). Reusing a valid token avoids asking for the password every launch (SC-009);
  the short TTL (1–2 h) bounds exposure, and re-auth is a simple prompt.
- **Known interaction**: device setup (009) does not retain a token, so the first panel launch
  after onboarding prompts a sign-in once, then caches. (A future tweak could have onboarding
  cache the token; out of scope here.)
- **Alternatives considered**:
  - *Prompt sign-in every launch*: needless friction within the token lifetime. Rejected.
  - *Store the token in the state file*: violates "no secret in a plain file". Rejected.

## Decision 4 — Marking "this machine"

- **Decision**: The device list comes from `GET /nodes`; the panel marks the entry whose name
  matches the setup record's `node_name` (falling back to the recorded address) as this
  machine.
- **Rationale**: The setup record (009) holds this device's name and address; node names are
  unique per user, so the match is unambiguous. The server intentionally does not return
  public keys, so name/address is the right key.
- **Alternatives considered**: a server "which node am I" flag — extra surface; the local
  record already identifies this machine. Rejected.

## Decision 5 — Owner-control gating + destructive confirmation

- **Decision**: `GET /zones` returns `is_owner` per zone; the controller exposes it and the
  view shows change-password/kick/delete only when `is_owner` is true. Every destructive
  action (leave, kick, delete) goes through a confirmation that names the specific zone or
  member. The server still enforces owner-only (006) as the source of truth.
- **Rationale**: FR-008/FR-009 — defense in depth (hide + server-enforce) and mistake
  prevention. The controller carries the boolean; the view renders the gate + confirmation.
- **Alternatives considered**: rely on server 403 alone (no hiding) — worse UX and invites
  confusing failures. Rejected.

## Decision 6 — Polling refresh for online status

- **Decision**: The panel refreshes the device/zone/online data on a timer (~ the server's
  ≤ 30 s online cadence, feature 007) and after every successful operation.
- **Rationale**: FR-004/FR-014/SC-006 — keep the display honest with the server without a
  push channel (WebSocket is out of scope, v1.1). Refresh-after-action gives immediate
  consistency; the timer keeps online status current.
- **Alternatives considered**: server push — deferred (v1.1). Rejected for now.

## Decision 7 — Testing strategy (real server, no mocking)

- **Decision**:
  - *Unit (non-privileged)*: apiclient zone-method typed-error mapping vs an `httptest`
    server with canned responses; the controller's view assembly + this-machine marking +
    session decisions with a fake API (our own seam).
  - *Integration (privileged, `unshare -rUn`)*: real apiclient + real controller against a
    real `api.NewRouter` (real store + real `wg.Server` + real `netfw`) over
    `httptest.NewTLSServer` (trusting the test cert via `RootCAs`, as in 009) — two users,
    real device registration, then create/join/leave, owner change-password/kick/delete,
    members transparency, and online status, asserted against the server.
  - *Acceptance*: the Fyne panel validated manually on Windows.
- **Rationale**: Honors Principle II — the server (with its real SQLite/WG/nft) is never
  mocked; only our own apiclient/API seams are faked in unit tests. The GUI manual portion is
  the documented exception.

## Resolved unknowns summary

| Topic | Resolution |
|-------|------------|
| UI/logic split | Fyne-free `panel.Controller`; thin Fyne view |
| Endpoints | reuse 005/006/007 + `GET /nodes`; extend apiclient |
| Session | token cached in the secure store; reuse-or-prompt; validate via `GET /me` |
| This machine | match the device list against the setup record's node name/address |
| Owner gating | `is_owner` from `GET /zones`; hide controls + server-enforce; named confirmations |
| Online refresh | timer (~30 s) + refresh-after-action |
| Tests | real server integration (privileged); fakes only for our own seams; GUI manual |
