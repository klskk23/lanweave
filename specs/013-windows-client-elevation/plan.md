# Implementation Plan: Windows Client Administrator Elevation

**Branch**: `013-windows-client-elevation` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/013-windows-client-elevation/spec.md`

## Summary

The Windows desktop client must run with administrator rights so it can create the WinTun
adapter; today a normal shortcut launch runs unelevated and connecting fails with
`ErrAdapter` ("couldn't set up the network adapter"). The fix: at startup, before any UI is
created, the client checks whether it is already elevated and, if not, relaunches itself via
the OS elevation prompt (`ShellExecute` with the `runas` verb) and exits. If the user grants
consent, the elevated instance proceeds normally; if the user declines, the client shows one
human-readable native message and exits without presenting a misleading "connected" state. The
elevation logic lives in a new, Fyne-free package `internal/client/winelevate` so its pure
parts are unit-tested headlessly and the Windows syscall path is compile-verified with
`GOOS=windows`; the live UAC prompt + adapter creation are validated manually on Windows
(documented Principle II GUI/OS exception, consistent with features 009–012). The 012 comment
and `INSTALL.md` that wrongly claimed the *installer* grants the running app its elevation are
corrected.

## Technical Context

**Language/Version**: Go 1.26 (module `lanweave`)

**Primary Dependencies**: `golang.org/x/sys/windows` (already a direct dependency — the keyring
DPAPI backend uses it; no new dependency added). Standard library only otherwise. The WinTun /
`wireguard-go` adapter path (feature 010) is the unchanged consumer of the granted privilege.

**Storage**: N/A (no persistent state; this changes how the process is launched)

**Testing**:
- Unit (headless, real, no mocks): pure command-line-argument quoting/join helper in
  `internal/client/winelevate` — runs under plain `go test ./...`.
- Compile verification: `GOOS=windows go vet ./internal/client/winelevate` ensures the Windows
  syscall path builds on the dev host (the package is Fyne-free, so it cross-compiles).
- Manual acceptance (Windows, documented exception): the UAC consent prompt, accept→connect,
  decline→honest exit, and already-elevated→clean launch per `quickstart.md`.

**Target Platform**: Windows 10/11 desktop (the elevation behavior). Other desktop builds
(Linux, used for dev/test) compile a no-op and are unaffected.

**Project Type**: desktop-app (Windows client)

**Performance Goals**: Startup elevation check is a single process-token query (sub-millisecond)
plus, when unelevated, one re-launch. No constitution Principle IV budget is affected (this is
client launch, not a server or API path).

**Constraints**: The elevation decision MUST run before the Fyne app/window is created (so an
unelevated process never flashes a window). MUST add no new module dependency. Headless and
Linux `-tags gui` builds MUST stay green.

**Scale/Scope**: One new ~4-file package (`internal/client/winelevate`), one call site in
`cmd/lanweave-client/main.go`, and documentation/comment corrections (`main.go`, `INSTALL.md`,
the NSIS script header).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS. New package `internal/client/winelevate` has one named
  responsibility (obtain administrator rights on Windows at startup). No premature abstraction
  (one call site). `gofmt`/`go vet`/`staticcheck` clean; errors handled as values; no panics;
  reuses the existing `x/sys/windows` dependency. The 012 inaccurate comment is fixed (comments
  explain WHY).
- **II. Testing Standards (NON-NEGOTIABLE)** — PASS WITH DOCUMENTED EXCEPTION. The pure logic
  (building the relaunch command line) is unit-tested headlessly against the real implementation
  (no mocks). The Windows-only token check + `ShellExecute`/`MessageBox` shim is compile-
  verified with `GOOS=windows` and validated manually per `quickstart.md`. The live UAC consent
  dialog and WinTun adapter creation cannot run in headless CI (no Windows runner, and a UAC
  consent prompt is not scriptable); this is the same GUI/OS exception accepted for 009–012,
  recorded in Complexity Tracking. The regression evidence (unelevated launch fails to create
  the adapter; after the fix a consented launch succeeds) is the manual Windows acceptance.
- **III. User Experience Consistency** — PASS. The decline path surfaces one human-readable
  native message ("lanweave needs administrator rights…"), not a stack trace or Go error chain,
  and never shows a misleading connected state — consistent with the constitution's "errors
  written for humans" rule. The happy path is a single OS-standard consent prompt.
- **IV. Performance Requirements** — PASS. One token query at startup; one relaunch at most. No
  server/API/kernel budget is touched.
- **Security & Operational Discipline** — PASS. No secrets are logged by the elevation path. The
  client legitimately requires administrator rights to create the kernel virtual-network adapter
  (the Windows analogue of the server's documented root + `CAP_NET_ADMIN` need); Windows exposes
  no finer-grained capability for WinTun adapter creation in this model. The relaunch re-invokes
  the same on-disk binary with the same arguments — no new attack surface, no new privilege
  beyond what adapter creation already required.
- **Single-instance assumption** — N/A (client-side; no shared server state).

## Project Structure

### Documentation (this feature)

```text
specs/013-windows-client-elevation/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output (no data entities; states/transitions)
├── quickstart.md        # Phase 1 output (manual Windows acceptance)
├── contracts/
│   └── startup-elevation.md   # Startup elevation behavior contract
├── checklists/
│   └── requirements.md  # Spec quality checklist (from /speckit-specify)
└── tasks.md             # /speckit-tasks output (NOT created here)
```

### Source Code (repository root)

```text
internal/client/winelevate/
├── args.go              # pure: build the relaunch command-line string from os.Args (no build tag)
├── args_test.go         # headless unit tests for arg quoting/join (no build tag)
├── elevate_windows.go   # //go:build windows — EnsureElevated(): token check + runas relaunch + decline message
└── elevate_other.go     # //go:build !windows — EnsureElevated() no-op (keeps Linux/headless green)

cmd/lanweave-client/
└── main.go              # //go:build gui — calls winelevate.EnsureElevated() first in main(); fixed comment

# Documentation / packaging corrections (FR-008)
INSTALL.md                              # Windows section: app self-elevates via UAC on launch
packaging/windows/lanweave-client.nsi   # header comment: app self-elevates (installer stays admin for the driver)
```

**Structure Decision**: A dedicated Fyne-free package `internal/client/winelevate` isolates the
elevation concern so the pure command-line logic is testable under plain `go test ./...` and the
Windows syscall path can be cross-compiled/vetted on the dev host without the GUI toolchain.
`cmd/lanweave-client/main.go` calls `winelevate.EnsureElevated()` as the first statement in
`main()`, before constructing the Fyne app, so an unelevated process relaunches and exits before
any window appears. The non-Windows stub keeps headless and Linux `-tags gui` builds green.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Principle II: the UAC consent prompt + WinTun adapter creation (US1–US3 acceptance, SC-001..005) are validated **manually on Windows**, not in automated CI | There is no Windows CI runner, and a UAC elevation consent dialog cannot be driven headlessly or scripted; adapter creation requires real administrator rights on real Windows | A mocked elevation/adapter would assert nothing about the real OS behavior (forbidden by Principle II); the testable pure logic *is* unit-tested, and the Windows syscall path is compile-verified with `GOOS=windows`. Same accepted GUI/OS pattern as features 009–012. |
