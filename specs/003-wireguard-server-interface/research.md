# Phase 0 Research: WireGuard Server Interface

Decisions are constrained by DESIGN.md §3/§6/§10, the constitution, the feature-001
codebase, and a recon of the dev host (kernel `wireguard` + `nf_tables` modules
loaded; `wg`/`wg-quick`/`ip` present; **`nft` CLI absent**; running as uid 1000).
No `NEEDS CLARIFICATION` remains.

---

## R1. WireGuard device configuration — wgctrl

**Decision**: `golang.zx2c4.com/wireguard/wgctrl` (+ `wgctrl/wgtypes`) for key
generation and device configuration (private key, listen port, peers).

**Rationale**:
- DESIGN §6.4 names wgctrl for peer management over netlink. It is the official
  WireGuard Go control library, configures the kernel device directly (no `wg` CLI),
  and produces the same device the `wg` tool reads.
- `wgtypes.GeneratePrivateKey()` / `Key.PublicKey()` give us the server identity;
  `Key` (un)marshals as base64 for persistence.

**Note**: wgctrl configures an **existing** device; it does not create the network
link. Link creation is R2.

**Alternatives**: shelling to `wg set` — rejected (less testable, depends on the
binary, no atomic config).

---

## R2. Link lifecycle (create/adopt/address/up) — vishvananda/netlink

**Decision**: `github.com/vishvananda/netlink` to create the `wireguard`-type link,
assign the address, and bring it up.

**Rationale**:
- wgctrl cannot create the link. netlink is the standard pure-Go (no CGO) library
  for RTNETLINK and supports `netlink.Wireguard` link type (`LinkAdd`), `AddrReplace`,
  `LinkSetUp`, and `LinkByName` for adopt-or-create.
- Adopt logic (FR-008/FR-016): `LinkByName(name)` → if present and type `wireguard`,
  reconcile (AddrReplace + ensure up + wgctrl ConfigureDevice, all idempotent) and
  **do not** delete it; if absent, `LinkAdd`; if present but wrong type, error
  (conflict, US3-4). This preserves the live interface across restarts (SC-006).

**Alternatives**: shelling to `ip link add … type wireguard` / `ip addr add` —
rejected (binary dependency, less testable). Raw netlink by hand — rejected
(reinventing the library).

---

## R3. nftables table — google/nftables (netlink, no nft binary)

**Decision**: `github.com/google/nftables` to build the `inet lanweave` table and
its forward chain over netlink.

**Rationale**:
- The recon shows the `nft` **CLI is absent** while the `nf_tables` kernel module is
  loaded. A netlink library works against the loaded module with no binary dependency,
  and the resulting table is fully visible to `nft list table inet lanweave` on hosts
  that do have the CLI (same kernel subsystem) — so operator verification still works.
- google/nftables supports the incremental `add/delete element` operations feature
  005 needs, and batched/atomic flushes for the startup rebuild.
- This **refines DESIGN §6**'s implicit "shell to nft" with a netlink implementation;
  the on-kernel result and the documented rule model are unchanged.

**This feature's table shape** (empty default-deny skeleton):
```
table inet lanweave {
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
}
```
No sets, no rules (FR-011/FR-012). Feature 005 adds `set zone_<id>` + accept rules.

**Rebuild idempotency (FR-013)**: on startup, delete the table if present, then add
table + chain in one netlink flush. Deterministic clean state regardless of stale
contents.

**Alternatives**: shelling to `nft -f` — rejected (CLI absent here; less atomic/testable).

---

## R4. Server private key persistence

**Decision**: store the base64 key string at `<data_dir>/wg_private`, file mode
`0600`. On startup: if the file exists, read+parse (`wgtypes.ParseKey`); on parse or
read failure, **return an error and stop** — never regenerate (FR-004/SC-005). If
absent, generate, write `0600`, and use it. If present with broader-than-`0600`
perms, tighten to `0600` and log a warning; if it cannot be tightened, fail.

**Rationale**: A stable server public key is the anchor every client config points
at. Silent regeneration on corruption would rotate the identity and orphan the whole
fleet — exactly the failure FR-004 forbids. Owner-only perms protect the key at rest.
The public key is derived (`Key.PublicKey()`), never persisted separately.

---

## R5. First usable address derivation

**Decision**: pure function `FirstUsableAddress(cidr string) (netip.Addr, prefixLen, error)`
using `net/netip`. For `100.127.0.0/16` → `100.127.0.1`, assigned to the interface as
`100.127.0.1/16` so the interface route covers the whole pool (FR-006).

**Rationale**: "First usable" = network base address + 1, reserving the `.0` network
address (DESIGN §3.2). Pure and fully unit-testable unprivileged. Edge cases: a `/31`
or `/32` (or any range without a usable host above the base) → error
(spec edge case "pool too small").

**Note**: feature 004's IPAM will allocate client addresses from the same pool; if it
needs this helper it can lift it to a shared package then. Not abstracted now.

---

## R6. Enabling IPv4 forwarding

**Decision**: write `1` to `/proc/sys/net/ipv4/ip_forward` at startup; idempotent;
**not** reverted on shutdown (FR-009/FR-010).

**Rationale**: Direct, dependency-free, and idempotent. Reverting on shutdown could
disrupt other host services that rely on forwarding, so we leave it (documented host
side effect). Reading the file is unprivileged; writing requires privilege.

**Alternatives**: `sysctl -w` (binary), netlink sysctl — rejected (proc write is the
simplest correct approach).

---

## R7. Wiring into app.Run & lifecycle

**Decision**: in `app.Run`, after admin bootstrap and before building the HTTP
server, run: (1) `wg` key load/generate, (2) `wg` interface ensure, (3) `netfw`
enable forwarding, (4) `netfw` rebuild table. Any failure returns an error and
prevents serving (FR-014). On shutdown, close the wgctrl/netlink client handles but
**do not** remove the interface, forwarding, or table (FR-010/FR-016).

**Rationale**: Data-plane readiness is a startup precondition; failing fast avoids a
half-configured serving state. Leaving kernel state in place across restarts keeps
existing client tunnels alive (SC-006).

---

## R8. Testing under (un)privilege — the key strategy

**Decision**: Two tiers.
- **Unit (unprivileged)**: `wg.LoadOrGenerateKey` (temp dir), perms enforcement,
  corrupt-key error, `FirstUsableAddress` edge cases, and the config→desired-state
  mapping. These run in any environment, including CI without privilege and the dev host.
- **Integration (privileged, real kernel, NOT mocked)**: interface create/adopt +
  address + wgctrl config, nft table build, ip_forward write. Guarded by a capability
  probe; **skip with a clear `t.Skip` message** when the process cannot manage network
  state.

**How privileged tests actually run**:
- Under real root in CI (a dedicated privileged job), or
- Rootless via a user+network namespace: `unshare -rUn go test ./internal/server/wg/ ./internal/server/netfw/`.
  `unshare -r` maps the caller to root in a new user ns and `-n` gives a fresh net ns
  with `CAP_NET_ADMIN`, allowing link/nftables creation without host root and without
  polluting the host network.

**Why this satisfies constitution Principle II**: the rule is "do not mock the system
boundary; run against real instances; CI provides them via container or privileged
runner." We never mock netlink/wgctrl/nftables — we run them for real where privilege
exists and skip (not fake) where it does not. The skip is loud and the gap is recorded
in plan.md Complexity Tracking. **CI MUST include a privileged/netns job** so these
tests actually execute in the pipeline.

**Hermeticity**: each privileged test uses a unique interface name (e.g.
`wgtest<rand>`) and a unique table name, and defers cleanup, so concurrent/repeat runs
don't collide; running inside a netns isolates them entirely.

**Honest limitation**: the dev host used during implementation (uid 1000, no `nft`
binary) can run the unit tier and the rootless-netns tier *if* `unshare -rUn` is
permitted; if user namespaces are restricted, the privileged tier is skipped there and
must be validated in CI.

---

## R9. What is deliberately deferred (scope guard)

- **No** client peers — feature 004 adds them via wgctrl `ConfigureDevice` peer append.
- **No** zone sets or accept rules — feature 005 grows `netfw` with `AddSet`/`AddElement`.
- **No** IPAM / client address allocation — feature 004.
- **No** `GET /server` endpoint exposing the public key — feature 004 (the key is
  produced here and made available in-process).
- **No** userspace WireGuard fallback — kernel WG required (spec edge case).
- **No** interface/table teardown on shutdown — intentional (FR-010/FR-016).
