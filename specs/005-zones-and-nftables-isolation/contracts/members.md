# Contract: Zone Members Endpoint

## `GET /api/v1/zones/{name}/members`

List a zone's members. Visible only to participants (owner or a user with a member
node); within a zone, every member node is fully visible (name, address, owner).
Requires `Authorization: Bearer <jwt>`.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `protocol.ZoneMembersResponse` (below) |
| `401`  | `unauthorized` | No/invalid token |
| `404`  | `not_found` | No such zone, OR the caller is not a participant (no disclosure, FR-016) |

**`ZoneMembersResponse`**:
```json
{
  "members": [
    { "node_name": "laptop", "ip": "100.127.0.2", "owner": "alice" },
    { "node_name": "phone",  "ip": "100.127.0.5", "owner": "bob" }
  ]
}
```

| Field | Source | Notes |
|-------|--------|-------|
| `node_name` | member node | |
| `ip` | member node's address | dotted form |
| `owner` | the node's owning username | cross-user transparency within the zone (FR-015) |

**Acceptance**: US4-2/3, SC-007.
