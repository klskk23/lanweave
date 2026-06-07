# Phase 0 Research: Invite Code Expiry

All open questions from the spec were resolved during the grill-me interview
(2026-06-07, recorded in `docs/ROADMAP.md` §026). This document captures the
non-obvious engineering decisions and their rationale.

## Decision 1 — No code-level default for `invite_ttl`; the 24h lives only in `config.toml.example`

**Decision**: `AuthConfig.InviteTTL` is a `string` with no `applyDefaults()` fallback.
An empty/absent value parses to a zero duration, which means *never expire* (write
`expires_at = NULL`). The recommended `"24h"` ships only in `config.toml.example`.

**Rationale**:
- Makes "`0`/空 = 永不过期" literally true: there is exactly one place (the config
  value) that decides expiry, and emptiness is a real off switch rather than a path
  that silently injects a hidden default.
- Upgrades are non-surprising: an operator who upgrades an existing deployment
  without adding the key keeps the old behavior (codes never expire) instead of
  having a 24h window appear under them.
- New deployments still get expiry out of the box because they start from
  `config.toml.example`, which carries `invite_ttl = "24h"`.

**Alternatives considered**:
- *Mirror the Limits `*int` three-state (nil → default 10)*: rejected. That pattern
  injects a default when the key is absent, which would silently turn on expiry for
  upgraders and make "empty = never" false. The semantics we want here are the
  opposite of the Limits caps.
- *Required key (error if missing)*: rejected as hostile to upgrades and to the
  closed/trusted deployments that legitimately want no expiry.

## Decision 2 — Unify "never expire" on SQL NULL

**Decision**: `invites.expires_at` is a nullable `TEXT` (RFC3339). `NULL` is the
single representation of "never expires". Three self-consistent uses converge on it:
1. Pre-existing codes (migrated rows) have `NULL` → grandfathered, valid forever.
2. `invite_ttl = 0`/empty writes `NULL` at generation → global off switch.
3. The registration predicate only rejects `expires_at IS NOT NULL AND expires_at < now()`.

**Rationale**: One NULL semantics covers grandfathering, the global disable, and the
enforcement check without special cases or sentinel timestamps. The `ADD COLUMN`
migration leaves existing rows `NULL` automatically, so grandfathering is free.

**Alternatives considered**:
- *Sentinel far-future timestamp for "never"*: rejected — adds a magic constant and
  a comparison edge case; NULL is the natural absence marker.
- *Backfill existing rows with created_at + 24h on migration*: rejected — violates
  FR-007 (no retroactive expiry).

## Decision 3 — Expired folds into the existing generic `ErrInviteInvalid`

**Decision**: Enforcement is the existing single-row register `UPDATE`, extended with
`AND (expires_at IS NULL OR expires_at > ?)`. When the predicate fails, `RowsAffected`
is `0`, the store returns the existing `ErrInviteInvalid`, and the handler maps it to
HTTP 422 `invite_invalid` — the same response already returned for unknown and
already-used codes.

**Rationale**:
- No new query and no new error path: expiry reuses the same atomic
  claim-the-code `UPDATE` that already prevents double-use.
- No oracle / no disclosure (FR-003, SC-005): a registrant cannot tell an expired
  code from an unknown or used one. This satisfies the security posture without any
  branch that says "expired".

**Alternatives considered**:
- *Separate pre-check SELECT for expiry with a distinct error*: rejected — extra
  round trip, a TOCTOU gap against the claim, and it would leak the expired/unknown
  distinction.

## Decision 4 — Deterministic expiry testing with past-dated rows, no clock seam

**Decision**: Test the expired path by inserting an invite row whose `expires_at` is
in the past (e.g. `now - 1h`) and asserting registration is rejected with the generic
error. Test the unexpired path with a far-future expiry (e.g. `now + 24h`). Both use
the real SQLite store; the production `time.Now()` is left in place.

**Rationale**:
- Satisfies Constitution II: real SQLite, no mock, no wall-clock `sleep` to "wait
  out" a short TTL (which would be flaky and slow).
- A past-dated row exercises exactly the production predicate
  (`expires_at IS NOT NULL AND expires_at < now()`) without needing an injected clock
  — the simplest thing that proves the behavior. No `Clock` interface is introduced
  (Principle I — no premature abstraction).

**Alternatives considered**:
- *Inject a fake clock into the store*: rejected — abstraction not justified by a
  single comparison; past-dated rows give the same coverage with less surface.
- *Generate with a 1s TTL and sleep*: rejected — wall-clock sleep, forbidden by
  Constitution II and inherently flaky.

## Decision 5 — Surface expiry at generation only; no list command, no cleanup

**Decision**: `Create` returns the stamped `expires_at` (or nil) up through the
admin HTTP response; `lanweavectl invite` prints `Expires: <ts>` or `Expires: never`.
No `invite list` command is added; expired unused rows are retained (no background
pruning job).

**Rationale**: Matches the interview scope — the admin learns the deadline at the
moment they hand out a code, which is when they need it. Listing and cleanup are
separable concerns with their own slices if ever needed; adding them now is
premature (Principle I). Retaining expired rows is harmless because enforcement is
purely at registration.

**Alternatives considered**:
- *Add `invite list` showing status incl. expired*: deferred (out of scope, ROADMAP §026).
- *Background sweeper deleting expired rows*: rejected — no functional need; rows are
  inert once expired.

## Cross-references

- Scope, acceptance, and explicit non-goals: `docs/ROADMAP.md` §026.
- Existing invite model being extended: slice 002 (`migrations/0002_invites.sql`,
  admin create endpoint, register validation point).
- Config-loaded-once pattern reused: `AuthConfig` (`jwt_ttl` neighbor).
