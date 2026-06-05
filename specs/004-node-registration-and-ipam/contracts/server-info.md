# Contract: Server Info Endpoint

## `GET /api/v1/server`

Returns everything a client needs to configure its side of the tunnel. Requires
`Authorization: Bearer <jwt>`.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `protocol.ServerInfoResponse` (below) |
| `401`  | `unauthorized` | No/invalid token |

**`ServerInfoResponse`**:
```json
{
  "public_key": "<server WireGuard public key, base64>",
  "endpoint":   "vpn.example.com:51820",
  "network":    "100.127.0.0/16",
  "mtu":        1420
}
```

| Field | Source | Notes |
|-------|--------|-------|
| `public_key` | server WG key (feature 003) | Stable across restarts. Public, not a secret. |
| `endpoint` | `wireguard.endpoint` config | The UDP host:port the client dials; may differ from the API address (NAT). |
| `network` | `wireguard.network` config | The pool CIDR; clients route this range into the tunnel. |
| `mtu` | `wireguard.mtu` config | Tunnel MTU. |

**Acceptance**: US1-3, SC-001. The values are non-secret operational data; the server
private key is never exposed (FR-019).
