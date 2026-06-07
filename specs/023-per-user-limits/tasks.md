---
description: "Task list for feature 023 per-user-limits"
---

# Tasks: per-user-limits

**Input**: Design documents from `specs/023-per-user-limits/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/limits-api.md, quickstart.md

**Tests**: REQUIRED per Constitution Principle II (NON-NEGOTIABLE). This feature crosses
the SQLite boundary, so each user story carries store-integration + API-acceptance
tests against real SQLite (no mocks), run under `unshare -rUn go test ./...`. GUI
presentation is verified by the manual quickstart matrix (Constitution II GUI/exec
exemption).

**Organization**: Tasks are grouped by user story (US1 device cap = MVP, US2 owned-zone
cap, US3 operator config + admin/unlimited bypass).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 / US2 / US3 (omitted for Setup, Foundational, Polish)

## Path notes

- Server: `internal/server/{config,store,api,app}/`, entrypoint `internal/server/app/app.go`
- Client: `internal/client/{apiclient,ui,i18n}/`
- No DB migration (caps are derived from row counts; see data-model.md)
- No new Go dependencies

---

## Phase 1: Setup

**Purpose**: Establish a clean baseline before any change.

- [X] T001 Verify baseline is green at repo root: `go build ./...` and `unshare -rUn go test ./...` both pass, so later failures are attributable to this feature. Confirm `go.mod` is unchanged (plan: no new dependencies).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Thread a resolved integer cap from config → router → handlers, with a safe
default, so each user story can plug enforcement in. Zero (the unset default before
T002 and the test-harness default) means unlimited, so this phase changes no behavior.

**⚠️ CRITICAL**: US1, US2, and US3 all depend on this phase.

- [X] T002 In `internal/server/config/config.go`: add `LimitsConfig` struct with two three-state pointer fields `MaxDevicesPerUser *int` (toml `max_devices_per_user`) and `MaxOwnedZonesPerUser *int` (toml `max_owned_zones_per_user`); add `Limits LimitsConfig \`toml:"limits"\`` to `Config`; in `applyDefaults()` set each nil pointer to 10 (mirrors the existing `ServerConfig.TLS *bool` three-state pattern — absent ≠ explicit 0). (Negative-value validation is deferred to US3 / T024.)
- [X] T003 [P] In `internal/server/api/router.go` add `MaxDevicesPerUser int` and `MaxOwnedZonesPerUser int` to `Options`; in `internal/server/api/auth_handlers.go` add matching `maxDevicesPerUser int` and `maxOwnedZonesPerUser int` fields to the `handlers` struct; in `NewRouter`'s `&handlers{…}` assign them from `opts`. (Carry plain ints, not the config pointer type, so the zero-value test harness reads as unlimited.)
- [X] T004 In `internal/server/app/app.go` (the `api.NewRouter(api.Options{…})` call near line 106): dereference `cfg.Limits.MaxDevicesPerUser` / `cfg.Limits.MaxOwnedZonesPerUser` (guaranteed non-nil after `applyDefaults`) and pass the values into the new `Options` fields. (Depends on T002, T003.)

**Checkpoint**: Config loads with `[limits]` (or defaults to 10), and the values reach `handlers` unused. Suite still green.

---

## Phase 3: User Story 1 - Device cap (Priority: P1) 🎯 MVP

**Goal**: A regular user is refused device registration once they hold
`max_devices_per_user` devices; deleting one frees a slot.

**Independent Test**: With a device cap configured via `api.Options`, register up to the
cap (201s), the next is 409 `device_limit_reached` with no node created, delete one,
register again succeeds.

### Tests for User Story 1 (write first; must FAIL before T008–T010) ⚠️

- [X] T005 [P] [US1] In `internal/server/store/nodes_test.go` add integration tests against the real store: (a) at cap → `ErrDeviceLimitReached`, no row inserted; (b) one below cap → success, now at cap; (c) delete a node then create → success (slot freed); (d) `maxDevices <= 0` → unlimited; (e) concurrency: fire many parallel `Create` for one user one-below-cap, assert final count never exceeds the cap (SC-010).
- [X] T006 [P] [US1] In `internal/server/api/node_handlers_test.go` add an acceptance test: a regular user (non-admin, seeded via invite/register) at the cap gets `409` with `error == "device_limit_reached"` from `POST /api/v1/nodes`, `GET /api/v1/nodes` count unchanged; one below cap → `201`; delete then re-register → `201`. (Set the cap through `api.Options.MaxDevicesPerUser` in the harness.)
- [X] T007 [P] [US1] In `internal/client/apiclient/client_test.go` add a case: a `409 {"error":"device_limit_reached"}` response maps to `apiclient.ErrDeviceLimitReached`.

### Implementation for User Story 1

- [X] T008 [US1] In `internal/server/store/nodes.go`: add `var ErrDeviceLimitReached = errors.New(...)`; change `Create` to `Create(ctx, userID, name, pubKey string, first, last uint32, maxDevices int)`. When `maxDevices > 0` use the conditional statement `INSERT INTO nodes (...) SELECT ?,?,?,?,? WHERE (SELECT COUNT(*) FROM nodes WHERE user_id = ?) < ?` and return `ErrDeviceLimitReached` when `RowsAffected()==0`; when `maxDevices <= 0` keep the existing unconditional INSERT. Preserve the ip-retry loop (UNIQUE `nodes.ip` → retry) and existing UNIQUE handling (pubkey/name). Update existing direct callers in `internal/server/store/nodes_test.go` (and any other store test) to pass `0`. (Makes T005 pass.)
- [X] T009 [US1] In `internal/server/api/node_handlers.go` `registerNode`: pass `h.maxDevicesPerUser` as the new `Create` argument; add a `case errors.Is(err, store.ErrDeviceLimitReached):` arm returning `protocol.WriteJSONError(w, http.StatusConflict, "device_limit_reached", "You have reached your device limit.")`. (Depends on T008; makes T006 pass.)
- [X] T010 [US1] In `internal/client/apiclient/client.go`: add `ErrDeviceLimitReached = errors.New("device limit reached")` to the error block and a `case "device_limit_reached": return ErrDeviceLimitReached` arm in `mapError`. (Makes T007 pass.)
- [X] T011 [P] [US1] In `internal/client/ui/wizard.go` device-setup error switch: add `case errors.Is(err, apiclient.ErrDeviceLimitReached): return i18n.T("wizard.errDeviceLimit")`. (Depends on T010.)
- [X] T012 [P] [US1] Add key `wizard.errDeviceLimit` to `internal/client/i18n/zh-Hans.json` (e.g. "设备数量已达上限——请删除一台后再添加。") and `internal/client/i18n/en.json` (e.g. "You've reached your device limit — remove one before adding another.").

**Checkpoint**: Device cap enforced end-to-end for regular users; MVP shippable.

---

## Phase 4: User Story 2 - Owned-zone cap (Priority: P2)

**Goal**: A regular user is refused zone creation once they own
`max_owned_zones_per_user` zones; deleting an owned zone frees a slot; joining another
user's zone is never counted.

**Independent Test**: With a zone cap configured, create up to the cap (201s), next is
409 `zone_limit_reached` with no zone created, delete an owned zone then create again
succeeds; while at the cap, joining a different user's zone still returns 200.

### Tests for User Story 2 (write first; must FAIL before T016–T018) ⚠️

- [X] T013 [P] [US2] In `internal/server/store/zones_test.go` add integration tests: (a) owner at cap → `ErrOwnedZoneLimitReached`, no row; (b) one below → success; (c) delete an owned zone then create → success; (d) `maxOwnedZones <= 0` → unlimited; (e) a user at their owned-zone cap can still be `Join`ed to a zone owned by another user (membership does not count toward the owner cap); (f) concurrency: fire many parallel `Create` for one owner one-below-cap, assert the final owned count never exceeds the cap (SC-010 zone side — same conditional-INSERT atomicity as T005e).
- [X] T014 [P] [US2] In `internal/server/api/zone_handlers_test.go` add an acceptance test: a regular user at the zone cap gets `409 {"error":"zone_limit_reached"}` from `POST /api/v1/zones`, no zone created; delete owned → create `201`; while at the cap, `POST /api/v1/zones/{name}/join` against another user's zone (correct password) → `200`. (Set the cap via `api.Options.MaxOwnedZonesPerUser`.)
- [X] T015 [P] [US2] In `internal/client/apiclient/client_test.go` add a case: `409 {"error":"zone_limit_reached"}` maps to `apiclient.ErrOwnedZoneLimitReached`.

### Implementation for User Story 2

- [X] T016 [US2] In `internal/server/store/zones.go`: add `var ErrOwnedZoneLimitReached = errors.New(...)`; change `Create` to `Create(ctx, ownerID int64, name, passwordHash string, maxOwnedZones int)`. When `maxOwnedZones > 0` use `INSERT INTO zones (...) SELECT ?,?,?,? WHERE (SELECT COUNT(*) FROM zones WHERE owner_user_id = ?) < ?` and return `ErrOwnedZoneLimitReached` when `RowsAffected()==0`; when `<= 0` keep the unconditional INSERT. Preserve `zones.name` UNIQUE → `ErrZoneNameTaken`. Update existing direct callers in `internal/server/store/zones_test.go` to pass `0`. (Makes T013 pass.)
- [X] T017 [US2] In `internal/server/api/zone_handlers.go` `createZone`: pass `h.maxOwnedZonesPerUser` as the new `Create` argument; add `case errors.Is(err, store.ErrOwnedZoneLimitReached):` returning `protocol.WriteJSONError(w, http.StatusConflict, "zone_limit_reached", "You have reached your zone limit.")`. (Depends on T016; makes T014 pass. The join path is untouched.)
- [X] T018 [US2] In `internal/client/apiclient/client.go`: add `ErrOwnedZoneLimitReached = errors.New("zone limit reached")` and a `case "zone_limit_reached": return ErrOwnedZoneLimitReached` arm in `mapError`. (Same file as T010 — sequence after it.)
- [X] T019 [P] [US2] In `internal/client/ui/panel.go` zone-create error switch: add `case errors.Is(err, apiclient.ErrOwnedZoneLimitReached): return i18n.T("panel.errZoneLimit")`. (Depends on T018.)
- [X] T020 [P] [US2] Add key `panel.errZoneLimit` to `internal/client/i18n/zh-Hans.json` (e.g. "你创建的区域已达上限——请删除一个后再创建。") and `internal/client/i18n/en.json` (e.g. "You've reached your zone limit — delete one you own before creating another."). (Same files as T012 — sequence after it.)

**Checkpoint**: Both caps enforced for regular users; join remains uncapped.

---

## Phase 5: User Story 3 - Operator config + admin/unlimited bypass (Priority: P3)

**Goal**: Caps are configurable (unset → 10, 0 → unlimited, negative → startup error);
the admin account is exempt; lowering a cap grandfathers existing resources.

**Independent Test**: Start with caps unset (effective 10), zero (unlimited), and
negative (startup fails); confirm an admin exceeds any positive cap; confirm a user
already over a lowered cap keeps everything but cannot create more.

### Tests for User Story 3 (write first; must FAIL before T024–T025) ⚠️

- [X] T021 [P] [US3] In `internal/server/config/config_test.go`: (a) `[limits]` absent → both caps default to 10 after `Load`/`applyDefaults`; (b) explicit `0` is preserved (not re-defaulted); (c) a negative value makes `Validate()` return an error naming the field; (d) a valid positive config validates clean.
- [X] T022 [P] [US3] In `internal/server/api/node_handlers_test.go` and `internal/server/api/zone_handlers_test.go`: with a small positive cap configured, the **admin** account can register devices and create zones beyond the cap (all `201`) — admin is exempt (SC-008).
- [X] T023 [P] [US3] In `internal/server/api/node_handlers_test.go`: grandfathering — seed a regular user with more devices than a (lower) configured cap, assert `GET /api/v1/nodes` still lists them all and they are usable, the next `POST /api/v1/nodes` is `409 device_limit_reached`, and nothing was removed (SC-009).

### Implementation for User Story 3

- [X] T024 [US3] In `internal/server/config/config.go` `Validate()`: append an error when a resolved `Limits.MaxDevicesPerUser`/`MaxOwnedZonesPerUser` is `< 0` (message form `"limits.max_devices_per_user must be >= 0 (0 = unlimited)"`), joined via the existing `errors.Join` so the operator sees it with all other config problems and the server refuses to start. (Makes T021c pass.)
- [X] T025 [US3] Admin exemption at the call sites: in `internal/server/api/node_handlers.go` `registerNode` compute `effective := h.maxDevicesPerUser; if id.IsAdmin { effective = 0 }` and pass `effective` to `Nodes().Create`; in `internal/server/api/zone_handlers.go` `createZone` do the same with `h.maxOwnedZonesPerUser`. (`0` already means unlimited in the store, so admin reuses the unlimited path — see research.md Decision 2. Edits the T009/T017 handlers; makes T022 pass.)
- [X] T026 [P] [US3] In `DESIGN.md` §10.3 config example: add a `[limits]` block with `max_devices_per_user = 10` and `max_owned_zones_per_user = 10` plus a comment noting `unset → 10`, `0 → unlimited`, `negative → startup error` (additive; does not contradict the frozen design).

**Checkpoint**: All three stories independently functional.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T027 [P] In `docs/ROADMAP.md` mark slice 023 (per-user-limits) as done (status line + table), per the Constitution workflow (check off on completion).
- [ ] T028 Run the `specs/023-per-user-limits/quickstart.md` server steps, then the manual GUI matrix G1–G4 on the Windows client (Mesa `opengl32.dll` present on the test VM): device-limit message in the wizard, zone-limit message in the panel, join-while-capped succeeds, messages follow the in-app language selector.
- [X] T029 [P] Final gate: `unshare -rUn go test ./...` green; `gofmt -l`, `go vet ./...`, and `staticcheck ./...` clean (Constitution I).

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (T001)**: no dependencies.
- **Foundational (T002–T004)**: after Setup. T004 depends on T002 + T003. **Blocks all stories.**
- **US1 (T005–T012)**, **US2 (T013–T020)**, **US3 (T021–T026)**: each after Foundational. Recommended order P1 → P2 → P3.
- **Polish (T027–T029)**: after the stories you intend to ship.

### Cross-story shared files (sequence, do not parallelize across stories)

- `internal/client/apiclient/client.go`: T010 (US1) then T018 (US2).
- `internal/client/i18n/zh-Hans.json` + `en.json`: T012 (US1) then T020 (US2).
- `internal/server/api/node_handlers.go`: T009 (US1) then T025 (US3); `zone_handlers.go`: T017 (US2) then T025 (US3).

### Within each story

- Tests (T005–T007 / T013–T015 / T021–T023) written first and failing, then implementation.
- Store change before handler (T008→T009, T016→T017); sentinel before client mapping (T010→T011, T018→T019).

### Parallel opportunities

- T002 ‖ T003 (Foundational, different files).
- US1 tests T005 ‖ T006 ‖ T007; then T011 ‖ T012.
- US2 tests T013 ‖ T014 ‖ T015; then T019 ‖ T020.
- US3 tests T021 ‖ T022 ‖ T023; T026 ‖ the US3 impl.
- If staffed in parallel after Foundational: US1, US2, US3 can proceed concurrently, honoring the shared-file sequencing above.

---

## Parallel Example: User Story 1 tests

```bash
# Write these together (different files), confirm they FAIL, then implement:
Task: "store device-cap integration tests in internal/server/store/nodes_test.go"   # T005
Task: "api device-cap acceptance test in internal/server/api/node_handlers_test.go" # T006
Task: "apiclient device_limit_reached mapping test in internal/client/apiclient/client_test.go" # T007
```

---

## Implementation Strategy

### MVP first (US1 only)

1. Phase 1 Setup → 2. Phase 2 Foundational → 3. Phase 3 US1 → 4. STOP and validate the
   device cap end-to-end (server tests + wizard message) → ship.

### Incremental delivery

US1 (MVP) → US2 (owned-zone cap) → US3 (operator config + admin/unlimited bypass), each
independently testable and shippable, then Polish (ROADMAP check-off, manual GUI matrix,
lint/test gate).

---

## Notes

- No DB migration: caps are derived from `COUNT(*)`; deletion frees a slot and lowered
  caps grandfather existing rows automatically (data-model.md).
- Atomicity comes from the single conditional-INSERT statement under SQLite's writer
  lock — no explicit transaction (research.md Decision 1).
- Admin exemption == effective cap 0 == the unlimited store path (research.md Decision 2).
- TODO.md at repo root is personal and must never be committed.
