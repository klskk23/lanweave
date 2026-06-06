# Implementation Plan: Windows App Icon

**Branch**: `016-windows-app-icon` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/016-windows-app-icon/spec.md`

## Summary

Brand the five user-visible surfaces of the Windows client with the lanweave mark from the
single committed vector `packaging/icon.svg`. A generation script rasterizes the SVG into a
multi-size `icon.ico` and a 256 px `icon.png`, then compiles a Windows resource object the Go
linker embeds into the client EXE (so the file, shortcuts, taskbar, and Alt+Tab inherit the
icon). The Fyne build sets the same mark as the running window's icon via an untagged,
headless-testable `ui.AppIcon()`. The NSIS script gains installer/uninstaller icons and a
complete Add/Remove Programs entry (icon + version + publisher). CI regenerates the full icon
set from the SVG before building so released artifacts are always branded from source. This
deliberately reverses 013's "no new build tooling" decision (a `.syso` is now required); the
runtime `runas` elevation 013 chose is kept, and the reversal is recorded in 013's research.

## Technical Context

**Language/Version**: Go 1.26.2 (module `lanweave`)

**Primary Dependencies**: client GUI: Fyne v2.7.4 (behind `//go:build gui`); the new
`ui.AppIcon()` imports only the **cgo-free** `fyne.io/fyne/v2` root package. Build/asset
toolchain (not Go deps): `rsvg-convert` (librsvg), `icotool` (icoutils), `windres` (MinGW
binutils — already in CI), NSIS (already in CI).

**Storage**: None. This feature adds no database, no protocol, no API. SQLite/nftables/
WireGuard are untouched.

**Testing**: `go test` — one headless unit test (`internal/client/ui/icon_test.go`, untagged)
asserting the embedded PNG's signature, run inside the existing `unshare -rUn go test ./...`
gate. Visual correctness of the embedded EXE/installer icons is verified manually on Windows
per `quickstart.md` (the project's standing Windows-GUI manual exception, 009–014).

**Target Platform**: Windows 10/11 client (icon surfaces) + Linux CI runner (asset gen, tests)
+ Windows CI runner (regenerate + build + package).

**Project Type**: Client/server Go monorepo; this feature touches only the client packaging,
the client `main`, and the `ui` package.

**Performance Goals**: N/A — no runtime hot path changes. (Embedding a resource and calling
`SetIcon` once at startup has no measurable effect on Constitution IV budgets.)

**Constraints**: The committed `internal/client/ui/icon.png` MUST exist whenever the package
compiles (it is an `//go:embed` target) — so the PNG is generated/committed together with
`icon.go`. The `.syso` is `GOOS=windows`-only (filename suffix) so the headless stub build is
unaffected. No new mock is introduced (none is possible or needed here).

**Scale/Scope**: Small. New: `gen-icons.sh`, `icon.rc`, `ui/icon.go`, `ui/icon_test.go`, two
committed raster assets, one gitignored `.syso`. Edited: `Makefile`, `.gitignore`,
`lanweave-client.nsi`, `release.yml`, `cmd/lanweave-client/main.go`, plus the 013 research
revision note. No server code, no `pkg/protocol` change.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS (with one recorded reversal). The change is small and
  reversible: one embed-and-set call on the client, three NSIS lines, one generation script,
  one resource file. No new Go dependency, no new package (the `ui` package gains one untagged
  file). SQLite remains the single source of truth (untouched); the `.syso` is a derived build
  artifact, not runtime state. **Reversal**: this overturns 013 Decision 1's "no new build
  tooling" stance by introducing a `windres` step + a generated `.syso`. It reuses MinGW
  already present in CI (no *new* tool download), and is recorded in 013's research.md per
  Decision 8 and logged in Complexity Tracking below.
- **II. Testing Standards (NON-NEGOTIABLE)** — PASS. This feature crosses **no** process/kernel
  boundary (no SQLite/nftables/WireGuard), so the mandatory integration tier does not apply and
  nothing is mocked. The one pure-Go boundary — the embedded icon bytes — has a real unit test
  that runs in the headless gate (feasibility verified: `fyne` core compiles with
  `CGO_ENABLED=0` and no `gui` tag). The remaining surfaces are PE-resource/installer visuals
  that are only observable on a Windows desktop; they are covered by the `quickstart.md` manual
  matrix, the same accepted exception used since 009. Each spec user story maps to a verify
  row (US1→file/shortcuts/taskbar rows, US2→window-title row + the unit test, US3→installer/
  uninstaller/ARP rows).
- **III. User Experience Consistency** — PASS. Branding the window, file, installer, and
  Add/Remove Programs entry makes the client look like one finished application across all its
  touch points (the principle's intent). No new operation, dialog, status, or destructive
  action is introduced; nothing about existing flows changes.
- **IV. Performance Requirements** — PASS / N/A. No server path, no new latency; `SetIcon` is a
  one-time startup call.
- **Security & Operational Discipline** — PASS. No secrets, no network input, no crypto, no
  privilege change (the icon does not alter the existing elevation path). The added
  build-toolchain packages (librsvg/icoutils) run only at build time on CI, never in the
  shipped product. `DESIGN.md §11` accepted-risks register is unaffected.

No blocking violations. One reversal and one standing exception are recorded in Complexity
Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/016-windows-app-icon/
├── plan.md              # This file
├── research.md          # Phase 0 output (8 decisions)
├── quickstart.md        # Phase 1 output — the manual 5-surface verify matrix
├── checklists/
│   └── requirements.md  # spec quality checklist (from /speckit-specify)
└── tasks.md             # Phase 2 output (/speckit-tasks — not created here)
```

> **No `data-model.md` and no `contracts/`**: this feature introduces no entities, no
> persistent state, and no external interface (API/CLI/protocol). Per the plan workflow's
> "skip if purely internal" rule, both are intentionally omitted; `quickstart.md` carries the
> only acceptance procedure (the manual verify matrix).

### Source Code (repository root)

```text
packaging/
├── icon.svg                          # source (first commit on this branch)
├── icon.ico                          # NEW, committed — 16/32/48/256, feeds windres + NSIS
├── scripts/
│   └── gen-icons.sh                  # NEW — rsvg-convert → PNGs → icotool → ICO; copy 256 PNG; windres → .syso
└── windows/
    ├── lanweave-client.nsi           # EDIT — Icon/UninstallIcon + DisplayIcon/DisplayVersion/Publisher; /DVERSION
    └── icon.rc                       # NEW — `IDI_ICON1 ICON "packaging/icon.ico"` (windres input)

cmd/lanweave-client/
├── main.go                           # EDIT (gui) — a.SetIcon(ui.AppIcon()) after NewWithID, before NewWindow
├── main_stub.go                      # unchanged
└── resources_windows.syso            # NEW, generated, GITIGNORED — linked automatically on GOOS=windows

internal/client/ui/
├── doc.go                            # unchanged (already untagged, keeps pkg valid headlessly)
├── icon.go                           # NEW, UNTAGGED — //go:embed icon.png; AppIcon() fyne.Resource
├── icon.png                          # NEW, committed — 256×256, the embed target
├── icon_test.go                      # NEW, UNTAGGED — PNG-signature assertion (headless gate)
├── panel.go                          # unchanged (//go:build gui)
└── wizard.go                         # unchanged (//go:build gui)

Makefile                              # EDIT — `icons` target (gen-icons.sh); `client: icons` prerequisite
.gitignore                            # EDIT — ignore cmd/lanweave-client/resources_windows.syso
.github/workflows/release.yml         # EDIT — Windows job: install librsvg/icoutils (MSYS2); `make icons` before go build; makensis /DVERSION
specs/013-windows-client-elevation/
└── research.md                       # EDIT — append 016 revision note to Decision 1
docs/ROADMAP.md                       # already edited on this branch (016 row + detail section)
```

**Structure Decision**: Reuse the existing layout. Assets live under `packaging/` (alongside
the existing `icon.svg` and `windows/lanweave-client.nsi`); the only code addition is the
untagged `internal/client/ui/icon.go` (+ its embed target and test) plus one line in the
`gui`-tagged `cmd/lanweave-client/main.go`. The `.syso` must sit in the `main` package
directory (`cmd/lanweave-client/`) because the Go linker only auto-links `*.syso` from there,
and carries the `_windows` suffix so the headless stub build ignores it. Keeping `AppIcon()`
untagged is what allows its unit test to run in the headless `unshare -rUn go test ./...` gate;
this was verified by compiling the `ui` package with `CGO_ENABLED=0` and no `gui` tag.

## Complexity Tracking

| Violation / Exception | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Reverses 013 Decision 1 ("no new build tooling") by adding a `windres` step + generated `.syso` | The on-disk EXE file's icon (and everything inheriting it: shortcuts, taskbar, Alt+Tab) can only come from an embedded PE resource; there is no runtime API for it (FR-001) | A manifest-free, syso-free repo cannot brand the executable file at all; runtime `SetIcon` only reaches the window, not the file. The cost is contained by reusing MinGW already in CI and is recorded in 013's research.md |
| EXE/installer/ARP icon correctness verified **manually** on Windows, not by an automated test | PE-resource and installer-window visuals are observable only on a Windows desktop; CI has no such observation point | An automated PE-resource parser is high-maintenance for marginal confidence over the eyeball check; the project already runs a standing manual Windows-GUI exception (009–014) |
| CI regenerates the full icon set on the Windows runner (adds an MSYS2 librsvg/icoutils install) instead of only re-linking the committed ICO | Guarantees released installer/EXE icons are derived from the authoritative SVG every release, never from a possibly-stale committed raster (FR-010/SC-006) | The `windres`-only path (commit ICO, CI adds no packages) ships whatever ICO is committed; chosen against in favor of source-of-truth freshness. Fallback to ImageMagick for ICO assembly if MSYS2 `icoutils` is unavailable |
