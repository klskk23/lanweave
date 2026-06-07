# Phase 0 Research: Client Logout Hardening

All unknowns are design-level (behavioral), since the slice is client-only and
reuses existing server endpoints. Each decision below resolves a Technical
Context choice; there were no `NEEDS CLARIFICATION` markers in the spec.

## D1 — What counts as "network unreachable" (the block trigger)?

**Decision**: Block **only** when the remote-removal attempt fails with
`errors.Is(err, apiclient.ErrUnreachable)`. Any HTTP response from the server —
2xx, 4xx, 5xx — and any certificate error (`*apiclient.CertError`,
`apiclient.ErrUntrustedCert`) is treated as "reachable" and does **not** block.

**Rationale**: `apiclient.attempt` already collapses transport-level failures
(timeout, connection refused, no route) into `ErrUnreachable` at exactly one
place (`client.go:369`), and routes TLS verification failures to distinct cert
errors *before* that fallback. So `ErrUnreachable` is a precise, already-tested
signal for "the bytes never reached a server." This matches the spec's
FR-003/FR-010 split (block on network layer; never block on a server response).

**Alternatives considered**:
- Inspect `net.Error.Timeout()` / `url.Error` directly in the panel — rejected:
  duplicates classification the apiclient already owns, and would couple the
  controller to net internals (Principle I).
- Block on 5xx too — rejected by spec ("不做": blocking limited to network layer);
  a 5xx means the server is alive and the node *may* have been removed, so
  blocking risks a false orphan-prevention that traps the user.

## D2 — Retry policy

**Decision**: At most **3 attempts** total, with a fixed **1 second** sleep
*between* attempts (i.e. attempt, sleep 1 s, attempt, sleep 1 s, attempt). Only a
network-unreachable failure is retried; a definitive non-network outcome
(success, 404-already-gone, 401, 5xx, cert error) exits the loop immediately.

**Rationale**: Matches the frozen ROADMAP design (3×, 1 s, infinite progress bar).
Bounds the worst-case blocking decision to ~2 s of sleeping + 3 quick failed
dials, comfortably under a "feels hung" threshold while giving a flapping link a
couple of chances.

**Alternatives considered**: exponential backoff (rejected — over-engineered for
a 3-shot bounded retry, and lengthens the worst case); retry all errors (rejected
— a 401 or 5xx is not transient in the relevant sense and is handled by its own
branch).

## D3 — Testing the 1 s interval without wall-clock sleeps (Constitution II)

**Decision**: Inject the sleep into `panel.Controller` as an unexported
`sleep func(time.Duration)` seam (defaulting to `time.Sleep`), set by tests to a
no-op (or a recording stub that asserts it was called with `1 * time.Second`
exactly twice). The retry **count** and **branch** are asserted via the fake
`api` returning `ErrUnreachable` a controlled number of times.

**Rationale**: Constitution II forbids wall-clock sleeps in tests; slice 024
already established the injectable-clock pattern (`RefreshTokenRepo.SetClock`).
A `sleep` seam is the controller-side analog and keeps the acceptance tests
instant and deterministic.

**Alternatives considered**: a full clock interface (rejected — heavier than
needed; we only need to *not actually sleep*, plus optionally assert the
interval); `time.Sleep` real in tests (rejected — violates II, makes the suite
slow/flaky).

## D4 — Flow ordering: remove remote node before tearing down the tunnel

**Decision**: New order on logout confirmation:
1. **Remote removal first**, tunnel still UP (the control API is public HTTPS,
   independent of the tunnel) — `removeRemoteNode` retried per D2.
2. On confirmed-removed / already-absent → disconnect tunnel → `firewall.Clear()`
   → revoke RT (`api.Logout()`) → clear keyring (session + RT + device key) +
   `state.Clear()` → return to wizard.
3. On network-unreachable after 3 tries → **block**: do nothing (tunnel, firewall,
   keyring, state all untouched) → return a `blocked` outcome for the UI prompt.

**Rationale**: The current `confirmLogout` disconnects the tunnel *before*
`Logout()` (`ui/panel.go`), but removal goes over the public endpoint, so
disconnecting first is unnecessary and — more importantly — the *old* `Logout()`
cleared local state unconditionally. Reordering to "remote-first, local-only-
after-success" is what actually prevents the orphan. The tunnel teardown moves
*inside* the success/force branches so it never runs on a blocked logout.

**Alternatives considered**: keep disconnect-first (rejected — pointless, and it
muddies the "nothing changed" guarantee of a blocked logout); remove over the
tunnel IP (rejected — the API is reached via the public endpoint regardless, and
the tunnel may itself be the thing that's down).

## D5 — Controller surface: one method with a result, or two methods?

**Decision**: Split into two controller entry points:
- `Logout() (LogoutOutcome, error)` — runs the remote-first flow; returns an
  outcome enum: `LogoutDone` (removed + cleared), `LogoutBlocked` (network-
  unreachable, nothing changed), or `LogoutNeedSignIn` (session expired and
  refresh failed). `error` is reserved for a *local* teardown failure on the
  done path.
- `ForceLogout() error` — the escape hatch: unconditional full local teardown
  (disconnect via the GUI, firewall clear, keyring + state clear), best-effort RT
  revoke, accepts server-side orphan. This is the old 017 always-clear behavior,
  now reachable *only* from the blocked prompt.

**Rationale**: A typed outcome keeps the GUI branchless-by-string and testable.
Separating `ForceLogout` makes the "accept residue" path explicit at the callsite
(Principle I: obvious and reversible) rather than a boolean flag into one method.

**Alternatives considered**: single `Logout(force bool)` (rejected — a boolean
parameter that flips the safety semantics is exactly the obscure callsite
Principle I warns against); returning raw errors and letting the GUI classify
(rejected — duplicates the network-vs-response decision into the UI layer).

## D6 — Refresh-token revocation placement

**Decision**: Revoke the RT (`api.Logout()`) on the **done** path only, *after*
the node removal is confirmed and *before/with* the local keyring clear; it is
best-effort (a failure there does not re-block — the orphan-causing residue, the
node, is already gone). On the **blocked** path the RT is **not** revoked
(nothing changed). On the **force** path it is best-effort revoked too.

**Rationale**: Spec FR-008 + edge case "revocation fails after node removed →
still complete local teardown." The node is the residue that matters for the
block decision; the RT revoke is a security-hygiene best-effort that must never
resurrect a block once we've committed to tearing down.

**Alternatives considered**: revoke before node removal (rejected — if removal
then blocks, we'd have revoked the RT while staying logged in, breaking the
"nothing changed" guarantee of a blocked logout).

## D7 — Session-expired (401) during logout

**Decision**: Rely on apiclient's existing lazy refresh inside `do` — a 401 on
`DeleteNode` auto-renews and retries transparently when an RT is held. Only if
refresh *also* fails (surfacing `ErrSessionExpired`/`ErrRefreshFailed`) does
`Logout()` return `LogoutNeedSignIn`, so the GUI prompts for a fresh sign-in and,
on success, re-invokes `Logout()`. A cancelled sign-in aborts with no local
change.

**Rationale**: Slice 024 already makes 401 "basically disappear" via lazy
refresh; we only need to handle the residual case where refresh itself fails.
Reuses the single chokepoint rather than adding refresh logic to the panel.

**Alternatives considered**: panel-driven explicit refresh before delete
(rejected — duplicates 024's `do` chokepoint).

## D8 — i18n keys

**Decision**: Add bilingual keys for the blocked prompt and force path, e.g.
`panel.logoutBlockedTitle`, `panel.logoutBlockedBody`, `panel.logoutCancel`,
`panel.logoutForce`, `panel.logoutNeedSignIn` (en + zh-Hans). The existing
`i18n_test.go` parity test enforces that both bundles carry identical keys.

**Rationale**: FR-011; reuses the established `i18n.T` mechanism and its parity
guard so a missing translation fails tests, not users.
