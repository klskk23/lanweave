# Feature Specification: WireGuard Server Interface

**Feature Branch**: `003-wireguard-server-interface`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "wireguard-server-interface"

Scope drawn from ROADMAP.md feature 003 and DESIGN.md §3.1, §3.2, §3.5, §6, §10.2:
at startup the relay brings up its data-plane WireGuard interface with a stable
identity, occupies the first pool address, enables packet forwarding, and
initializes the nftables isolation table with an empty default-deny forward
chain. No client peers and no zone rules are created here (those arrive with
features 004 and 005).

---

## User Scenarios & Testing *(mandatory)*

> The actor for this feature is the **operator** running the relay server. There
> is no end-user (Windows client) surface and no new HTTP API; value is delivered
> as observable, correct server-side data-plane state.

### User Story 1 — 中转服务器拥有稳定身份的隧道接口 (Priority: P1)

The operator starts the relay. A WireGuard interface comes up with the configured
name, listening on the configured port, holding the first address of the pool, and
identified by a long-lived key pair whose public half is stable across restarts.
On first start the server's private key is generated and stored with owner-only
permissions; on every later start the existing key is loaded, never regenerated.

**Why this priority**: Every client tunnel will be configured against this server's
public key and endpoint. If that identity is missing or changes between restarts,
every client breaks. This is the bedrock of the data plane and is independently
observable the moment the server starts.

**Independent Test**: On a privileged host, start the server → inspect the system:
the interface exists with the configured name, carries the first pool address, is
listening on the configured port, and reports a public key. Confirm the private
key file exists and is readable only by its owner. Restart → the reported public
key is identical to before.

**Acceptance Scenarios**:

1. **Given** a host with no prior server key, **When** the relay starts, **Then** a key pair is generated, the private key is persisted with owner-only access, and the interface is created with the configured name, port, and the first pool address.
2. **Given** a host where the server key already exists, **When** the relay starts, **Then** the existing key is loaded (not regenerated) and the interface's public key matches the prior run.
3. **Given** the running relay, **When** the operator inspects the tunnel, **Then** the interface has the server identity and **zero** client peers (peers are added by a later feature).
4. **Given** the configured pool, **When** the interface is addressed, **Then** the server holds the first usable address of that pool (e.g., the `.1` of the range) and can route the whole pool range.

---

### User Story 2 — 转发与隔离骨架就位（默认拒绝） (Priority: P1)

With the interface up, the relay enables IP forwarding so it can relay traffic
between clients, and installs its dedicated network-isolation table containing a
single forwarding chain whose default policy is to **drop**. The table starts
empty of any allow rules, so no client could reach another even if peers existed —
isolation is closed by default until zones open specific paths in a later feature.

**Why this priority**: The relay's entire purpose is mediated, isolated forwarding.
The forwarding capability and the default-deny posture must exist before any client
or zone is added, so the system is never briefly "open" during bring-up. Independently
testable by inspecting host forwarding state and the isolation table immediately after start.

**Independent Test**: On a privileged host, start the server → confirm host IPv4
forwarding is enabled and the dedicated isolation table exists with a forward chain
whose default policy is drop and which contains no allow rules. Restart → the table
returns to the same clean empty state.

**Acceptance Scenarios**:

1. **Given** the relay has started, **When** host forwarding configuration is inspected, **Then** IPv4 forwarding is enabled.
2. **Given** the relay has started, **When** the isolation table is inspected, **Then** the dedicated table exists with a forward chain whose default policy is drop and which contains no allow rules and no client groups.
3. **Given** a previous run left a partial or stale isolation table, **When** the relay restarts, **Then** the table is rebuilt to the clean empty default-deny state (the database remains the single source of truth).

---

### User Story 3 — 重启幂等且失败安全 (Priority: P2)

Restarting the relay is safe and idempotent: an already-present interface is
adopted and reconciled rather than torn down (so existing client handshakes are
not dropped by a routine restart), and unsafe conditions fail loudly instead of
silently degrading. In particular, a present-but-unreadable/corrupt server key
aborts startup rather than minting a new identity that would orphan every client.

**Why this priority**: Operators restart services routinely. Idempotency and safe
failure prevent a restart from silently breaking the fleet or weakening security.
It hardens US1/US2 and can be validated independently by re-running and by fault injection.

**Independent Test**: Start, then restart with unchanged config → the interface is
not torn down (same identity/index). Separately, corrupt the key file and start →
startup fails clearly and no new key is written. Start unprivileged → startup fails
with a clear privilege error.

**Acceptance Scenarios**:

1. **Given** the interface already exists from a prior run, **When** the relay restarts, **Then** it adopts and reconciles the existing interface (correct key, address, port) without removing and recreating it.
2. **Given** a server key file that exists but is corrupt or unreadable, **When** the relay starts, **Then** startup fails with a clear error and **no** new key is generated.
3. **Given** the process lacks the privilege required to manage network interfaces or the isolation table, **When** the relay starts, **Then** startup fails with a clear, actionable error and the service does not enter a half-configured serving state.
4. **Given** the configured listening port or interface name is already occupied by an incompatible device/process, **When** the relay starts, **Then** startup fails with a clear error identifying the conflict.

---

### Edge Cases

- **Missing kernel WireGuard support**: if the host cannot provide a kernel WireGuard interface, startup fails with a clear error (userspace fallback is out of scope for v1).
- **Pool too small to host a server address** (e.g., a single-address range): startup fails with a configuration error.
- **Key file permissions too broad** (group/world readable): the relay tightens them to owner-only and logs a warning, or fails if it cannot.
- **IP forwarding already enabled by the host**: enabling is idempotent and not treated as an error; the relay does not disable forwarding on shutdown (avoids disrupting other host services).
- **Isolation table left behind after an unclean shutdown**: rebuilt to the clean state on next start.
- **Interface exists but mis-addressed or on the wrong port** (drift): reconciled to match configuration at startup.
- **Concurrent second instance on the same host**: out of scope — the architecture assumes a single relay instance per host (DESIGN §11); behavior is undefined and the operator is responsible for not running two.

---

## Requirements *(mandatory)*

### Functional Requirements

**Server identity & key material**

- **FR-001**: On first start (no existing key), the relay MUST generate a WireGuard key pair and persist the private key to the configured data directory.
- **FR-002**: The persisted private key file MUST be readable and writable only by its owner (owner-only permissions); broader permissions MUST be tightened or cause a clear failure.
- **FR-003**: On every subsequent start, the relay MUST load the existing private key and MUST NOT regenerate it, so the server's public key is stable across restarts.
- **FR-004**: If a key file is present but cannot be read or parsed, the relay MUST fail startup with a clear error and MUST NOT generate a replacement key.

**Tunnel interface**

- **FR-005**: At startup the relay MUST ensure a WireGuard interface exists with the configured name, configured listening port, and the server's key.
- **FR-006**: The relay MUST assign the interface the first usable address of the configured pool and configure it to route the entire pool range.
- **FR-007**: The interface MUST start with zero client peers in this feature (client peers are introduced by a later feature).
- **FR-008**: If the interface already exists, the relay MUST adopt and reconcile it to the configured name/port/key/address rather than tearing it down and recreating it.

**Forwarding & isolation skeleton**

- **FR-009**: At startup the relay MUST enable IPv4 packet forwarding on the host.
- **FR-010**: The relay MUST NOT disable host IP forwarding on shutdown.
- **FR-011**: At startup the relay MUST (re)create its dedicated network-isolation table containing a single forward chain whose default policy is drop.
- **FR-012**: The isolation table MUST start with no allow rules and no client groups (default-deny; openings are added by a later feature).
- **FR-013**: Rebuilding the isolation table at startup MUST be idempotent and MUST bring it to the clean default-deny state regardless of any pre-existing/stale contents, treating the database as the single source of truth.

**Lifecycle & safety**

- **FR-014**: The relay MUST treat interface and isolation-table setup as part of startup: a failure in any of them MUST prevent the service from entering its normal serving state and MUST surface a clear, actionable error.
- **FR-015**: The relay MUST require the elevated privilege needed to manage network interfaces and the isolation table; if it is missing, startup MUST fail clearly.
- **FR-016**: Restarting the relay with unchanged configuration MUST NOT drop the live tunnel interface, so existing client sessions survive a routine restart.
- **FR-017**: Setup actions and their outcomes (key generated vs loaded, interface created vs adopted, forwarding enabled, isolation table rebuilt) MUST be recorded in the structured logs, and the private key value MUST NOT appear in any log line.

### Key Entities

- **Server tunnel identity**: The relay's long-lived WireGuard key pair. The private key is persisted (owner-only) and stable; the public key is derived from it and is what clients will be configured against.
- **Tunnel interface**: The relay's WireGuard network interface — its name, listening port, assigned server address, and (initially empty) set of peers.
- **Isolation table**: The relay's dedicated firewall table holding a single default-deny forward chain; initially empty of allow rules and client groups.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Across 10 consecutive restarts with unchanged configuration, the server's tunnel public key is identical every time (stable identity, 100%).
- **SC-002**: After a cold start on a prepared host, the tunnel interface is up, addressed with the first pool address, and listening on the configured port within 3 seconds.
- **SC-003**: The persisted private key file is owner-only (not group/world readable) in 100% of starts.
- **SC-004**: Immediately after start, with no client yet onboarded, no traffic could be forwarded between pool addresses — the forward chain default policy is drop and contains zero allow rules (verified 100% of starts).
- **SC-005**: A corrupt or unreadable existing key file results in a clear startup failure and zero key regeneration in 100% of fault-injection runs.
- **SC-006**: A routine restart preserves the live interface (its identity/index is not torn down), so an already-connected client's tunnel is not reset by the restart.
- **SC-007**: Inspecting the logs across a start cycle reveals each setup decision (key generated/loaded, interface created/adopted, forwarding enabled, table rebuilt) and contains zero occurrences of the private key value.

---

## Assumptions

- Builds on feature 001: the TOML config already supplies and validates the
  WireGuard settings (`network` CIDR, `listen_port`, `interface` name, `mtu`) and
  the nftables `table` name, and provides the data directory and structured logging.
- Target platform is Linux with kernel WireGuard support and the nftables subsystem
  available (confirmed appropriate for the project's Debian/Ubuntu target, DESIGN §10).
- The relay runs with the elevated privilege required to manage network interfaces
  and firewall tables (root with `CAP_NET_ADMIN`, per constitution Security and
  DESIGN §10.4); unprivileged operation is not supported and is expected to fail clearly.
- The server's private key lives at the configured data directory (e.g.
  `/var/lib/lanweave/wg_private`, DESIGN §10.2) with owner-only permissions; the
  operator is responsible for protecting the data directory.
- "First usable address" means the first host address of the pool range (the `.1`
  for a typical `/16`), reserving the network base address.
- The interface and isolation table are intended to persist across routine restarts;
  shutdown does not tear them down (avoids dropping the data plane), while startup
  always reconciles them to the configured/clean state.
- This feature creates NO client peers (feature 004), NO zone groups or allow rules
  (feature 005), and exposes NO new HTTP endpoint; the server's public key is made
  available for a later feature's `GET /server` response but is not surfaced here.
- Because creating a kernel WireGuard interface and a firewall table requires
  elevated privilege, the behaviors that touch the kernel are validated on a
  privileged runner; environments lacking that privilege cannot exercise them and
  must skip those checks with a clear signal (the pure logic — key persistence,
  permission enforcement, address derivation — is validated without privilege).
