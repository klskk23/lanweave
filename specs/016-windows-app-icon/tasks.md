---

description: "Task list for Windows App Icon (016)"
---

# Tasks: Windows App Icon

**Input**: Design documents from `/specs/016-windows-app-icon/`

**Prerequisites**: plan.md, spec.md, research.md, quickstart.md (no data-model.md / contracts/
— this feature has no entities and no external interface).

**Tests**: This feature crosses **no** SQLite/nftables/WireGuard boundary, so the mandatory
integration tier (Constitution II) does not apply and nothing is mocked. The one pure-Go
boundary — the embedded window-icon bytes — gets a real headless unit test (US2). Every other
surface is a Windows-desktop visual, covered by the `quickstart.md` manual matrix under the
project's standing Windows-GUI manual exception (009–014).

**Organization**: Tasks are grouped by user story. The shared **icon-generation pipeline** is
Foundational (every story consumes its output). US1 (EXE-file icon) and US2 (running-window
icon) are both P1 and together form the MVP; US3 (installer/uninstaller/ARP) is the P2
increment.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- File paths are repository-relative.

## Path Conventions

Client/server Go monorepo (per plan.md): client `main` under `cmd/lanweave-client/`, client UI
under `internal/client/ui/`, packaging under `packaging/`, CI under `.github/workflows/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish the pre-change baseline and the asset toolchain before generating anything.

- [X] T001 Baseline on the current tree: confirm `CGO_ENABLED=0 go build ./cmd/lanweave-client` (the non-gui stub) and `CGO_ENABLED=0 go vet ./internal/client/ui/` are green (this is the SC-005 "stub still builds" baseline), and install the asset toolchain on the dev box: `sudo apt-get install -y librsvg2-bin icoutils binutils-mingw-w64` (provides `rsvg-convert`, `icotool`, `x86_64-w64-mingw32-windres`).
- [X] T002 Ensure the authoritative source `packaging/icon.svg` is tracked by git (it is currently untracked); every generation step derives from it, so it must be committed with this feature.

---

## Phase 2: Foundational (Blocking Prerequisites — the generation pipeline)

**Purpose**: One script + its inputs that turn `packaging/icon.svg` into every raster form the
three stories need. **Blocks all user stories** — US1 needs the `.syso`, US2 needs `icon.png`,
US3 needs `icon.ico`.

**⚠️ CRITICAL**: `internal/client/ui/icon.png` MUST exist before `internal/client/ui/icon.go`
(US2) will compile — it is an `//go:embed` target. Generate assets (T007) before writing US2 code.

- [X] T003 Create `packaging/windows/icon.rc` containing a single icon resource line `IDI_ICON1 ICON "packaging/icon.ico"` (the `windres` input that becomes the EXE's embedded icon resource).
- [X] T004 Create `packaging/scripts/gen-icons.sh` (`set -euo pipefail`, no network), and `chmod +x` it. It MUST: (a) `rsvg-convert -w N -h N packaging/icon.svg -o <tmp>/N.png` for N in 16 32 48 256; (b) `icotool -c -o packaging/icon.ico <tmp>/16.png <tmp>/32.png <tmp>/48.png <tmp>/256.png`; (c) copy the 256 px PNG to `internal/client/ui/icon.png`; (d) resolve windres via `WINDRES="${WINDRES:-$(command -v x86_64-w64-mingw32-windres || command -v windres)}"` and run `"$WINDRES" -I . packaging/windows/icon.rc -O coff -o cmd/lanweave-client/resources_windows.syso`.
- [X] T005 [P] Add `cmd/lanweave-client/resources_windows.syso` to `.gitignore` (generated artifact, never committed).
- [X] T006 Add an `icons` target to `Makefile` that runs `./packaging/scripts/gen-icons.sh`, add `icons` to the `.PHONY` list, and make the existing `client` target depend on `icons` (`client: icons`) so a client build cannot silently omit the `.syso`.
- [X] T007 Run `make icons`; verify the three outputs and commit the two raster assets: `file packaging/icon.ico` → "MS Windows icon resource - 4 icons", `file internal/client/ui/icon.png` → "PNG image data, 256 x 256", `file cmd/lanweave-client/resources_windows.syso` → COFF object. Commit `packaging/icon.ico` and `internal/client/ui/icon.png`; the `.syso` stays gitignored.

**Checkpoint**: All raster forms reproducible from the SVG (SC-004); `icon.png` present so the
`ui` package compiles headlessly; `icon.ico` ready for windres + NSIS.

---

## Phase 3: User Story 1 - The installed program shows the lanweave icon (Priority: P1) 🎯 MVP

**Goal**: The client EXE embeds the icon so the program file, Start-menu/desktop shortcuts, the
running taskbar, and Alt+Tab all display the lanweave mark (FR-001).

**Independent Test**: Build the GUI client (CI or local Windows `make icons && go build -tags
gui`); the produced `lanweave-client.exe` file and its shortcuts show the icon. On Linux, the
non-gui stub still builds (the `_windows.syso` is ignored off-Windows).

### Implementation for User Story 1

- [X] T008 [US1] Guarantee the linker auto-links the resource: confirm the generated object is exactly `cmd/lanweave-client/resources_windows.syso` (Go links `*.syso` only from the `main` package dir; the `_windows` suffix scopes it to `GOOS=windows`). If the name/location differs, fix T004's windres `-o` path. Add a one-line WHY comment in `gen-icons.sh` next to the windres step explaining the suffix scoping.
- [X] T009 [US1] Regression guard for SC-005: with the generated `resources_windows.syso` present, confirm `CGO_ENABLED=0 go build ./cmd/lanweave-client` (GOOS=linux stub) still builds and ignores the Windows-only resource object.
- [ ] T010 [US1] Manual verify (Windows; quickstart rows 2–4): from a pipeline build or local `make icons && go build -tags gui -ldflags "-H windowsgui" -o lanweave-client.exe ./cmd/lanweave-client`, confirm the EXE file, the Start-menu and desktop shortcuts, and the running taskbar/Alt+Tab all show the lanweave icon. **[MANUAL — deferred to a Windows desktop; cannot run headlessly]**

**Checkpoint**: The executable and everything inheriting its icon are branded; the headless stub
build is unaffected.

---

## Phase 4: User Story 2 - The running window shows the lanweave icon (Priority: P1)

**Goal**: The live Fyne window carries the lanweave icon as its own window icon (FR-002),
visually consistent with the file icon.

**Independent Test**: Headless — `go test ./internal/client/ui/...` asserts the embedded PNG is
a real PNG. Visual — launch the client and confirm the title-bar/window icon.

### Tests for User Story 2 (write FIRST; fails to compile until T012) ⚠️

- [X] T011 [P] [US2] Add `internal/client/ui/icon_test.go` (UNTAGGED — no `//go:build gui`) asserting `ui.AppIcon().Content()` is non-empty AND its first 8 bytes equal the PNG signature `0x89 0x50 0x4E 0x47 0x0D 0x0A 0x1A 0x0A`, and that `ui.AppIcon().Name()` is the expected stable string. It must run in the headless gate.

### Implementation for User Story 2

- [X] T012 [US2] Add `internal/client/ui/icon.go` (UNTAGGED so it compiles headlessly; imports only the cgo-free `fyne.io/fyne/v2` root package): `//go:embed icon.png` into a package-level `[]byte`, and export `func AppIcon() fyne.Resource { return fyne.NewStaticResource("lanweave-icon", iconPNG) }`. (Requires `icon.png` from T007.)
- [X] T013 [US2] In `cmd/lanweave-client/main.go` (the `//go:build gui` file), add `a.SetIcon(ui.AppIcon())` immediately after `a := app.NewWithID("com.lanweave.client")` and before `w := a.NewWindow(...)`. (`ui` is already imported.) Validated: `go vet -tags gui` and a full `CGO_ENABLED=1 go build -tags gui` both pass.
- [X] T014 [US2] Run the headless unit test in the real gate: `unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/ui/... -count=1'` — green (confirms T011 passes after T012, and that the untagged `ui` package builds without the GUI toolchain).
- [ ] T015 [US2] Manual verify (Windows; quickstart row 5): launch the client, confirm the window's own title-bar icon is the lanweave icon and matches the file icon from US1. **[MANUAL — deferred to a Windows desktop; cannot run headlessly]**

**Checkpoint**: The running window is branded; the only automated icon check (headless PNG
signature) is green.

---

## Phase 5: User Story 3 - The installer, uninstaller, and program list show the lanweave icon (Priority: P2)

**Goal**: The setup program, the uninstaller, and the Add/Remove Programs entry display the icon,
and the ARP entry also shows a version and publisher (FR-003/FR-004/FR-005).

**Independent Test**: Run the pipeline-built installer; the setup file/window, the ARP row (icon
+ version + publisher), and the uninstaller all show the branding.

### Implementation for User Story 3

- [X] T016 [P] [US3] Edit `packaging/windows/lanweave-client.nsi`: add top-level `Icon "icon.ico"` and `UninstallIcon "icon.ico"`; in the existing Add/Remove Programs registry block (`...Uninstall\${APPNAME}`) add `DisplayIcon "$INSTDIR\${EXE}"`, `DisplayVersion "${VERSION}"`, and `Publisher "lanweave"`. Guard `VERSION` so a bare `makensis` still compiles: `!ifndef VERSION` / `!define VERSION "0.0.0-dev"` / `!endif`.
- [X] T017 [P] [US3] Edit `.github/workflows/release.yml` Windows job: (a) provision the rasterization toolchain via the runner's MSYS2 — install `mingw-w64-x86_64-librsvg` (rsvg-convert) and `icoutils` (icotool) and add the MSYS2 `bin` dirs to `PATH` (windres already comes from the installed mingw); (b) run `make icons` before the `go build -tags gui ...` step so the released EXE/ICO are regenerated from the SVG (FR-010/SC-006); (c) `cp packaging/icon.ico packaging/windows/` alongside the existing `cp lanweave-client.exe wintun.dll packaging/windows/`; (d) pass the version to NSIS: change the makensis call to `"/c/Program Files (x86)/NSIS/makensis.exe" "/DVERSION=${VERSION}" lanweave-client.nsi`.
- [ ] T018 [US3] Manual verify (Windows; quickstart rows 1, 6, 7): run the setup EXE (its file/window show the icon), open Add/Remove Programs and confirm the lanweave row shows the icon + a non-empty version + publisher "lanweave", then start the uninstaller and confirm its icon. **[MANUAL — deferred to a Windows desktop; cannot run headlessly]**

**Checkpoint**: All three install-time surfaces are branded and the ARP entry is complete.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T019 [P] Append a "Revision (2026-06-06, feature 016)" note to Decision 1 of `specs/013-windows-client-elevation/research.md` recording that 016 introduces a `.syso` for the app icon (so the project-wide "no new build tooling" rationale no longer holds) while the runtime `runas` elevation path 013 chose is retained unchanged (research Decision 8).
- [X] T020 [P] If `docs/GUIDE.en.md` / `docs/GUIDE.zh.md` document building the Windows client, add a line that `make icons` must run first (it generates the `.syso` + `icon.png`); keep en/zh in sync. Skip if no such build section exists.
- [X] T021 Full validation per quickstart Definition of Done: `gofmt -l .` empty, `go vet ./...`, `staticcheck ./...`, `unshare -rUn bash -c 'ip link set lo up && go test ./...'`, and `CGO_ENABLED=0 go build ./cmd/lanweave-client` (stub) — all green.
- [ ] T022 Final ritual (separate commit): mark 016 ✅ in `docs/ROADMAP.md` (table row + the top status line), per the Constitution's ROADMAP-tracking gate. **[HELD — do after the manual Windows verify matrix (T010/T015/T018) passes, since SC-001/002/003 are visual-only]**

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001–T002)**: no dependencies.
- **Foundational (T003–T007)**: T004 depends on T003 (gen-icons.sh feeds `icon.rc` to windres); T007 depends on T003/T004/T006; T005 is independent `[P]`. **Blocks all user stories.**
- **US1 (Phase 3)**: depends on T007 (the `.syso` exists). Largely verification — the resource is produced in Foundational.
- **US2 (Phase 4)**: depends on T007 (`icon.png` exists). T011 (test) is written before T012 (impl) and fails to compile until then; T013 after T012; T014 after T012/T013; T015 manual after a build.
- **US3 (Phase 5)**: depends on T007 (`icon.ico` exists). T016 and T017 touch different files and are `[P]`; T018 manual after a pipeline build.
- **Polish (Phase 6)**: T019/T020 are `[P]` (independent files) and may proceed any time after the decision is settled; T021/T022 after all stories are green.

### Within Each User Story

- **US1**: T008 (naming guarantee) → T009 (stub regression) → T010 (manual).
- **US2**: T011 (failing test) → T012 (embed) → T013 (SetIcon) → T014 (headless gate) → T015 (manual).
- **US3**: T016 ∥ T017 → T018 (manual).

### Parallel Opportunities

- T005 `[P]` (gitignore) alongside T003/T004.
- US1, US2, US3 are independent once Foundational (T007) lands — different files; can proceed in parallel.
- T016 ∥ T017 (nsi vs yml); T019 ∥ T020 (013 research vs GUIDE docs).

---

## Parallel Example: after Foundational (T007) lands

```bash
# US2 first (it has the only automated test) — author the failing test, then implement:
Task: "T011 headless icon_test.go (PNG signature) in internal/client/ui/icon_test.go"
Task: "T012 //go:embed icon.png + AppIcon() in internal/client/ui/icon.go"

# US3 packaging edits in parallel (different files):
Task: "T016 NSIS Icon/UninstallIcon + ARP DisplayIcon/Version/Publisher in packaging/windows/lanweave-client.nsi"
Task: "T017 release.yml: MSYS2 toolchain + make icons + cp ico + makensis /DVERSION in .github/workflows/release.yml"

# Polish docs in parallel:
Task: "T019 013 research.md revision note"
Task: "T020 GUIDE build-step note (en/zh)"
```

---

## Implementation Strategy

### MVP First (the two P1 stories: US1 + US2)

1. Setup (T001–T002) → Foundational pipeline (T003–T007): assets generated, `make icons` works.
2. US1 (T008–T009 + manual T010): the EXE/file/shortcuts/taskbar are branded; stub build intact.
3. US2 (T011 failing → T012/T013 → T014 headless green + manual T015): the window is branded.
4. **STOP and VALIDATE**: headless test green; on Windows the file, shortcuts, taskbar, and
   window all show the icon. This is a shippable branded app.

### Incremental

5. US3 (T016 ∥ T017 → manual T018): installer/uninstaller/ARP branded; ARP shows version+publisher.
6. Polish: 013 revision (T019), GUIDE note (T020), full lint+test (T021).
7. Final ritual (separate commit): mark 016 ✅ in `docs/ROADMAP.md` (T022).

---

## Notes

- `[P]` = different files, no ordering dependency.
- No SQLite/nftables/WireGuard here → no integration tier, nothing mocked (Constitution II
  satisfied by scope). The single automated check is the headless PNG-signature unit test (T014).
- `internal/client/ui/icon.go` and `icon_test.go` are **untagged** on purpose: importing only
  `fyne.io/fyne/v2` core is cgo-free (verified), so the test runs in the headless
  `unshare -rUn go test ./...` gate. Do NOT add `//go:build gui` to them.
- The `.syso` is generated, never committed; `make icons` reproduces it from the committed ICO.
- Commit after each logical group; keep `internal/client/ui/icon.png` committed so every build
  (including the Linux test gate) compiles the embed target.
