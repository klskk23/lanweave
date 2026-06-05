# Feature Specification: Node Online Status

**Feature Branch**: `007-node-online-status`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "继续按照ROADMAP.md 实现007" (ROADMAP feature 007: node-online-status)

Scope drawn from ROADMAP.md feature 007 and DESIGN.md §6.5: the server periodically
reads each tunnel peer's most recent handshake time and reports a per-node "online"
flag (online when the last handshake is within 3 minutes), surfaced in the user's
node list so people can see which of their nodes are currently connected.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 用户查看自己节点的在线状态 (Priority: P1)

A user lists their nodes and, for each one, sees whether it is currently online —
plus, when available, how recently it was last seen. A node whose tunnel has
handshaked recently shows online; one that has been idle or disconnected past the
threshold shows offline; one that has never connected shows offline.

**Why this priority**: Knowing which devices are actually connected is the core
value — users need it to trust the network and to spot a node that has dropped.
Independently testable by listing nodes and checking the online flag against the
tunnel's handshake state.

**Independent Test**: With a node whose tunnel is handshaking (a connected client),
list nodes → that node shows online. With a node that has not handshaked within the
threshold, list nodes → it shows offline. A freshly registered, never-connected node
shows offline.

**Acceptance Scenarios**:

1. **Given** a registered node whose client is connected and handshaking, **When** the user lists their nodes, **Then** that node is reported online.
2. **Given** a node whose client has not handshaked within the online threshold, **When** the user lists their nodes, **Then** that node is reported offline.
3. **Given** a freshly registered node that has never connected, **When** the user lists their nodes, **Then** it is reported offline.
4. **Given** any node, **When** it is listed, **Then** its most recent handshake time (or "never") is available so a client can show "last seen".
5. **Given** an unauthenticated request, **When** it targets the node list, **Then** it is refused (status is only ever shown for the caller's own nodes).

---

### User Story 2 — 状态及时且健壮 (Priority: P2)

The reported status tracks reality within a bounded lag and never destabilizes the
server. A node that connects becomes online within the refresh interval; one that
stops connecting becomes offline within the threshold plus one interval; a
reconnecting node returns to online within the interval. The status is derived,
ephemeral data — it is not authoritative and is rebuilt after a restart — and the
periodic tracking degrades gracefully if the tunnel cannot be read.

**Why this priority**: The freshness bound and robustness make the flag trustworthy
and safe; they harden US1. Independently testable via connect/disconnect/reconnect
timing and fault/restart scenarios.

**Independent Test**: Connect a client → online within the interval. Stop it → offline
within threshold + interval. Reconnect → online within the interval. Restart the
server → status repopulates within one interval. Make the tunnel unreadable → the
server keeps serving and reports offline (no crash).

**Acceptance Scenarios**:

1. **Given** a node whose client connects, **When** at most one refresh interval passes, **Then** the node is reported online.
2. **Given** an online node whose client stops connecting, **When** the threshold plus one refresh interval passes, **Then** the node is reported offline.
3. **Given** an offline node whose client reconnects, **When** at most one refresh interval passes, **Then** the node is reported online again.
4. **Given** the server is restarted, **When** at most one refresh interval passes, **Then** each node's online status reflects current tunnel state (no stale "online" carried across the restart).
5. **Given** the tunnel temporarily cannot be read, **When** the status is requested, **Then** the server still responds (nodes default to offline) and does not crash.

---

### Edge Cases

- **Never-connected node**: no handshake on record → offline (not "unknown").
- **Clock behavior**: the online decision compares the handshake time to the current time; minor clock skew does not flip a clearly-online or clearly-offline node.
- **Idle but connected**: a client that keeps its tunnel alive (periodic keepalive) continues to handshake and stays online even with no user traffic; a client without keepalive may appear offline when idle (a documented client requirement).
- **A peer present in the tunnel without a matching node record**: ignored for status.
- **A node whose peer is briefly absent during reconciliation**: reported offline until the peer/handshake reappears.
- **Many nodes**: listing remains fast and does not scan the tunnel once per node per request.
- **Status request right after restart, before the first refresh**: nodes report offline until the first refresh completes (within one interval).

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The server MUST periodically (at least every refresh interval) read each tunnel peer's most recent handshake time.
- **FR-002**: The node list MUST report, per node, an `online` flag that is true exactly when that node's most recent handshake is within the online threshold.
- **FR-003**: A node that has never handshaked MUST be reported offline.
- **FR-004**: The node list MUST also expose each node's most recent handshake time (or an explicit "never"), so a client can display "last seen".
- **FR-005**: Online status MUST be shown only for the caller's own nodes; listing remains authenticated and scoped to the caller (unchanged from feature 004).
- **FR-006**: A connecting node MUST be reported online within one refresh interval; a node that stops connecting MUST be reported offline within the threshold plus one refresh interval; a reconnecting node MUST return to online within one refresh interval.
- **FR-007**: Online status MUST be derived, ephemeral data (not authoritative persisted state); after a restart it MUST reflect current tunnel state within one refresh interval, never carrying a stale "online" across the restart.
- **FR-008**: If the tunnel cannot be read during a refresh, the server MUST continue serving and report affected nodes as offline, without crashing.
- **FR-009**: The periodic tracking MUST start when the server starts and stop cleanly when the server shuts down.
- **FR-010**: Computing online status for the node list MUST NOT require scanning the tunnel once per node per request (the periodic refresh is the data source).

### Key Entities

- **Node online status**: A derived, per-node fact — the most recent tunnel handshake time and whether it falls within the online threshold. Not stored as authoritative data; refreshed periodically from the live tunnel and surfaced in the node list.
- **Online threshold**: The maximum age of the last handshake for a node to count as online (3 minutes).
- **Refresh interval**: How often the server re-reads handshake times (30 seconds), which bounds how stale the reported status can be.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A node whose client connects (and keeps its tunnel alive) is reported online within 30 seconds of connecting.
- **SC-002**: A node whose client stops connecting is reported offline within 3.5 minutes (the 3-minute threshold plus one 30-second refresh).
- **SC-003**: A reconnecting node returns to online within 30 seconds.
- **SC-004**: A never-connected node is reported offline 100% of the time.
- **SC-005**: After a server restart, no node is incorrectly reported online from before the restart; current status is reflected within 30 seconds.
- **SC-006**: Listing nodes with status returns within the same responsiveness budget as listing nodes without status (no per-node tunnel scan); a user with up to 100 nodes sees results quickly.
- **SC-007**: When the tunnel is unreadable, the node list still returns successfully (nodes shown offline) in 100% of attempts, with no server crash.

---

## Assumptions

- Builds on features 003–004: the running WireGuard interface and the registered
  nodes (with their tunnel peers) already exist; this feature only reads handshake
  times and surfaces an online flag. It adds no new node lifecycle behavior.
- The online threshold is 3 minutes and the refresh interval is 30 seconds (DESIGN
  §6.5); these are fixed for v1 (not operator-configurable).
- Clients keep their tunnel alive with a periodic keepalive (≈25 s) so an
  idle-but-connected node keeps handshaking and stays online; this is a client-side
  configuration requirement (delivered with the client, features 009–011) and is out
  of scope for the server here — the server only reports what the tunnel shows.
- Online status is ephemeral and derived from the live tunnel; it is not persisted as
  authoritative data and is repopulated after a restart within one refresh interval.
  (Whether a transient cache is kept is an implementation detail.)
- Status is surfaced in the user's own node list only; this feature does not add
  online status to the zone-members view (feature 005), which remains unchanged.
- Verifying a literal "online" requires a real client actually handshaking through the
  relay; the deterministic, automated checks cover the status computation and the
  reading of real handshake times, while a true connect/disconnect/reconnect timing
  test is a manual scenario (a real client), consistent with how reachability is
  validated in features 003–006.
- IPv4 only; single relay instance.
