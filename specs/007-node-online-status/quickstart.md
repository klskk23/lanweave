# Quickstart: Node Online Status

**Feature**: 007-node-online-status | **Date**: 2026-06-05

Validates that `GET /api/v1/nodes` reports a per-node `online` flag (and
`last_handshake`) driven by the 30 s handshake poll. Builds on the 003/004 setup
(running relay, a registered node).

## Prerequisites

- Server built and running as root (or under `unshare -rUn`), WG interface up.
- An account + JWT (`TOKEN`), and at least one registered node (feature 004).
- For the live-online check: a real client able to bring up the WireGuard tunnel with
  `PersistentKeepalive = 25`.

## Automated checks (CI)

```bash
# Unit: tracker computation + refresh + restart-repopulate + source-error (no kernel).
go test ./internal/server/status/... ./internal/server/api/... ./pkg/protocol/...

# Integration (privileged): Handshakes() reads real kernel peer handshake data.
unshare -rUn go test ./internal/server/wg/...
```

Unprivileged hosts skip the kernel tests via `testutil.RequireNetAdmin`.

## Scenario A — never-connected node is offline (US1, no client needed)

```bash
# Register a node but never connect a client for it.
curl -sk -H "Authorization: Bearer $TOKEN" https://127.0.0.1:8443/api/v1/nodes | jq
```

**Expect**: the node appears with `"online": false` and **no** `last_handshake` field.

## Scenario B — connected node shows online within 30 s (US1/US2, real client)

1. On the client, bring up the tunnel for a registered node (keepalive 25 s).
2. Within ~30 s, list nodes:

```bash
curl -sk -H "Authorization: Bearer $TOKEN" https://127.0.0.1:8443/api/v1/nodes | jq '.nodes[] | {name, online, last_handshake}'
```

**Expect**: that node now shows `"online": true` and a recent `last_handshake`
(SC-001). An idle-but-connected client stays online thanks to keepalive.

## Scenario C — disconnect flips to offline (US2, real client)

1. Stop the client tunnel (or drop its network).
2. Wait 3 min + one poll (~3.5 min total), then list nodes.

**Expect**: the node flips to `"online": false`; `last_handshake` still shows the last
time it was seen (SC-002).

## Scenario D — reconnect recovers within 30 s (US2, real client)

1. Bring the client tunnel back up.
2. Within ~30 s, list nodes.

**Expect**: the node returns to `"online": true` (SC-003).

## Scenario E — restart carries no stale "online" (US2)

1. With a node currently online, stop the server, then stop the client.
2. Start the server again and immediately list nodes.

**Expect**: the node is `"online": false` right after restart (no stale online), and
remains false because the client is gone; if the client were still connected it would
re-show online within one poll (SC-005).

## Success

- Scenario A passes in CI (deterministic).
- Scenarios B–E pass with a real client (manual), demonstrating the ≤ 30 s freshness
  and 3-min offline threshold end-to-end.
