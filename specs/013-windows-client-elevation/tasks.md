---

description: "Task list for 013-windows-client-elevation"
---

# Tasks: Windows Client Administrator Elevation

**Input**: Design documents from `specs/013-windows-client-elevation/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/startup-elevation.md, quickstart.md

**Tests**: Per constitution Principle II, the testable logic (the pure relaunch command-line
helper) ships with headless unit tests. The UAC consent prompt and real WinTun adapter creation
cannot run in headless CI; those acceptance steps are validated manually on Windows per
`quickstart.md` (documented GUI/OS exception in plan.md Complexity Tracking, consistent with
features 009–012).

**Organization**: Tasks are grouped by user story. All three user stories are realized by the
single `EnsureElevated()` entry point, so they share a file; story phases add the happy path
(US1), the decline branch (US2), and the already-elevated guard (US3) incrementally.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Exact file paths included in each task

## Path Conventions

New package at `internal/client/winelevate/`; call site in `cmd/lanweave-client/main.go`;
documentation in `INSTALL.md` and `packaging/windows/lanweave-client.nsi`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the package so headless builds stay green from the first commit

- [X] T001 Create `internal/client/winelevate/` and add `internal/client/winelevate/elevate_other.go` (`//go:build !windows`) with a no-op `func EnsureElevated()` and package doc comment, so the package compiles on headless/Linux builds immediately.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The pure relaunch-command-line helper that the Windows path depends on

**⚠️ CRITICAL**: The Windows implementation (US1) uses this helper; complete this phase first.

- [X] T002 Write headless unit tests for the relaunch command-line helper in `internal/client/winelevate/args_test.go` — table cases: empty args → `""`; `["--insecure"]` → `--insecure`; an argument containing spaces → wrapped in double quotes; an argument containing a double quote → quotes escaped. Tests MUST fail before T003.
- [X] T003 Implement the pure helper in `internal/client/winelevate/args.go`: `commandLine(args []string) string` that quotes any argument containing whitespace or a double quote (escaping embedded quotes) and space-joins them. No build tag (compiles on all platforms). Make T002 pass.

**Checkpoint**: `go test ./internal/client/winelevate/...` green headlessly.

---

## Phase 3: User Story 1 - Connect after a normal launch (Priority: P1) 🎯 MVP

**Goal**: An unelevated shortcut launch raises one UAC prompt; on consent the client runs
elevated and can create the WinTun adapter. (Already-elevated launches return immediately —
this guard also serves US3.)

**Independent Test**: quickstart §D — standard user, shortcut launch, accept UAC, Connect →
`100.127.x.y` adapter appears, server reachable, no manual "Run as administrator".

### Implementation for User Story 1

- [X] T004 [US1] Implement the happy path in `internal/client/winelevate/elevate_windows.go` (`//go:build windows`): `EnsureElevated()` returns immediately when `windows.GetCurrentProcessToken().IsElevated()` is true; otherwise relaunch via `windows.ShellExecute(0, ptr("runas"), ptr(exe), ptr(commandLine(os.Args[1:])), nil, windows.SW_SHOWNORMAL)` using `os.Executable()` and the T003 helper, then `os.Exit(0)` on success.
- [X] T005 [US1] Call `winelevate.EnsureElevated()` as the first statement in `main()` in `cmd/lanweave-client/main.go` (before `app.NewWithID`), and replace the inaccurate manifest comment (lines ~31–33) with an accurate description of runtime self-elevation.
- [X] T006 [US1] Verify the Windows syscall path compiles on the dev host: `GOOS=windows go vet ./internal/client/winelevate` and `GOOS=windows go build ./internal/client/winelevate` both succeed.
- [ ] T007 [US1] Manual acceptance on Windows (quickstart §D): normal shortcut launch → one UAC prompt → accept → Connect creates the adapter; server pingable, no manual elevation. (SC-001, SC-002, SC-005)

**Checkpoint**: MVP — a normal launch elevates and connects.

---

## Phase 4: User Story 2 - Declining elevation is honest (Priority: P2)

**Goal**: Declining (or a failed relaunch) shows one human-readable message and exits without a
misleading UI.

**Independent Test**: quickstart §E — decline the UAC prompt → one message box → app closes,
never appears connected.

### Implementation for User Story 2

- [X] T008 [US2] Add the declined/failed branch in `internal/client/winelevate/elevate_windows.go`: when `ShellExecute` returns an error, show `windows.MessageBox(0, ptr(msg), ptr(caption), windows.MB_OK|windows.MB_ICONERROR)` stating administrator rights are required and the app is closing, then `os.Exit(1)`; never fall through to the UI.
- [ ] T009 [US2] Manual acceptance on Windows (quickstart §E): decline UAC → exactly one message box → app exits; no "connected" state. (SC-003)

**Checkpoint**: US1 and US2 both behave correctly.

---

## Phase 5: User Story 3 - Already-elevated launch is clean (Priority: P3)

**Goal**: An already-elevated launch shows no second prompt and does not relaunch (loop-break).

**Independent Test**: quickstart §F — right-click → Run as administrator → app opens, no second
prompt, no relaunch.

### Implementation for User Story 3

- [X] T010 [US3] In `internal/client/winelevate/elevate_windows.go`, confirm the `IsElevated()`-true path returns before any `ShellExecute` (the loop break) and add a one-line WHY comment documenting that the guard prevents a relaunch loop.
- [ ] T011 [US3] Manual acceptance on Windows (quickstart §F): launch elevated → no additional prompt, no visible relaunch, app works normally. (SC-004)

**Checkpoint**: All three stories independently verified.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation accuracy (FR-008) and full-suite verification

- [X] T012 [P] Update the Windows section of `INSTALL.md`: state that the client self-elevates via a UAC prompt on launch (no manual "Run as administrator" needed), keeping the manual elevation as a noted fallback. (FR-008)
- [X] T013 [P] Update the header comment in `packaging/windows/lanweave-client.nsi` to state the app self-elevates at startup (the installer itself remains admin only to install the driver / write to Program Files). (FR-008)
- [X] T014 Run `gofmt -l .`, `go vet ./...`, `staticcheck ./...` (if present), and `go test ./...` — all clean/green, including the `winelevate` unit tests.
- [X] T015 Run `go build -tags gui ./...` on Linux to confirm the non-Windows no-op stub keeps the GUI build green.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none — start immediately.
- **Foundational (Phase 2)**: after Setup; T003 BLOCKS the Windows implementation (T004 uses `commandLine`).
- **US1 (Phase 3)**: after Foundational. Delivers the MVP and the shared `IsElevated()` guard.
- **US2 (Phase 4)**: after US1 (extends the same `elevate_windows.go` `EnsureElevated`).
- **US3 (Phase 5)**: after US1 (verifies the guard added in T004).
- **Polish (Phase 6)**: after the stories you intend to ship; T012/T013 are independent docs.

### Within Each Story

- T002 (test) before T003 (impl) — test fails first.
- T004 before T005 (wire only after the function exists), before T006 (compile-verify).
- T008 extends T004's function; T010 verifies T004's guard.

### Parallel Opportunities

- T012 and T013 are different files → `[P]` together.
- The manual-acceptance tasks (T007, T009, T011) can be run in one Windows session after the code lands.
- Most code tasks touch the same `elevate_windows.go` and are therefore sequential.

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup → Phase 2 Foundational (helper + unit tests green).
2. Phase 3 US1 (windows happy path + main wiring + `GOOS=windows` vet).
3. **STOP and VALIDATE**: quickstart §D on a clean Windows machine.

### Incremental Delivery

1. Setup + Foundational → package compiles, helper tested.
2. US1 → normal launch elevates & connects (MVP, demo).
3. US2 → decline is honest (message + exit).
4. US3 → already-elevated is clean.
5. Polish → docs corrected (FR-008), full suite + GUI build green.

---

## Notes

- [P] tasks = different files, no dependencies.
- The single automated test is the pure `commandLine` helper (T002/T003) — real, no mocks.
- UAC consent + adapter creation are validated manually on Windows (documented Principle II GUI/OS exception).
- The `winelevate` package has no `gui` build tag, so its tests run under plain `go test ./...`.
- Commit after each logical group; keep `gofmt`/`vet`/`staticcheck` clean.
