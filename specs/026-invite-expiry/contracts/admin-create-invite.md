# Contract: Admin — Create Invite

**Endpoint**: `POST /api/v1/admin/invites` (admin-only, unchanged auth)

## Request

Unchanged. No per-code TTL parameter (FR: global config only). Empty body / existing
shape preserved.

## Response (changed — additive field)

`CreateInviteResponse` gains an optional `expires_at`:

```jsonc
{
  "code": "<base64url invite code>",
  "expires_at": "2026-06-08T07:00:00Z"   // RFC3339 UTC; OMITTED when the code never expires
}
```

- `expires_at` present → the moment the code stops being redeemable
  (`created_at + invite_ttl`).
- `expires_at` absent/omitted → the code never expires (`invite_ttl` is `0`/empty).

Protocol struct (`pkg/protocol/auth.go`):

```go
type CreateInviteResponse struct {
    Code      string  `json:"code"`
    ExpiresAt *string `json:"expires_at,omitempty"` // RFC3339; nil = never expires
}
```

## Invite list item (changed — additive, if/where invites are listed internally)

`InviteListItem` gains `expires_at` and a new `"expired"` status value:

```go
type InviteListItem struct {
    Code      string  `json:"code"`
    Status    string  `json:"status"`              // "unused" | "used" | "expired"
    CreatedBy int64   `json:"created_by"`
    CreatedAt string  `json:"created_at"`
    UsedBy    *int64  `json:"used_by,omitempty"`
    UsedAt    *string `json:"used_at,omitempty"`
    ExpiresAt *string `json:"expires_at,omitempty"` // nil = never expires
}
```

`status` derivation in `toInviteListItem`:
- `used_at` set → `"used"` (precedence unchanged).
- else `expires_at` non-NULL and `< now()` → `"expired"`.
- else → `"unused"`.

> Note: this slice adds **no** new list *endpoint* (no `invite list` command). The
> `InviteListItem` shape is updated only so any existing/internal listing stays
> consistent; surfacing it is out of scope per ROADMAP §026.

## Behavior

- The server stamps `expires_at = created_at + invite_ttl` at creation when
  `invite_ttl > 0`, otherwise NULL (FR-001, FR-005).
- The invite code MUST NOT appear in logs (unchanged security rule).
