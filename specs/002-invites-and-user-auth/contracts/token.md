# Contract: Session Token (JWT)

The credential issued by `/login` and required by protected endpoints. Stateless,
signed, not stored server-side.

## Format

- JWS compact JWT, algorithm **HS256** only.
- Signed with `cfg.Auth.JWTSecret` (≥ 32 bytes, validated in feature 001).

## Claims

| Claim      | Type   | Meaning |
|------------|--------|---------|
| `sub`      | string | Account id. |
| `username` | string | Account username. |
| `is_admin` | bool   | Whether the account is an administrator. |
| `iat`      | number | Issued-at (unix seconds). |
| `exp`      | number | Expiry = `iat + jwt_ttl` (default 2h). |

## Issuance

- On successful `/login` only. Registration does NOT issue a token.

## Verification rules (enforced by `AuthRequired`)

A token is accepted only if ALL hold; otherwise the request gets `401 unauthorized`:

1. Present as `Authorization: Bearer <token>`.
2. Parses as a compact JWT.
3. `alg` is exactly `HS256` (pinned via `WithValidMethods`) — `none`/`RS256`/etc. rejected.
4. Signature verifies against `jwt_secret`.
5. `exp` is in the future (no grace window; small skew that has already passed = expired).

## Invalidation

- Expiry (`exp`) is the only invalidation path (FR-004). No refresh, no revocation list.
- Rotating `jwt_secret` (operator action, requires restart) invalidates ALL
  outstanding tokens — because none will verify against the new secret (US4-4).
- A normal restart with an unchanged `jwt_secret` keeps tokens valid (reconciles
  DESIGN §7.3 with feature 001's persisted secret).

## Security

- The token MUST NOT appear in any log line (FR-020).
- `is_admin` is trusted from the verified token for the token's lifetime; a privilege
  change mid-lifetime takes effect only after expiry (accepted trade-off, research.md R4).
