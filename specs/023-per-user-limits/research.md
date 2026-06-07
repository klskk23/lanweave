# Research: per-user-limits

Phase 0 decisions. The spec left no `[NEEDS CLARIFICATION]` markers (all six design
branches were resolved in a prior grilling session); the open questions here are
purely *how* to implement the resolved *what*.

## Decision 1 — Atomic count-and-insert without a TOCTOU window

**Decision**: Enforce each cap with a single conditional `INSERT … SELECT … WHERE`
statement that folds the count check into the insert:

```sql
-- device cap (maxDevices > 0)
INSERT INTO nodes (user_id, name, wg_pubkey, ip, created_at)
SELECT ?, ?, ?, ?, ?
WHERE (SELECT COUNT(*) FROM nodes WHERE user_id = ?) < ?;

-- owned-zone cap (maxOwnedZones > 0)
INSERT INTO zones (name, password_hash, owner_user_id, created_at)
SELECT ?, ?, ?, ?
WHERE (SELECT COUNT(*) FROM zones WHERE owner_user_id = ?) < ?;
```

After execution, branch on `RowsAffected()`:
- `1` → inserted; read `LastInsertId()`.
- `0` → the `WHERE` was false → cap reached → return `ErrDeviceLimitReached` /
  `ErrOwnedZoneLimitReached`.

A UNIQUE violation still surfaces as an *error* (not 0 rows), so existing handling is
unchanged: `nodes.ip` collision → retry; `nodes.wg_pubkey` → `ErrPubKeyTaken`;
`nodes.name`/`user_id` → `ErrNodeNameTaken`; `zones.name` → `ErrZoneNameTaken`.

**Rationale**: SQLite serializes writers (one writer at a time). The danger is the
*read-then-write gap*: two requests both `SELECT COUNT(*)=9` (cap 10), then both
insert → 11. Folding the count into the insert removes the gap entirely — the count
sub-select is evaluated as part of the single atomic write statement under the writer
lock, so a concurrent request that committed first is already visible. This satisfies
FR-013 / SC-010 with **no explicit transaction**, keeping the change minimal
(Constitution I: no premature abstraction, no transaction-manager plumbing).

**Alternatives considered**:
- *Separate `SELECT COUNT` then `INSERT` in a deferred `tx`*: still races — a deferred
  transaction takes only a shared read lock for the SELECT, so two txns both read 9.
  Rejected.
- *`BEGIN IMMEDIATE` transaction* (write lock up front, then SELECT, then INSERT):
  correct, but requires either a DSN-wide `_txlock=immediate` (changes every
  transaction in the app) or grabbing a dedicated `*sql.Conn` and issuing raw
  `BEGIN IMMEDIATE`/`COMMIT`. More moving parts than the single-statement form for no
  benefit. Rejected.
- *App-level mutex per user*: doesn't survive the single-instance→multi-instance
  boundary and is slower; SQLite already serializes writers. Rejected.

**Edge note**: when a user is at their zone cap *and* picks an already-taken zone
name, the conditional insert evaluates the `WHERE` first → 0 rows → the user gets
`zone_limit_reached` rather than `zone_name_taken`. Acceptable: they cannot create
either way, and the spec only requires the two refusals be *distinguishable from each
other and from unrelated failures*, which holds. Documented so it isn't mistaken for a
bug.

## Decision 2 — `0 = unlimited` handled in the store, admin exemption handled in the handler

**Decision**: The store `Create` methods take a `maxN int` parameter and treat
`maxN <= 0` as **unlimited** — they run the original unconditional `INSERT` (no
`WHERE`, no `COUNT`). Only `maxN > 0` uses the conditional insert.

The **admin exemption** is implemented by the handler passing an *effective* limit of
`0` when the caller is admin:

```go
effective := h.maxDevicesPerUser          // resolved config value (0 = unlimited)
if id.IsAdmin {                            // id is *auth.Claims from IdentityFrom
    effective = 0
}
node, err := h.store.Nodes().Create(ctx, id.UserID, name, pubkey, first, last, effective)
```

**Rationale**: One mechanism serves three cases. Because `0 = unlimited` is already a
config semantic, "admin is exempt" collapses to "admin's effective cap is 0" — no
separate exemption branch in the store, no `isAdmin` flag threaded into SQL. This is
the elegant unification surfaced during the grill: admin exemption and the unlimited
sentinel are the *same* code path (`maxN <= 0 → no check`). Keeps the store ignorant
of identity/roles (Constitution I: single responsibility — the store enforces a
number, the handler decides the number).

**Alternatives considered**:
- *Pass `isAdmin bool` into the store and branch there*: leaks auth concepts into the
  persistence layer; two booleans (`isAdmin`, `unlimited`) expressing one idea.
  Rejected.
- *Resolve `0`→`MaxInt` in the handler and always use the conditional insert*: works
  but executes a pointless `COUNT(*)` on every admin/unlimited insert. Rejected for the
  cheap early-out.

## Decision 3 — Config three-state (`unset` / `0` / negative) via `*int` pointers

**Decision**: Add a `[limits]` section with two `*int` fields, mirroring the existing
`ServerConfig.TLS *bool` three-state pattern:

```go
type LimitsConfig struct {
    MaxDevicesPerUser   *int `toml:"max_devices_per_user"`
    MaxOwnedZonesPerUser *int `toml:"max_owned_zones_per_user"`
}
```

- `applyDefaults()`: if pointer is `nil` (key absent) → set to `10`. An explicit `0`
  stays `0` (unlimited); the pointer keeps "unset" distinct from "explicit zero",
  exactly as `TLS` keeps "unset" distinct from "explicit false".
- `Validate()`: if a (now-defaulted, non-nil) value is `< 0` → append an error so
  `errors.Join` reports it with everything else and the server refuses to start.

The server `main` then dereferences the resolved pointers and passes plain `int`
values into `api.Options.MaxDevicesPerUser` / `MaxOwnedZonesPerUser`.

**Rationale**: Reuses a pattern already in the codebase (`TLS *bool`), so reviewers
recognize it instantly. The "absent ≠ explicit zero" distinction is the whole point of
the feature's US3 (unset → default 10; zero → unlimited), and the pointer is the
idiomatic three-state representation here. Validation at the boundary (Constitution
Security & Ops) catches negative caps before any request is served.

**Alternatives considered**:
- *Plain `int` with `0` meaning "use default 10"*: collapses "unset" and "explicit 0",
  making the unlimited sentinel impossible. Rejected — directly contradicts US3.
- *`-1` as the unlimited sentinel, `0` as "block all"*: less intuitive for operators
  and inverts the resolved spec semantic (`0 = unlimited`). Rejected.

## Decision 4 — No schema migration; counts are derived

**Decision**: Do **not** add columns or a counters table. The "allowance" is computed
live as `COUNT(*)` over the user's current `nodes` / owned `zones` rows.

**Rationale**: Deletion must free a slot immediately (FR-005/FR-006, SC-003), and
grandfathering must let existing rows survive a lowered cap untouched (FR-010,
SC-009). A live `COUNT(*)` gives both for free: delete a row and the next count drops;
lower the cap and the `WHERE count < newCap` simply stays false until rows are removed.
A stored counter would have to be kept in sync on every create/delete (and on cascade
deletes when a user or node is removed) — more state to drift, violating Constitution
I ("nftables/WG are derivative; no hidden runtime-only state" — same spirit: don't
persist what you can derive). The relevant indexes already exist (`nodes.user_id` via
the `UNIQUE(user_id,name)` index; `zones.owner_user_id` is filtered cheaply at this
scale).

**Alternatives considered**:
- *Materialized per-user counter column*: premature optimization with a correctness
  cost (sync bugs, cascade handling). Rejected.

## Decision 5 — Client surfacing reuses the existing typed-error pipeline

**Decision**: Add `ErrDeviceLimitReached` and `ErrOwnedZoneLimitReached` sentinels in
`apiclient`, map the two new server codes in `mapError`, and add one `errors.Is` arm
each to the wizard's device-setup error switch and the panel's zone-create error
switch, backed by two new locale keys (`wizard.errDeviceLimit`, `panel.errZoneLimit`)
in `zh-Hans.json` and `en.json`.

**Rationale**: This is the exact path already used for `node_name_taken`,
`pool_exhausted`, `zone_name_taken`, etc. (Constitution III: one consistent
error-presentation mechanism). No new UI surface; the device-limit message appears at
device setup and the zone-limit message at zone creation, in the active language.

**Alternatives considered**:
- *Reuse a generic "limit reached" message for both*: fails FR-011/FR-012's
  "distinguishable" requirement and the user wouldn't know which resource. Rejected.
