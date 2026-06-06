# Quickstart & Verification: Client Firewall Control and TOFU Certificate Pinning

Two verification tiers, matching the constitution: **automated headless** tests that run in the
standing `unshare` gate, and a **manual Windows matrix** for the `netsh` execution and the Fyne UI
(the standing GUI/exec exception, DESIGN §365).

## Build & gate

```sh
# Headless build (no GUI tag) — controller/apiclient/state/firewall logic compiles and tests:
CGO_ENABLED=0 go build ./cmd/lanweave-client        # stub main (non-gui)
unshare -rUn bash -c 'ip link set lo up && go test ./...'

# GUI build (Windows-facing) compiles + vets:
go build -tags gui ./...
go vet -tags gui ./...
gofmt -l .                                           # must be empty
```

## Automated acceptance (headless, no mocks of crypto)

### US1 — TOFU certificate pinning (`apiclient` against `httptest` TLS)

1. **First-trust fingerprint** — start an `httptest.NewTLSServer` (self-signed). Build
   `apiclient.New(url)` (verify on, no pin). A request fails with a `*CertError` whose
   `errors.Is(err, ErrUntrustedCert)` is true and whose `Fingerprint` equals
   `SHA-256(server leaf cert.Raw)` in lowercase hex. (FR-001/003)
2. **Pinned → silent verify** — rebuild `apiclient.New(url, WithPinnedCert(fp))` with that
   fingerprint; the same request succeeds (no error). (FR-002/003)
3. **CA-valid passes regardless of pin** — using `WithRootCAs(pool)` for the server's own cert plus a
   bogus pin, the request still succeeds via the system/roots path (pin-or-CA). (FR-003)
4. **Certificate changed** — build with `WithPinnedCert(F)` but point at a server presenting a
   *different* self-signed cert `F'`; the request fails with `errors.Is(err, ErrCertChanged)` and
   `*CertError{Fingerprint: F', Changed: true}`. (FR-005)

### US2 — firewall decision logic (`panel.Controller` with fake `firewall.Control` + fake `api`)

5. **Preference ∧ connected matrix** — for each (preference, connected) in the 4-cell table,
   `ReconcileFirewall(connected)` calls `Allow()` exactly when both are true, else `Clear()`. (FR-013)
6. **Toggle persistence** — `SetFirewallAllowed(true, connected)` writes `FirewallAllowVPN=true` to
   `state.json` (reload to confirm) and applies the seam per the matrix; `SetFirewallAllowed(false,…)`
   writes false and `Clear()`s. (FR-011/012/014/015)
7. **Idempotent apply** — two consecutive `Allow()`s record a delete-then-add each time (the fake
   asserts no duplicate "open" state). (FR-016)
8. **Clear on logout** — `Logout()` calls the firewall seam's `Clear()` and then clears state; after
   logout, reload shows no record (so preference is gone, defaults on re-onboard). (FR-015, edge case)

### state migration (`state`)

9. **v1 → v2 default load** — write a `state.json` with `"schema_version": 1` and no new keys; `Load`
   succeeds with `PinnedCertSHA256 == ""` and `FirewallAllowVPN == false`. Round-trip `Save` then
   `Load` of a record carrying both new fields preserves them and writes version 2. (FR-020)

## Manual Windows verify matrix (real `netsh` + Fyne)

Run on a real Windows 10/11 desktop (or a Mesa-equipped VM — drop `opengl32.dll` next to the exe; see
the GUI-test-VM note). Elevated, since `netsh advfirewall` + adapter creation need admin.

| # | Action | Expected |
|---|---|---|
| M1 | Fresh upgrade from prior version, open client | Loads without re-onboarding; footer shows the "Allow inbound from VPN peers (100.127.0.0/16)" check **off**, with the inline exposure warning beside it |
| M2 | Connect to a self-signed/internal server for the first time | First-trust dialog names the server, shows the certificate fingerprint; declining returns to server-address entry without connecting |
| M3 | Accept the first-trust dialog | Connects; main panel shows the neutral "self-signed (trusted on this device)" indicator (not the red "⚠ certificate not verified") |
| M4 | Quit and relaunch, connect again to the same server | Connects **silently** — no certificate prompt |
| M5 | Server presents a *different* unverifiable certificate | Heavier "certificate changed" warning appears before any data; declining blocks; accepting connects and updates the remembered fingerprint |
| M6 | While connected, toggle the firewall check **on** | `netsh advfirewall firewall show rule name=lanweave-vpn-inbound` lists an inbound allow rule scoped to `100.127.0.0/16`; a VPN peer can now reach a local service that was blocked |
| M7 | Toggle the check **off** (still connected) | Rule disappears (`show rule` → "No rules match"); peer access blocked again |
| M8 | Re-enable, then Disconnect | Rule disappears on disconnect; peer access blocked |
| M9 | Re-enable, connect, then **kill the process** (Task Manager); relaunch | On startup the stranded rule is swept; after reconnect the rule is re-applied per the remembered toggle; `show rule` shows exactly one rule (no duplicates) |
| M10 | Toggle on, connected, then Log out | Tunnel drops, rule removed, returns to server-address step; relaunch shows the check back at its default (off) |
| M11 | Run with `--insecure` against a self-signed server | Connects with the existing red "⚠ certificate not verified" banner (no TOFU prompt); confirms the CLI escape hatch is intact |

## Done when

- All headless tests (1–9) green under `unshare -rUn ... go test ./...`.
- `go build -tags gui ./...` + `go vet -tags gui` clean; `gofmt -l` empty.
- Manual matrix M1–M11 verified on Windows and recorded in the PR (the tasks.md Windows row).
