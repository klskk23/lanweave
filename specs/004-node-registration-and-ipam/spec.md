# Feature Specification: Node Registration and IPAM

**Feature Branch**: `004-node-registration-and-ipam`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "node-registration-and-ipam"

Scope drawn from ROADMAP.md feature 004 and DESIGN.md §3.2, §3.3, §3.5, §4.1, §6.4,
§8, §9.3: an authenticated user registers named nodes by uploading a WireGuard
public key; the server allocates the lowest free pool address (recycling freed
ones), registers the node as a tunnel peer, and returns the connection details a
client needs to build its tunnel. Users can list and delete their own nodes.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 注册节点并取得隧道配置 (Priority: P1)

A logged-in user generates a WireGuard key pair on their device and registers a
node by submitting a node name and the public key. The server assigns the node an
address from the pool, registers it as a tunnel peer so the relay will route to
it, and returns the node's address plus the server's connection details (server
public key, tunnel endpoint, pool network, MTU). With that, the client can stand
up its tunnel to the relay.

**Why this priority**: This is the join point of the whole product — until a user
can register a node and receive an address + server details, no client can connect.
It is independently testable with only an authenticated user (features 001–003).

**Independent Test**: As a logged-in user, submit a node name + a valid public key
→ receive the node's id and assigned address. Fetch server connection info →
receive the server's public key, endpoint, network, and MTU. Inspect the relay's
tunnel → a peer exists with the submitted public key and the node's address.

**Acceptance Scenarios**:

1. **Given** an authenticated user and a host with no nodes yet, **When** they register a node with a valid name and public key, **Then** the node is created with the first available client address and a success response returns its id, name, and address.
2. **Given** a registered node, **When** the relay's tunnel is inspected, **Then** a peer exists whose public key matches the submitted key and whose allowed address is exactly the node's assigned address.
3. **Given** an authenticated user, **When** they request server connection info, **Then** they receive the server's public key, the tunnel endpoint (host:port), the pool network, and the MTU.
4. **Given** two nodes registered in sequence, **When** the second is created, **Then** it receives the next address after the first (addresses are assigned ascending from the start of the client range).
5. **Given** an unauthenticated request, **When** it targets any node or server-info operation, **Then** it is refused with 401.

---

### User Story 2 — 查看自己的节点 (Priority: P1)

A user lists the nodes they have registered to see each node's name, address, and
when it was created, so they can manage them and recognize them across devices.

**Why this priority**: Users must be able to see what they have registered to manage
it (and to delete stale entries that hold addresses). Independently testable right
after US1.

**Independent Test**: Register two nodes, then list nodes → both appear with their
names and addresses; a different user's list does not include them.

**Acceptance Scenarios**:

1. **Given** a user with registered nodes, **When** they list their nodes, **Then** they receive each node's id, name, assigned address, and creation time.
2. **Given** two different users, **When** each lists their nodes, **Then** each sees only their own nodes and never another user's.
3. **Given** a user with no nodes, **When** they list, **Then** they receive an empty list (not an error).

---

### User Story 3 — 删除自己的节点（释放地址与隧道） (Priority: P1)

A user deletes a node they no longer use. Its address is released back to the pool
for reuse and its tunnel peer is removed so the relay no longer routes to it.

**Why this priority**: Without deletion, addresses leak and stale peers accumulate.
Releasing the address is what makes the recycling behavior (US4) observable.
Independently testable after US1.

**Independent Test**: Register a node, delete it → it disappears from the user's
list, the relay's peer for it is gone, and its address becomes available again.

**Acceptance Scenarios**:

1. **Given** a user's own node, **When** they delete it, **Then** the node is removed, its tunnel peer is removed, and its address is freed.
2. **Given** a node owned by another user, **When** a user tries to delete it, **Then** the request is refused and the node is unaffected (the existence of others' nodes is not revealed).
3. **Given** a node id that does not exist, **When** a delete is attempted, **Then** the response indicates not found and nothing changes.

---

### User Story 4 — 地址分配的正确性（回收、最低空闲、并发、上限） (Priority: P2)

Address allocation is correct under reuse and contention: the relay always hands
out the lowest free address, immediately reclaims a deleted node's address for the
next registration, never assigns the same address twice even under simultaneous
registrations, and fails clearly when the pool is exhausted. Registered nodes also
survive a relay restart — their peers are restored from the database so connected
clients do not have to re-register.

**Why this priority**: These correctness guarantees protect the integrity of the
address space and the data plane. They harden US1/US3 and are independently
testable via reuse, concurrency, and restart scenarios.

**Independent Test**: Register several nodes (addresses ascend), delete a middle
one, register again (the freed address is reused). Fire many simultaneous
registrations (all get distinct addresses). Exhaust a tiny pool (clear error).
Restart the relay (all peers reappear from the database).

**Acceptance Scenarios**:

1. **Given** nodes occupying the first few client addresses, **When** a middle node is deleted and a new node is then registered, **Then** the new node reuses the freed (lowest available) address.
2. **Given** many users registering at the same instant, **When** the registrations complete, **Then** every node has a distinct address with no collisions and no gaps are skipped beyond what is occupied.
3. **Given** a pool with only one free client address, **When** a second registration is attempted after it is taken, **Then** it is refused with a clear "no addresses available" error and no node is created.
4. **Given** registered nodes and a relay restart, **When** the relay comes back up, **Then** every node's tunnel peer is restored from the database (same public key and address), so existing clients reconnect without re-registering.

---

### Edge Cases

- **Duplicate node name for the same user**: refused with a conflict; the name must be unique per user (a different user may reuse the name).
- **Duplicate public key (any user)**: refused with a conflict; a public key identifies exactly one node across the whole relay.
- **Malformed/invalid public key**: refused with a validation error; no address is allocated.
- **Empty or oversized node name**: refused with a validation error.
- **Deleting a node mid-flight / double delete**: the second delete reports not found; no error cascade.
- **Registration when the tunnel peer cannot be added** (data-plane failure): the registration fails and no node/address is persisted (database and tunnel stay consistent), so the user can retry.
- **Pool exhaustion**: a clear, specific error distinct from a generic failure.
- **Crash between persisting a node and adding its peer**: self-heals on restart, when peers are rebuilt from the database (the database is the source of truth).
- **A user with many nodes across devices**: listing remains correct and scoped to that user only.

---

## Requirements *(mandatory)*

### Functional Requirements

**Node registration**

- **FR-001**: An authenticated user MUST be able to register a node by submitting a node name and a WireGuard public key.
- **FR-002**: On registration the system MUST allocate the node the lowest free address from the configured pool, excluding the network base address and the server's own address.
- **FR-003**: On registration the system MUST register the node as a tunnel peer of the relay, using the submitted public key and the node's assigned address as its single allowed address, so the relay forwards that address to that peer.
- **FR-004**: Registration MUST be atomic with respect to the database and the tunnel: if the peer cannot be added, no node row and no address allocation persist (the user receives an error and may retry).
- **FR-005**: The system MUST validate the node name (non-empty, within the length limit) and the public key (valid WireGuard public key format), rejecting violations with a clear validation error and allocating no address.
- **FR-006**: A node name MUST be unique per user (the same name may exist for different users); a duplicate for the same user is refused with a conflict.
- **FR-007**: A public key MUST be unique across the entire relay; a duplicate is refused with a conflict and no address is allocated.
- **FR-008**: A successful registration MUST return the node's id, name, and assigned address.

**Server connection info**

- **FR-009**: An authenticated user MUST be able to retrieve the relay's connection details: the server public key, the tunnel endpoint (publicly reachable host and port), the pool network, and the MTU.

**Listing & deletion**

- **FR-010**: An authenticated user MUST be able to list their own nodes, each with id, name, assigned address, and creation time.
- **FR-011**: A user MUST only see their own nodes; another user's nodes MUST never appear in their list.
- **FR-012**: An authenticated user MUST be able to delete a node they own; deletion MUST release the address back to the pool and remove the node's tunnel peer.
- **FR-013**: Attempting to delete a node the user does not own, or one that does not exist, MUST be refused as not found, changing nothing and not revealing other users' nodes.

**Address management (IPAM)**

- **FR-014**: Allocation MUST always choose the lowest-numbered free client address in the pool.
- **FR-015**: A deleted node's address MUST be immediately available for reuse by the next allocation.
- **FR-016**: Allocation MUST be concurrency-safe: simultaneous registrations MUST never receive the same address; each node's address is unique.
- **FR-017**: When the pool has no free client address, registration MUST be refused with a clear "no addresses available" error and create no node.

**Consistency & lifecycle**

- **FR-018**: The database MUST be the source of truth for nodes; at startup the relay MUST rebuild all node tunnel peers from the database so registered nodes survive a restart.
- **FR-019**: The server MUST NOT handle any client private key (clients keep their private keys); public keys and assigned addresses are non-secret operational data.
- **FR-020**: All node and server-info operations MUST require authentication and MUST use the shared error envelope and global rate limiting established in earlier features.

### Key Entities

- **Node**: A registered client endpoint. Attributes: id, owning user, name (unique per user), WireGuard public key (unique relay-wide), assigned pool address (unique), creation time. Belongs to exactly one user.
- **Address pool**: The configured client address range. The server holds the first usable address (feature 003); nodes occupy subsequent addresses. Allocation is lowest-free with reuse.
- **Tunnel peer**: The relay-side representation of a node on the WireGuard interface: the node's public key plus its single allowed address. Derived from the node record; rebuilt from the database at startup.
- **Server connection info**: The relay's public key, tunnel endpoint, pool network, and MTU — everything a client needs to configure its side of the tunnel.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A logged-in user can go from "register node" to holding everything needed for a tunnel (assigned address + server public key + endpoint + network) in a single round-trip.
- **SC-002**: Registering a node makes the relay forward to it: a peer with the submitted public key and the node's address exists, verified 100% of the time.
- **SC-003**: Addresses are assigned ascending: across 5 sequential registrations the addresses are consecutive starting from the first client address.
- **SC-004**: After deleting a node, the very next registration reuses the freed (lowest available) address, verified deterministically.
- **SC-005**: Across 50 concurrent registrations, all 50 nodes receive distinct addresses with zero collisions.
- **SC-006**: Deleting a node removes its tunnel peer and frees its address 100% of the time.
- **SC-007**: After a relay restart, 100% of previously registered nodes have their tunnel peers restored from the database (same key and address) with no client re-registration.
- **SC-008**: Exhausting the pool produces a clear, specific "no addresses available" error in 100% of attempts, with no node created and no crash.
- **SC-009**: Duplicate node names (same user) and duplicate public keys (any user) are rejected 100% of the time, with no address allocated.

---

## Assumptions

- Builds on features 001–003: authenticated users and the JWT middleware (002), the
  running WireGuard interface and the nftables skeleton (003), the config (pool
  network, listen port, MTU), structured logging, the shared error envelope, and the
  global rate limiter all already exist and are reused.
- Clients generate their own WireGuard key pairs and upload only the public key
  (DESIGN §3.5); the server never sees or stores a client private key.
- The pool's first usable address is the server's (feature 003); client addresses
  start at the next address and ascend.
- The relay's publicly reachable tunnel endpoint (the host clients dial over UDP) is
  provided by operator configuration; it may differ from the API address, so it is a
  configured value returned by the server-info operation.
- Each tunnel peer's allowed address is the node's single address (a /32), matching
  the hub-and-spoke model where the relay routes each client address to its peer.
- This feature does NOT implement zone membership or inter-node reachability rules
  (feature 005): a freshly registered node can reach the relay but not other nodes
  until a zone admits it (the default-deny forward chain from feature 003 still
  blocks node-to-node traffic).
- This feature does NOT implement full cascade deletion of a user's nodes when the
  user is deleted (feature 008); it implements only user-initiated deletion of one's
  own nodes.
- Online/last-handshake status is out of scope (feature 007).
- Node names follow the same length limit as usernames (≤ 64 characters) for
  consistency; addresses are presented in standard dotted form.
