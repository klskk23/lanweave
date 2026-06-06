# Quickstart: Windows App Icon

**Feature**: 016-windows-app-icon

This feature has no runtime/API behavior to exercise — its acceptance is **visual**, on a
Windows desktop, plus one headless unit test. Use this document as the verification procedure
referenced by `spec.md` (US1–US3) and `plan.md` (Constitution II manual exception).

## Prerequisites

- A clean Windows 10/11 machine or VM (no prior lanweave install), to confirm no stale icon
  cache is masking the result.
- An installer built by the release pipeline (`lanweave-client-<ver>-setup.exe`), or a local
  build per "Local build" below.

## Regenerate the icon assets (when changing `packaging/icon.svg`)

```bash
# Linux dev box — one-time tool install:
sudo apt-get install -y librsvg2-bin icoutils binutils-mingw-w64
# Regenerate icon.ico, internal/client/ui/icon.png, and the .syso:
make icons
```

`make icons` must reproduce all raster forms from `packaging/icon.svg` with no hand-editing
(SC-004). Inspect the results:

```bash
file packaging/icon.ico                 # -> "MS Windows icon resource - 4 icons ..."
file internal/client/ui/icon.png        # -> "PNG image data, 256 x 256 ..."
file cmd/lanweave-client/resources_windows.syso   # -> COFF object (x86-64)
```

## Headless automated check (runs in CI test gate)

```bash
unshare -rUn bash -c 'ip link set lo up && go test ./internal/client/ui/...'
```

`internal/client/ui/icon_test.go` asserts `ui.AppIcon()` returns a non-empty resource whose
content begins with the PNG signature `89 50 4E 47 0D 0A 1A 0A`. This is the only icon
property checkable without a Windows desktop, and it runs untagged (no `gui`).

## Local build (Windows dev, optional)

```bash
make icons                                              # ensure .syso + png exist
go build -tags gui -ldflags "-H windowsgui" -o lanweave-client.exe ./cmd/lanweave-client
```

## Manual verify matrix (Windows desktop) — the acceptance gate

Run the installer, then check each surface by eye. All five MUST show the lanweave icon; the
Add/Remove Programs row MUST also show a version and publisher.

| # | Surface | How to check | Pass criterion | Spec ref |
|---|---------|--------------|----------------|----------|
| 1 | Installer EXE + window | View `…-setup.exe` in Explorer; launch it | File icon and setup window show the lanweave icon | US3 / FR-003 |
| 2 | Installed program file | After install, open `C:\Program Files\lanweave\`; view `lanweave-client.exe` | File shows the lanweave icon (not the default app icon) | US1 / FR-001 |
| 3 | Shortcuts | Open Start menu and Desktop; find the lanweave shortcuts | Both shortcuts show the lanweave icon | US1 / FR-001 |
| 4 | Running taskbar / Alt+Tab | Launch the client (accept the UAC prompt); look at the taskbar and press Alt+Tab | Running app is represented by the lanweave icon | US1 / FR-001 |
| 5 | Running window title bar | With the client open, look at the window's own (title-bar) icon | Window icon is the lanweave icon, matching the file | US2 / FR-002 |
| 6 | Add/Remove Programs | Settings → Apps → Installed apps (or `appwiz.cpl`); find lanweave | Entry shows the lanweave icon **and** a non-empty version **and** publisher "lanweave" | US3 / FR-005, SC-003 |
| 7 | Uninstaller | Start the uninstall from the entry in #6 | Uninstaller window/EXE shows the lanweave icon | US3 / FR-004 |

> Tip: if an icon looks stale after reinstalling, the Windows icon cache may be caching the
> old (blank) icon. Verify on a fresh VM or clear the icon cache before concluding a failure.

## Done when

- `make icons` regenerates every raster form from the SVG (SC-004) and the headless unit test
  is green (Decision 7).
- All seven rows of the manual matrix pass on a clean Windows install (SC-001–SC-003).
- A pipeline-built installer already carries the icons with no manual post-build step
  (SC-006), and the non-graphical stub build still compiles (SC-005:
  `CGO_ENABLED=0 go build ./cmd/lanweave-client` succeeds).
