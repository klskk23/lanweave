# Quickstart: Windows Client Skeleton & First-Run Wizard

**Feature**: 009-windows-client-skeleton | **Date**: 2026-06-05

Validates that a fresh client run completes onboarding (server → account → device name →
registered + remembered) and that a relaunch skips the wizard. The tunnel and full panel
are later features.

## Automated checks (build host)

```bash
# Fyne-free onboarding core: apiclient error mapping, controller flow, state round-trip.
go test ./internal/client/apiclient/... ./internal/client/onboard/... ./internal/client/state/...

# End-to-end onboarding against a REAL server (real SQLite + WireGuard + nftables):
unshare -rUn go test ./internal/client/onboard/... -run Integration
```

Unprivileged hosts skip the privileged integration via `testutil.RequireNetAdmin`. The
Fyne UI packages (`internal/client/ui`, `cmd/lanweave-client`) build on the desktop/GL
toolchain, not necessarily on a bare CI host.

## Scenario A — new user onboards (US1, automated against a real server)

The privileged integration test performs this without a GUI:

1. Start a real server (in-process `api.NewRouter` + real store/wg/nft over TLS); mint an
   invite (admin) for the test.
2. Run the onboarding controller: server URL → create account (invite + username +
   password) → device name → provision.
3. Assert: the server has the device registered to the user with an assigned `100.127.x.y`
   address; the local state record is written with the address + server public key +
   endpoint + network; the fake vault holds the private key.

## Scenario B — existing user onboards (US1, sign-in path)

Same as A but the account already exists and the controller signs in (username/password)
instead of creating an account. Assert the device is registered and the record written.

## Scenario C — relaunch skips the wizard (US2, automated)

With a state record present (from A/B), constructing the app's startup decision returns
"go to home" (wizard not shown); with the record removed, it returns "run wizard".

## Scenario D — failures and cancel (US3, automated)

- Wrong password → `ErrAuthFailed`; invalid/used invite → `ErrInviteInvalid`; duplicate
  device name → `ErrNodeNameTaken`; unreachable server → `ErrUnreachable` — each a clear
  message, controller stays recoverable.
- Cancel mid-wizard → the vault key and any partial state record are gone (fresh next run).
- Partial failure: device registered but state write fails → retry recovers via the
  pubkey-idempotent path with no duplicate device.

## Scenario E — Windows GUI + DPAPI (manual, target OS)

On a clean Windows machine: launch the built client → walk the wizard visually (Back/Esc
work, progress shows on each network step, no "skip certificate" control) → finish →
confirm `state.json` under `%LOCALAPPDATA%\lanweave\` and the private key in Windows
Credential Manager → relaunch → lands on the home placeholder directly.

## Success

- Scenarios A–D pass automatically (A/B privileged; C/D non-privileged).
- Scenario E passes by manual inspection on Windows (GUI + DPAPI — the documented
  manual-validation exception).
