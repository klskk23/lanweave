# Quickstart: Windows Client Administrator Elevation

**Feature**: 013-windows-client-elevation | **Date**: 2026-06-06

How to build and verify the elevation behavior. Steps A–B run on the dev host (headless);
steps C–F are the manual Windows acceptance (the documented Principle II GUI/OS exception).

## A. Headless unit tests (dev host)

```sh
go test ./internal/client/winelevate/...   # pure command-line quoting/join helper
go test ./...                              # full headless suite stays green
```

Expect: the `winelevate` argument-quoting tests pass; no regression elsewhere.

## B. Cross-compile the Windows syscall path (dev host)

```sh
GOOS=windows go vet ./internal/client/winelevate
GOOS=windows go build ./internal/client/winelevate
```

Expect: both succeed (the package is Fyne-free, so it cross-compiles without the GL toolchain),
proving `elevate_windows.go` compiles against `golang.org/x/sys/windows`.

## C. Build the client on Windows

```bat
set CGO_ENABLED=1
go build -tags gui -ldflags "-H windowsgui -X main.version=0.1.0" -o lanweave-client.exe .\cmd\lanweave-client
```

Place the matching `wintun.dll` (amd64) next to the exe (see INSTALL.md).

## D. US1 — normal launch, accept consent (happy path)

1. Sign in to Windows as a standard desktop user.
2. Launch the client the ordinary way (double-click the shortcut / exe — **not** right-click).
3. Expect: exactly one UAC consent prompt appears.
4. Accept it.
5. Expect: the app window opens; complete setup if needed; click Connect.
6. Verify: `ipconfig` shows the `100.127.x.y` adapter and the server (`100.127.0.1`) is
   reachable — with **no** manual "Run as administrator" step. (SC-001, SC-002, SC-005)

## E. US2 — decline consent (honest failure)

1. Launch the client unelevated as in D.
2. When the UAC prompt appears, **decline** it.
3. Expect: one human-readable message box stating administrator rights are required; the app
   then closes. The app never shows a "connected" state. (SC-003)

## F. US3 — already elevated (clean)

1. Right-click the exe/shortcut → **Run as administrator** (accept the prompt).
2. Expect: the app opens and works normally, with **no** second prompt and **no** visible
   relaunch. (SC-004)

## Pass criteria

- A and B are green on the dev host.
- D, E, F behave exactly as described on a clean Windows 10/11 machine.
- The previously seen "couldn't set up the network adapter" no longer occurs for a normal
  shortcut launch when the user consents.
