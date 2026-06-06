# Phase 0 Research: Client Firewall Control and TOFU Certificate Pinning

All design unknowns were resolved during a prior grill session and confirmed by reading the current
client code (`apiclient`, `state`, `tunnel`, `panel`, `ui`, `main`). No `[NEEDS CLARIFICATION]`
markers remain. Decisions below are the inputs to Phase 1.

## D1 — TOFU verification mechanism (pin-or-system)

**Decision**: Build the API client's `tls.Config` with `InsecureSkipVerify: true` **and** a custom
`VerifyConnection(cs tls.ConnectionState) error` that:
1. computes the leaf certificate fingerprint (D2);
2. returns nil if a pin is configured and the fingerprint equals it;
3. otherwise runs standard chain verification (`leaf.Verify` with `Roots` = the configured pool or
   system roots, `DNSName` = the server host, intermediates from `cs.PeerCertificates[1:]`) and
   returns nil on success;
4. otherwise returns a `*CertError` carrying the fingerprint and whether a pin was configured.

`InsecureSkipVerify: true` only disables Go's *default* verification so our `VerifyConnection`
becomes the sole authority — it is **not** "skip verification". The existing `WithRootCAs(pool)` test
seam is honored by passing the pool as `Roots`.

**Rationale**: `VerifyConnection` sees the full negotiated `ConnectionState`, so it can both pin and
fall back to system trust in one place, and it yields the leaf certificate uniformly (for both the
first-trust and changed cases) without parsing transport error types. It keeps the "verify = pin OR
system CA" rule (FR-003) in exactly one function.

**Alternatives considered**:
- *Default verifier + parse `x509.UnknownAuthorityError.Cert`* — works for first-trust (the error
  carries the leaf) but cannot express "accept if pinned", so the pinned path would still need a
  second mechanism. Rejected for splitting the rule across two code paths.
- *`VerifyPeerCertificate`* — fires before the handshake completes and does not receive
  `VerifiedChains`/`ServerName` as cleanly; `VerifyConnection` is the documented hook for custom
  whole-connection policy.

## D2 — Certificate fingerprint (identity + storage form)

**Decision**: Pin the **leaf** certificate by `SHA-256(cert.Raw)` (the DER bytes), stored as
lowercase hex with no separators in `state.Record.PinnedCertSHA256`. The UI may format it
group-wise (e.g. colon-separated, uppercase) for display only.

**Rationale**: Leaf pinning is the standard TOFU granularity (matches "this exact certificate"),
survives intermediate/root reshuffling, and is trivial to compute and compare. Hex-without-colons is
a stable canonical storage form; display formatting is a presentation concern.

**Alternatives considered**: Pinning the SPKI (public-key) hash would survive benign re-issuance with
the same key, but the spec deliberately treats any fingerprint change as a "certificate changed"
event requiring re-acceptance (an accepted friction, per Edge Cases), so leaf-cert pinning is the
simpler match.

## D3 — First-trust vs certificate-changed, and typed errors

**Decision**: Add `ErrCertChanged = errors.New("server certificate changed")` and a struct error:

```go
type CertError struct { Fingerprint string; Changed bool }
func (e *CertError) Error() string { ... }
func (e *CertError) Is(target error) bool {
    if e.Changed { return target == ErrCertChanged }
    return target == ErrUntrustedCert
}
```

`Changed` is true when the client was built with a pin but the presented certificate matched neither
the pin nor system trust. `do()` unwraps the transport error to find `*CertError` (it now originates
from our `VerifyConnection`) and returns it directly. Callers use `errors.Is(err, ErrUntrustedCert)`
/ `errors.Is(err, ErrCertChanged)` as today, and type-assert `*CertError` to read the fingerprint
for the prompt.

**Rationale**: Keeps the existing `errors.Is` call sites working while carrying the fingerprint the
UI must display. One struct, two sentinels, no behavior hidden.

**Alternatives considered**: Two separate non-struct sentinels with the fingerprint smuggled in a
wrapped message string — rejected as fragile (string parsing) and lossy.

## D4 — Re-pin means rebuild, not mutate

**Decision**: The pin is supplied at construction via `WithPinnedCert(fpHex string) Option`. On
first-trust accept or cert-changed accept, the caller persists the new pin to `state.json` and
**rebuilds** the client with `apiclient.New(url, WithPinnedCert(fp))`, re-applying the cached session
token (exactly the rebuild pattern 017 established for `WithInsecure`). The `--insecure` flag still
short-circuits to `InsecureSkipVerify` with no `VerifyConnection` (unchanged behavior + existing
severe banner).

**Rationale**: `apiclient` bakes TLS at `New()`; rebuilding keeps the constructor the single place
TLS policy is set and avoids mutating a live `http.Transport`.

## D5 — Firewall rule shape (Windows Defender via netsh)

**Decision**: One named inbound allow rule:
```
netsh advfirewall firewall add rule name=lanweave-vpn-inbound dir=in action=allow \
      remoteip=100.127.0.0/16 profile=any
```
An allow rule with no `protocol`/`localport` covers all ports and ICMP. Idempotent application is
**delete-then-add**:
```
netsh advfirewall firewall delete rule name=lanweave-vpn-inbound   # ignore "No rules match"
netsh advfirewall firewall add rule name=lanweave-vpn-inbound ...
```
`Clear()` is the `delete` line alone (ignoring the no-match result). Mirrors `addr_windows.go`'s use
of `exec.Command("netsh", ...)` with `CombinedOutput()`.

**Rationale**: A named rule is required for idempotent re-apply, startup sweep, and clean removal.
`remoteip` scopes the opening to the VPN subnet only. `profile=any` ensures it applies regardless of
the WinTun adapter's network profile (which defaults to Public). No protocol filter = "all local
services", matching the spec's single coarse toggle.

**Alternatives considered**: Per-port rules (rejected — the spec is explicitly a single all-or-
nothing toggle, FR not-in-scope), or editing the adapter's network profile to Private (rejected —
broader and less precise than a scoped inbound rule).

## D6 — Firewall lifecycle ownership and hook points

**Decision**: The rule exists iff (preference ON ∧ tunnel Connected). Ownership split:
- **`panel.Controller`** (headless) holds the preference (from `state.Record.FirewallAllowVPN`) and a
  two-method firewall seam `interface{ Allow() error; Clear() error }`. It exposes
  `FirewallAllowed() bool`, `SetFirewallAllowed(on, connected bool) error` (persist preference, then
  `Allow()` if `on && connected` else `Clear()`), and `ReconcileFirewall(connected bool) error`
  (`Allow()` if `preference && connected` else `Clear()`).
- **`ui/panel.go`** wires events: after a successful `tn.Connect()` → `ReconcileFirewall(true)`;
  `onDisconnect`/`confirmLogout` → `ReconcileFirewall(false)`; the footer `Check` handler →
  `SetFirewallAllowed(checked, tn.State()==Connected)`.
- **`main.go`** does the startup orphan sweep (`firewall.Clear()` before showing UI) and
  `defer firewall.Clear()` on exit (next to the existing `defer tn.Close()`).

**Rationale**: Putting the (preference ∧ connection) decision and the persistence in the already-
headless Controller makes it unit-testable with a fake seam; the gui layer only forwards events. The
tunnel stays unaware of the firewall (Principle I separation).

**Alternatives considered**: A firewall manager living inside `Tunnel` — rejected because the tunnel
has no business knowing the user's toggle preference. A standalone controller — rejected as more
surface than ~10 lines of logic warrant; the Controller already owns analogous local state.

## D7 — Startup orphan sweep

**Decision**: On every startup, `main` calls `firewall.Clear()` (delete-by-name, ignore no-match)
before building the panel; the subsequent first connect re-applies the rule if the (now loaded)
preference is ON. This guarantees an unclean shutdown that stranded the rule is reconciled.

**Rationale**: The rule is derivative state; the database/`state.json` is the source of truth, and the
post-startup reconcile rebuilds it. Sweeping first is the cheapest way to converge.

## D8 — State schema migration (1 → 2, additive)

**Decision**: Bump `state.SchemaVersion` to 2 and add `PinnedCertSHA256 string` +
`FirewallAllowVPN bool` (both `omitempty`-friendly zero values). `Load` continues to reject only
`SchemaVersion == 0` (and the existing required fields), so v1 records load with both new fields
defaulted; on next `Save` the version is written as 2. No re-onboarding, no data loss (FR-020).

**Rationale**: Additive optional fields are backward compatible; the single bump bundles both
features per the spec's atomic-unit decision.

## D9 — VPN subnet constant

**Decision**: Use the fixed CIDR `100.127.0.0/16` (a package constant in `firewall`), matching
DESIGN §66/§77 (the CGNAT pool and the client's `AllowedIPs`).

**Rationale**: The subnet is a frozen design constant; hardcoding it keeps the rule unambiguous and
avoids widening the opening if `state.Record.Network` ever held something broader. Documented so a
future pool change updates both DESIGN and this constant together.

## D10 — Windows-only enforcement, preference everywhere

**Decision**: `firewall_windows.go` runs `netsh`; `firewall_other.go` (`//go:build !windows`) is a
no-op `Control`. The preference is still read/written in `state.json` on all platforms, so the
headless Controller tests run on Linux CI and the toggle persists regardless of platform.

**Rationale**: The GUI ships only on Windows, but keeping a no-op elsewhere lets the controller logic
compile and be unit-tested under the `unshare` gate without `//go:build windows` leaking into
`panel`.

## D11 — Removing 017's session-level insecure opt-in

**Decision**: Delete the per-session "continue insecurely, not remembered" UI path
(`ui/panel.go::offerInsecure` and the wizard's equivalent) and its `panel.Controller.UseInsecureClient`
session-swap usage. Replace with the TOFU first-trust / cert-changed dialogs. The `--insecure` CLI
flag and its severe persistent banner remain unchanged. DESIGN §275–§277 / §362 are amended to the
TOFU posture; the corresponding 017 FRs are marked superseded (FR-010).

**Rationale**: TOFU subsumes the legitimate use case (self-signed/internal servers) with a safer,
persistent, per-server trust decision; keeping the old session path would leave two overlapping
opt-ins and contradict the amended DESIGN.

## Constitution re-check (post-research)

No new violations introduced by these decisions. The DESIGN amendments, the new `firewall` package,
the schema bump, and the Controller seam are all recorded in `plan.md` Complexity Tracking. Proceed
to Phase 1.
