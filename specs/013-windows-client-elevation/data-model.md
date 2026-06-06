# Data Model: Windows Client Administrator Elevation

**Feature**: 013-windows-client-elevation | **Date**: 2026-06-06

No application data entities. This feature changes how the client process starts; the only
"model" is the startup elevation **state machine** and the small, pure value the relaunch
depends on.

## Startup elevation states (Windows)

| State | Condition | Transition / Outcome |
|-------|-----------|----------------------|
| `Elevated` | `IsElevated()` is true | proceed to normal startup (wizard or main panel) — no prompt, no relaunch |
| `NotElevated` | `IsElevated()` is false | relaunch self via `ShellExecute("runas")` |
| `ConsentGranted` | relaunch succeeded | original process exits `0`; the new elevated instance starts in `Elevated` |
| `ConsentDenied` | `ShellExecute` returns an error | show one native message box, exit `1` (never reach the UI) |

```text
launch
  │
  ▼
IsElevated()? ──true──► proceed (normal UI)
  │false
  ▼
ShellExecute("runas", self, args)
  │success            │error
  ▼                   ▼
exit(0)            MessageBox(...) ; exit(1)
(elevated child
 takes over)
```

On non-Windows builds the entry point is a no-op: control falls straight through to the normal
startup with no state machine.

## Pure value: relaunch command line

The only computed value is the command-line string passed to the elevated relaunch, derived
from the current process arguments.

| Field | Source | Rule |
|-------|--------|------|
| executable path | `os.Executable()` | the on-disk path of the running binary |
| argument string | `os.Args[1:]` | each argument re-quoted, then space-joined into one command line |

**Quoting rule** (the unit-tested function): an argument is emitted verbatim when it contains no
whitespace or double-quote; otherwise it is wrapped in double quotes with any embedded double
quotes escaped. An empty argument list yields an empty string (relaunch with no extra args).

## Invariants

- The elevation check and any relaunch happen **before** the Fyne app/window is constructed
  (no window is shown by an unelevated process).
- An already-elevated process never relaunches (no loop): the `IsElevated()` guard is the loop
  break.
- The relaunch re-invokes the same executable with the same user-supplied arguments (FR-006).
- Declining elevation never yields a "connected" or otherwise success-looking UI (FR-004).
- Non-Windows behavior is unchanged (the no-op stub).
