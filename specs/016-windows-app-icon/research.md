# Research: Windows App Icon

**Feature**: 016-windows-app-icon | **Date**: 2026-06-06

The questions are *how* to put the lanweave brand mark on each of the five user-visible
surfaces (program file, running window, installer, uninstaller, Add/Remove Programs),
*how* to derive the raster icon forms from the one committed vector source, *where* to
hook generation into the build, and *how* to test any of it under the project's
"no-mock system boundaries" + headless-CI constraints. Decisions below resolve them;
none remain as NEEDS CLARIFICATION.

## Decision 1 — Embed the EXE icon with `windres`, reusing the MinGW toolchain already in CI

- **Decision**: Generate a Windows COFF resource object
  (`cmd/lanweave-client/resources_windows.syso`) from a tiny `.rc` file that references the
  multi-size `icon.ico`, using **`windres`** (the GNU binutils resource compiler that ships
  with MinGW). The Go linker auto-links any `*.syso` in the `main` package directory, so
  `go build -tags gui` picks it up with no build-flag change. The `_windows` filename suffix
  restricts linking to `GOOS=windows`, so the headless/untagged stub build never sees it.
- **Rationale**:
  - **No new tool in CI.** `release.yml` already runs `choco install mingw` for the cgo
    compiler; `windres` comes in that same package. We reuse a dependency that is already
    paid for rather than adding `goversioninfo`/`rsrc`/`rcedit`.
  - **`.syso` is the only way to give the *file itself* an icon.** Everything that inherits
    an executable's icon (Explorer, Start-menu/desktop shortcuts, taskbar, Alt+Tab) reads
    the embedded resource; there is no runtime API that brands the on-disk file. This
    satisfies FR-001 and the SC-001 surfaces that depend on the file's own icon.
  - **One ICO feeds four surfaces.** The same `icon.ico` is consumed by `windres` (EXE
    resource) and by NSIS `Icon`/`UninstallIcon` (Decision 5), so only one multi-size raster
    asset is introduced.
- **Overturns 013 Decision 1.** 013 (windows-client-elevation) chose runtime `runas`
  self-elevation specifically to **avoid** a `.syso`/resource toolchain, under the banner
  "No new build tooling" (Principle I). This feature deliberately reverses that constraint:
  a `.syso` is now required for the icon. The **runtime `runas` elevation path is kept
  unchanged** — we are not switching to a `requireAdministrator` manifest, so 013's
  friendly-decline behavior (FR-004) is preserved. A revision note is appended to
  `specs/013-windows-client-elevation/research.md` so the reversal is on record and the
  appearance of a `.syso` in `cmd/lanweave-client/` is explained for future readers.
- **Alternatives considered**:
  - *`goversioninfo` / `rsrc` (Go-native syso generators)*: each needs a fresh
    `go install` in CI. `windres` is already present, so they add a dependency for no gain.
  - *`rcedit` (post-build PE editor)*: pulls in a Node toolchain; over-heavy for a Go repo.
  - *Embedded `requireAdministrator` manifest*: 013 already rejected it (no friendly
    decline); out of scope here.

## Decision 2 — Rasterize with `rsvg-convert` (librsvg) + assemble with `icotool` (icoutils)

- **Decision**: A single script `packaging/scripts/gen-icons.sh` renders the SVG to PNGs at
  16/32/48/256 px with `rsvg-convert`, assembles them into a multi-size `packaging/icon.ico`
  with `icotool -c`, copies the 256 px PNG to `internal/client/ui/icon.png` for the Fyne
  window icon, and finally runs `windres` to produce the `.syso`. The script is `set -euo
  pipefail`, needs no network, and auto-detects the `windres` binary name
  (`x86_64-w64-mingw32-windres` on Linux, `windres` on Windows/MinGW).
- **Rationale**:
  - **librsvg renders SVG faithfully** (it is the GNOME SVG renderer); icon edges stay crisp
    at every size, satisfying FR-007/SC-001 legibility.
  - **icoutils' `icotool` is the standard multi-size ICO packer**; one command bundles all
    four PNGs into the single `.ico` the EXE and NSIS need.
  - **Same Unix tool names on every platform.** Using `rsvg-convert`/`icotool` (rather than
    PowerShell-only tooling) lets one `gen-icons.sh` run identically on a Linux dev box and
    on the Windows CI runner (via MSYS2 — Decision 6), so there is exactly one generation
    recipe to maintain.
- **ICO sizes 16/32/48/256**: the four Windows commonly requests (small list, default,
  large list, high-DPI/Explorer extra-large). 24/64/128 are interpolated by the OS from
  these and were dropped to keep the file small.
- **Alternatives considered**:
  - *ImageMagick `magick convert` for SVG→ICO*: its built-in SVG renderer is lower fidelity
    than librsvg unless a librsvg delegate is installed anyway; rejected for quality.
  - *Inkscape CLI*: high fidelity but a ~250 MB install for a one-line rasterize; too heavy.
  - *cairosvg + Pillow (Python)*: introduces a Python runtime into a Go repo; rejected.

## Decision 3 — Commit SVG + ICO + PNG; generate the `.syso` (gitignored)

- **Decision**: `packaging/icon.svg` (source), `packaging/icon.ico`, and
  `internal/client/ui/icon.png` are committed. `cmd/lanweave-client/resources_windows.syso`
  is **not** committed — it is added to `.gitignore` and produced by `make icons`.
- **Rationale**:
  - **The committed PNG must exist for the package to compile.** `internal/client/ui/icon.go`
    uses `//go:embed icon.png`; if the file were absent even a headless `go vet ./...` would
    fail. Committing it keeps every build (including the Linux test gate) self-contained.
  - **The committed ICO lets a local dev rebuild the `.syso` without re-rasterizing** (only
    MinGW needed), and lets reviewers preview the icon on GitHub.
  - **The `.syso` is an opaque linker artifact** (not even a viewable image); committing it
    would add an unreadable binary to every diff for zero review value. It is cheap to
    regenerate, so it is generated, not stored.
  - **No drift guard / no asset hashing** (per the design discussion): `rsvg-convert` output
    is not byte-stable across library versions, so a hash gate would fail spuriously on a
    tool bump. Asset freshness is enforced by Decision 6 (CI regenerates from the SVG) plus
    code review, not by a checksum.
- **Alternatives considered**:
  - *Commit the `.syso` too (zero CI generation step)*: rejected — unreadable binary in the
    tree, and the build would silently link a stale icon if the committed `.syso` drifted.
  - *Commit nothing, generate all three in CI only*: rejected — the headless test gate could
    not compile `internal/client/ui` (missing embed target) and local builds would break
    without the full raster toolchain.

## Decision 4 — Fyne window icon via an untagged `ui.AppIcon()` (headless-safe)

- **Decision**: Add `internal/client/ui/icon.go` (no build tag) that does `//go:embed
  icon.png` into a `[]byte` and exports `AppIcon() fyne.Resource` via
  `fyne.NewStaticResource`. In `cmd/lanweave-client/main.go` (the `gui` build) call
  `a.SetIcon(ui.AppIcon())` immediately after `app.NewWithID(...)` and before
  `a.NewWindow(...)`.
- **Rationale**:
  - **Importing only the `fyne.io/fyne/v2` root package is cgo-free.** Verified empirically:
    `CGO_ENABLED=0 go build ./internal/client/ui/` with **no** `gui` tag compiles cleanly
    (the GL/desktop toolchain lives in `fyne.io/fyne/v2/app` and the drivers, which we do not
    import here). So `icon.go` and its test live *outside* the `gui` tag and run in the
    headless `unshare -rUn go test ./...` gate — which is exactly what lets Decision 7's unit
    test execute in CI. The existing `ui` package already keeps an untagged `doc.go` so the
    package is valid headlessly; `icon.go` joins it.
  - **`SetIcon` is cross-platform and harmless off-Windows.** It also brands the window on a
    Linux/Mac GUI build; that is accepted (the client is Windows-first but the extra branding
    costs nothing and is consistent — FR-002).
  - **Single 256 px image suffices for Fyne**, which scales its window/taskbar icon from one
    resource; the multi-size set is only needed for the EXE/installer ICO.
- **Alternatives considered**:
  - *Put `AppIcon()` behind the `gui` tag*: then its test would also be `gui`-tagged and
    would **not** run in the headless gate — defeating the only automated check this feature
    can have. Rejected.
  - *Set a per-size Fyne icon set*: Fyne `SetIcon` takes one `fyne.Resource`; multi-size is
    not a Fyne concept. Unnecessary.

## Decision 5 — NSIS: installer/uninstaller icons + complete Add/Remove Programs metadata

- **Decision**: In `packaging/windows/lanweave-client.nsi` add `Icon "icon.ico"` and
  `UninstallIcon "icon.ico"` (installer & uninstaller EXE icons), and in the Add/Remove
  Programs registry block add `DisplayIcon "$INSTDIR\${EXE}"`, `DisplayVersion "${VERSION}"`,
  and `Publisher "lanweave"`. The script consumes `${VERSION}` via a `/DVERSION=...` define;
  `release.yml`'s `makensis` call passes `"/DVERSION=${VERSION}"`. The CI step that copies
  build outputs into `packaging/windows/` also copies `packaging/icon.ico` so the `.nsi`
  finds it next to itself.
- **Rationale**:
  - `Icon`/`UninstallIcon` are the documented NSIS directives for the generated
    setup/uninstall EXEs' own icons (FR-003/FR-004).
  - `DisplayIcon` pointing at the installed EXE reuses the resource embedded by Decision 1,
    so the Add/Remove Programs row shows the same mark with no extra asset (FR-005, SC-003).
  - `DisplayVersion` + `Publisher` complete the Add/Remove Programs entry; `${VERSION}` is
    already the single version source in `release.yml`, so this reuses it (FR-005, no new
    versioning).
- **Out of scope**: upgrading the Classic UI to Modern UI 2 (welcome/finish pages need a
  164×314 BMP that the square SVG cannot supply); that is a separate effort and is listed in
  the spec's "不做".
- **Alternatives considered**:
  - *Hard-code a version string in the `.nsi`*: would drift from the tag; rejected in favor
    of the `/DVERSION` define.

## Decision 6 — CI regenerates the full icon set from the SVG on the Windows runner

- **Decision**: The Windows build job in `release.yml` provisions the rasterization toolchain
  (in addition to the existing MinGW + NSIS) and runs `make icons` **before** `go build`, so
  the released installer/EXE always carry icons freshly derived from `packaging/icon.svg`
  rather than from the committed raster assets. Tooling is provisioned via the MSYS2
  environment that ships on the `windows-2022` image: `rsvg-convert` (from
  `mingw-w64-x86_64-librsvg`) and `icotool` (from `icoutils`); `windres` comes from the
  already-installed MinGW. The MSYS2 `bin` directories are added to `PATH` for the job.
- **Rationale**:
  - **Released assets can never ship a stale committed ICO/PNG** — they are regenerated from
    the authoritative SVG on every release (FR-010/SC-006). The committed rasters remain only
    a local-dev/headless-test convenience.
  - **One recipe, two platforms.** Because `gen-icons.sh` uses the Unix tool names, the same
    script the developer runs on Linux runs on the Windows runner through MSYS2; there is no
    separate Windows generation path to drift.
- **Cost / tradeoff (logged in Complexity Tracking)**: this adds an MSYS2 package-install
  step to the Windows job. The cheaper alternative — *commit the ICO/PNG and have CI run only
  `windres` to make the `.syso`* (zero new CI packages, since the committed ICO already
  exists) — was explicitly **not** chosen, in favor of guaranteeing freshness from source.
  The exact MSYS2 package names are confirmed at implementation; if `icoutils` is unavailable
  via MSYS2 on the runner, the fallback is ImageMagick (`choco install imagemagick`) for the
  ICO assembly step while keeping librsvg for rasterization.
- **Makefile wiring**: `make icons` runs `gen-icons.sh`. `make client` gains an `icons`
  prerequisite so a local client build cannot silently omit the `.syso` (the `client` target
  itself stays an informational/echo target on non-Windows, since the real GUI build is a
  native-Windows/CI `go build -tags gui`).
- **Alternatives considered**:
  - *Two Make targets (`icons` full vs `syso`-only) with CI using `syso`-only*: the minimal
    path (no librsvg/icoutils in CI). Not chosen — it ships whatever ICO is committed rather
    than regenerating from the SVG.

## Decision 7 — Testing: one headless unit test + a manual five-surface verify matrix

- **Decision**:
  - **Automated (headless, runs in the `unshare -rUn go test ./...` gate)**:
    `internal/client/ui/icon_test.go` (untagged) asserts `AppIcon().Content()` is non-empty
    and begins with the 8-byte PNG signature `89 50 4E 47 0D 0A 1A 0A`, and that
    `AppIcon().Name()` is stable. This is the only thing about the icon that is verifiable
    without a Windows desktop, and Decision 4 makes it runnable headlessly.
  - **Manual (Windows, recorded in `quickstart.md`)**: a five-row verify matrix — installed
    program file, Start-menu/desktop shortcuts, running taskbar/Alt+Tab, running window title
    bar, and the Add/Remove Programs entry — each checked by eye after a real
    `release.yml`-built installer is run on Windows. The Add/Remove row also confirms version
    + publisher are present.
- **Rationale**:
  - **Principle II is satisfied without mocking anything**: this feature touches no SQLite /
    nftables / WireGuard, so no system-boundary integration test applies; the one boundary it
    *can* test in pure Go (the embedded resource bytes) is tested for real.
  - **PE-resource visual correctness is inherently a Windows-desktop observation.** It joins
    the project's existing, already-accepted manual-verification exception for Windows GUI
    (009–014), logged again in Complexity Tracking.
  - **No automated PE-resource parser**: writing a PowerShell/.NET reader to assert the icon
    group exists in the built EXE is high-maintenance for low marginal confidence over the
    eyeball check; rejected (ROI).
- **Alternatives considered**:
  - *Assert `file packaging/icon.ico` / PNG magic inside `gen-icons.sh`*: redundant — the
    upstream `rsvg-convert`/`icotool`/`windres` already fail the build on bad input. Not
    added.

## Decision 8 — Record the 013 reversal where 013 lives

- **Decision**: Append a short "Revision (2026-06-06, feature 016)" note to
  `specs/013-windows-client-elevation/research.md` Decision 1, stating that 016 introduces a
  `.syso` for the application icon and therefore the "no new build tooling" rationale no
  longer holds project-wide, while the runtime `runas` elevation mechanism that 013 chose is
  retained unchanged.
- **Rationale**: keeps the historical decision honest in place, so a future reader of 013
  does not conclude the repo is still `.syso`-free. This is the single non-reproducible
  output of this feature — without it, project memory has a blind spot about why a generated
  resource object appears in the client `main` package.

## Resolved unknowns summary

| Unknown | Resolution |
|---|---|
| EXE-file icon mechanism | `windres` → `.syso`, reusing CI's MinGW (Decision 1) |
| SVG → raster toolchain | `rsvg-convert` + `icotool` (Decision 2) |
| Asset commit policy | commit SVG/ICO/PNG; gitignore + generate `.syso` (Decision 3) |
| Fyne window icon, headless-testable | untagged `ui.AppIcon()`, fyne core is cgo-free (Decision 4, verified) |
| Installer/uninstaller/ARP branding | NSIS `Icon`/`UninstallIcon` + `DisplayIcon`/`DisplayVersion`/`Publisher` (Decision 5) |
| Where generation hooks into the build | `make icons`; CI regenerates full set on Windows via MSYS2 (Decision 6) |
| Test strategy under Principle II | headless PNG-magic unit test + manual 5-surface matrix (Decision 7) |
| 013 "no new build tooling" conflict | reversed and recorded in 013's research.md (Decision 8) |
