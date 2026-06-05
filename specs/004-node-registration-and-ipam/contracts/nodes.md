# Contract: Node Endpoints

All require `Authorization: Bearer <jwt>` (any authenticated user). All use the
shared JSON error envelope and the global rate limiter.

---

## `POST /api/v1/nodes`

Register a node and allocate it an address.

**Request** (`protocol.RegisterNodeRequest`):
```json
{ "name": "laptop", "wg_pubkey": "<base64 WireGuard public key>" }
```

**Behavior**: validate name + key → allocate lowest free pool address (retry on ip
race) → INSERT node → add WireGuard peer (pubkey + ip/32). If the peer cannot be
added, the node is removed and an error returned (FR-004).

**Responses**:
| Status | Code | When |
|--------|------|------|
| `201`  | —    | Body: `{ "id": 1, "name": "laptop", "ip": "100.127.0.2" }` |
| `400`  | `validation_error` | Empty/oversized name, malformed body, or invalid public key |
| `401`  | `unauthorized` | No/invalid token |
| `409`  | `node_name_taken` | Caller already has a node with this name |
| `409`  | `pubkey_taken` | This public key is already registered (any user) |
| `503`  | `pool_exhausted` | No free address in the pool; no node created |

**Acceptance**: US1-1/2/4, US4-2/3, SC-002/003/005/008/009.

---

## `GET /api/v1/nodes`

List the caller's own nodes, newest first.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `200`  | —    | Body: `{ "nodes": [ { "id", "name", "ip", "created_at" }, ... ] }` (empty list if none) |
| `401`  | `unauthorized` | No/invalid token |

Only the caller's nodes appear (FR-011). **Acceptance**: US2-1/2/3.

---

## `DELETE /api/v1/nodes/{id}`

Delete a node the caller owns; frees its address and removes its peer.

**Responses**:
| Status | Code | When |
|--------|------|------|
| `204`  | —    | Deleted; address freed, peer removed |
| `401`  | `unauthorized` | No/invalid token |
| `404`  | `not_found` | No such node OR not owned by caller (no enumeration, FR-013) |

**Acceptance**: US3-1/2/3, SC-004/006.
