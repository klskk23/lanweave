# Quickstart: Windows Client Main Panel

**Feature**: 011-windows-client-main-panel | **Date**: 2026-06-06

Validates that the panel shows the user's devices/zones/members and performs create/join/
leave and owner operations end to end. Builds on 009 (setup + secure store) and the existing
server (002/004/005/006/007).

## Automated checks (build host)

```bash
# apiclient zone error mapping + the Fyne-free controller (view assembly, this-machine, session).
go test ./internal/client/apiclient/... ./internal/client/panel/...

# Real server end-to-end: controller drives create/join/leave/owner/members/online.
unshare -rUn go test ./internal/client/panel/... -run Integration
```

Unprivileged hosts skip the privileged integration via `testutil.RequireNetAdmin`. The Fyne
panel builds/validates on the desktop toolchain.

## Scenario A — see my network (US1, automated, privileged)

1. Stand up a real server; create a user with two devices (one is "this machine" per the
   setup record) in some zones.
2. The controller's `Devices()` lists both devices with this machine marked and online state;
   `Zones()` lists the device's zones; `Members(zone)` shows every member's name/owner/address.

## Scenario B — create / join / leave (US2, automated, privileged)

`CreateZone("team","pw")` → appears in `Zones()` as owner. A second user's device `JoinZone`
by name + password → appears as a member (and shows in the first user's `Members`). `LeaveZone`
→ membership gone. A wrong password → `ErrZonePasswordWrong`; a duplicate name →
`ErrZoneNameTaken`.

## Scenario C — owner controls (US3, automated, privileged)

On an owned zone: `ChangePassword` (the old password no longer lets a new device join),
`KickMember` (the member leaves `Members`), `DeleteZone` (it disappears). On a zone owned by
someone else, `IsOwner` is false (the view hides the controls) and a forced owner call →
`ErrNotOwner`.

## Scenario D — session reuse / prompt (US4, automated, non-privileged)

With a cached valid token, `LoadSession()` reports no sign-in needed. With an absent/expired
token, it reports sign-in needed; `SignIn(user, pass)` caches a new token. A 401 on an authed
call surfaces `ErrSessionExpired`.

## Scenario E — the panel on Windows (manual, target OS)

Open the built client: the panel shows the top status (IP, connect switch, last-seen), the
"My nodes" and "My zones" tabs; create a zone, join another's zone, leave, and (as owner)
change password / kick / delete — each with a confirmation for destructive actions and
progress feedback — entirely through the UI, no command line. Verify a member sees all
members' names + addresses. 

## Success

- Scenarios A–D pass automatically (A/B/C privileged; D non-privileged).
- Scenario E passes by manual inspection on Windows (the documented GUI exception).
