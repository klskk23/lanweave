# Contract: Startup Elevation Behavior

**Feature**: 013-windows-client-elevation | **Date**: 2026-06-06

This is a behavioral contract for the desktop client's startup on Windows. It has no network or
RPC surface; the "interface" is what the user and OS observe when the app is launched.

## Entry point

`internal/client/winelevate.EnsureElevated()` — called as the **first** statement in
`cmd/lanweave-client` `main()` (under the `gui` build), before the Fyne app is created.

| Platform | Behavior |
|----------|----------|
| Windows | runs the elevation state machine below |
| non-Windows | no-op (returns immediately) |

## Guarantees

1. **Already elevated → transparent.** If the process is already running with administrator
   rights, `EnsureElevated()` returns immediately. No consent prompt is shown and the process is
   not relaunched. (FR-005, SC-004)
2. **Not elevated → one consent prompt.** If the process is not elevated, exactly one OS
   elevation consent prompt is raised (via relaunch with the `runas` verb). (FR-002, SC-002)
3. **Consent granted → elevated continuation.** When the user grants consent, the original
   process exits with code `0` and a new, elevated instance starts and proceeds into the normal
   experience with the privileges needed to create the network adapter. (FR-003, SC-001)
4. **Consent denied → honest exit.** When the user denies consent (or the relaunch fails), the
   client shows one human-readable native message and exits with code `1`. It does **not** open
   the normal UI and never presents a "connected" state. (FR-004, SC-003)
5. **Arguments preserved.** Any command-line arguments supplied to the original process
   (e.g. `--insecure`) are passed through to the elevated relaunch. (FR-006)
6. **No new window before elevation.** No Fyne window is created by an unelevated process; the
   elevation decision precedes UI construction. (Edge case: repeated double-clicks do not spawn
   stuck windows.)

## Observable post-conditions (acceptance)

| Launch | Prompt? | Relaunch? | End state |
|--------|---------|-----------|-----------|
| from shortcut, not elevated, user accepts | yes (1) | yes | app open & elevated; tunnel connect creates the adapter |
| from shortcut, not elevated, user declines | yes (1) | attempted | one message box; app exits; not connected |
| already elevated (e.g. right-click → Run as administrator) | no | no | app open & elevated; works normally |
| non-Windows build | no | no | normal startup, unchanged |

## Non-goals

- No attempt to create the network adapter without administrator rights.
- No persistence; nothing is written to disk by the elevation path.
- No change to the server or to any client behavior after startup.
