# Research: Windows Client Administrator Elevation

**Feature**: 013-windows-client-elevation | **Date**: 2026-06-06

The questions are about *how* to obtain administrator rights for the client, how to detect the
current elevation, how to handle a declined prompt, and how to test any of this without a
Windows CI runner. Decisions below resolve them; none remain as NEEDS CLARIFICATION.

## Decision 1 — Elevate at runtime by relaunching via `runas`, not via an embedded manifest

- **Decision**: On Windows, at the very start of `main()`, check whether the process is already
  elevated; if not, relaunch the same executable with the OS elevation verb (`ShellExecute` with
  `"runas"`) and exit the original process. Do **not** embed a `requireAdministrator` application
  manifest.
- **Rationale**:
  - **No new build tooling.** A manifest must be embedded as a `.syso` resource produced by
    `windres`/`goversioninfo`, which adds a build step and a generated binary artifact. Runtime
    self-elevation is pure Go and works with the project's existing `go build -tags gui` flow
    unchanged (Principle I: small, obvious, reversible).
  - **No new dependency.** It uses `golang.org/x/sys/windows`, already a direct dependency (the
    keyring DPAPI backend uses it).
  - **Testable seam.** The decision/relaunch-argument logic is plain Go and unit-testable
    headlessly; a manifest is an opaque OS behavior with nothing to unit-test.
  - **Honest decline.** With self-relaunch we can detect a declined prompt and show one
    human-readable message before exiting (FR-004 / US2). A pure manifest, when declined, just
    makes the process fail to start with a generic OS error — no chance to be friendly.
- **Alternatives considered**:
  - *Embedded `requireAdministrator` manifest (`.syso`)*: the canonical Windows approach and it
    elevates the original process without a relaunch, but it needs a resource toolchain + a
    committed/generated `.syso`, and offers no friendly decline path. Rejected for v1 to keep the
    build dependency-free and the logic testable.
  - *Privileged background helper service + unprivileged UI*: proper least-privilege split, but a
    large architectural change. Deferred (Out of Scope in spec).
  - *Leave it manual ("Run as administrator")*: that is the broken status quo this feature fixes.
    Rejected.

## Decision 2 — Detect elevation with `GetCurrentProcessToken().IsElevated()`

- **Decision**: Determine whether to relaunch using
  `windows.GetCurrentProcessToken().IsElevated()` (verified present in `golang.org/x/sys/windows`
  v0.45.0, `security_windows.go`). The pseudo-token needs no `OpenProcessToken`/`Close`.
- **Rationale**: It is the library's purpose-built UAC-elevation query; one call, no handle
  lifecycle. If it ever errored it returns `false`, which conservatively triggers the relaunch
  (worst case: a redundant prompt, never a silent unprivileged run).
- **Alternatives considered**:
  - *`OpenProcessToken` + `GetTokenInformation(TokenElevation)` by hand*: equivalent but more
    code and a handle to close. Rejected — `IsElevated()` wraps exactly this.
  - *Check membership in the Administrators SID*: answers a different question (could-elevate vs
    is-elevated). Rejected.

## Decision 3 — Relaunch with `ShellExecute("runas", …)`, preserving arguments

- **Decision**: Relaunch via
  `windows.ShellExecute(0, ptr("runas"), ptr(exePath), ptr(argLine), nil, windows.SW_SHOWNORMAL)`,
  where `exePath` is `os.Executable()` and `argLine` is the current process arguments
  (`os.Args[1:]`) re-quoted into a single command-line string (FR-006). On success, call
  `os.Exit(0)` so only the elevated instance continues.
- **Rationale**: `runas` is the documented verb that raises the standard UAC consent dialog.
  `SW_SHOWNORMAL` (=1) shows the relaunched window normally. Re-quoting argv preserves the
  advanced `--insecure` flag and any future flags across the relaunch.
- **Argument quoting**: the only current flag is the boolean `--insecure`, but the join helper
  quotes generically (wrap an argument containing whitespace or quotes in double quotes, escaping
  embedded quotes) so the seam is correct and unit-testable. This is the pure function tested
  headlessly.
- **Alternatives considered**:
  - *`os/exec` with a `runas` attribute*: Go's `exec` cannot raise a UAC prompt;
    elevation requires the ShellExecute/`runas` path. Rejected.

## Decision 4 — Declined or failed elevation: one native message, then exit

- **Decision**: If `ShellExecute` returns an error (the user declined the UAC prompt, or the
  relaunch otherwise failed), show a single native message box
  (`windows.MessageBox(0, …, MB_OK|MB_ICONERROR)`) stating the app needs administrator rights to
  create the network adapter and is closing, then `os.Exit(1)`. Do **not** fall through into the
  normal (unelevated, non-functional) UI.
- **Rationale**: Satisfies FR-004 / US2 — the outcome is understandable and the app never
  presents a misleading "connected" state. A native message box needs no Fyne window (the Fyne
  app hasn't been created yet at this point). `MessageBox` and `MB_OK`/`MB_ICONERROR` are present
  in `x/sys/windows` v0.45.0.
- **Alternatives considered**:
  - *Continue unelevated and let the existing in-app "needs administrator rights" message show on
    connect*: leaves a non-functional window open and is less clear at the moment of decline.
    Rejected as the default (the in-app `ErrAdapter`/`ErrElevationDenied` messages remain as a
    backstop for any path that still reaches the UI).
  - *Silent exit*: technically not misleading, but US2 wants an *understandable* outcome.
    Rejected in favor of one message.

## Decision 5 — Testing without a Windows runner

- **Decision**: Three layers. (1) Headless unit tests for the pure argument-quoting/join helper
  via plain `go test ./...` (real code, no mocks). (2) Cross-compile/vet the Windows syscall path
  with `GOOS=windows go vet ./internal/client/winelevate` on the dev host — possible because the
  package is Fyne-free. (3) Manual Windows acceptance per `quickstart.md` for the UAC prompt,
  accept→connect, decline→message+exit, and already-elevated→clean launch.
- **Rationale**: Honors Principle II where it is mechanizable (the pure logic is genuinely
  tested; the syscall path genuinely compiles for the target) and records the irreducible manual
  remainder — a UAC consent dialog and real WinTun adapter creation cannot be driven headlessly —
  as the same documented GUI/OS exception accepted for 009–012. No system boundary is mocked.
- **Alternatives considered**:
  - *Spin up a Windows CI runner with a UAC automation harness*: out of proportion for one
    startup behavior and still cannot assert a real human-consent dialog deterministically.
    Deferred.

## Verified API surface (golang.org/x/sys/windows v0.45.0)

| Symbol | Location | Use |
|--------|----------|-----|
| `GetCurrentProcessToken() Token` | `security_windows.go` | obtain the current process token (pseudo-handle) |
| `(Token).IsElevated() bool` | `security_windows.go` | UAC elevation check |
| `ShellExecute(hwnd, verb, file, args, cwd *uint16, showCmd int32) error` | `zsyscall_windows.go` | relaunch with `runas` |
| `MessageBox(hwnd HWND, text, caption *uint16, boxtype uint32) (int32, error)` | `zsyscall_windows.go` | decline message |
| `SW_SHOWNORMAL = 1` | `types_windows.go` | show command for the relaunched window |
| `MB_OK`, `MB_ICONERROR` | `types_windows.go` | message-box style |

## Resolved unknowns summary

| Topic | Resolution |
|-------|------------|
| Elevation mechanism | Runtime self-relaunch via `ShellExecute("runas")` (no manifest, no new build tooling) |
| Elevation detection | `windows.GetCurrentProcessToken().IsElevated()` |
| Argument preservation | Re-quote `os.Args[1:]` into the relaunch command line (pure, unit-tested) |
| Declined prompt | One native `MessageBox`, then `os.Exit(1)` — never a misleading UI |
| Already elevated | `IsElevated()` true → return immediately; no prompt, no relaunch |
| Testing | Headless unit (pure args) + `GOOS=windows` vet + manual Windows acceptance (documented exception) |
| New dependency | None (`x/sys/windows` already present) |
