# Phase 1 Contracts: Client Surface Deltas

There is **no server API change** in this feature — no new endpoints, no changed request/response
shapes. The contracts below are the **client-internal** surfaces (package APIs) that the
implementation and its tests bind to. They are the seams the headless tests exercise.

## 1. `internal/client/apiclient` — TLS pinning

```go
// New option (mutually exclusive with WithInsecure).
func WithPinnedCert(sha256Hex string) Option   // lowercase hex of the trusted leaf cert

// New typed error + struct error carrying the offending leaf fingerprint.
var ErrCertChanged = errors.New("server certificate changed")

type CertError struct {
    Fingerprint string // lowercase hex SHA-256 of the presented leaf certificate
    Changed     bool   // true: a pin was configured but did not match (vs first-trust)
}
func (e *CertError) Error() string
func (e *CertError) Is(target error) bool // Changed→ErrCertChanged, else→ErrUntrustedCert
```

**Verification rule (in `New`'s `tls.Config.VerifyConnection`)**: accept iff the leaf fingerprint
equals the configured pin **or** the chain passes verification against the configured roots
(`WithRootCAs` pool when set, else system). On rejection, return `*CertError{Fingerprint, Changed}`
where `Changed == (pin != "")`. The `--insecure` path (`WithInsecure`) is unchanged and skips this.

**Behavior contract**:

| Built with | Server presents | Result |
|---|---|---|
| no pin, verify on | CA-valid cert | connect OK |
| no pin, verify on | self-signed cert | `*CertError{fp, Changed:false}` → `errors.Is ErrUntrustedCert` |
| `WithPinnedCert(F)` | leaf fp == F | connect OK (no system check needed) |
| `WithPinnedCert(F)` | CA-valid cert (fp ≠ F) | connect OK (system-CA path) |
| `WithPinnedCert(F)` | self-signed fp F' ≠ F | `*CertError{F', Changed:true}` → `errors.Is ErrCertChanged` |
| `WithInsecure()` | anything | connect OK, no verification (existing behavior) |

## 2. `internal/client/state` — record + schema

```go
const SchemaVersion = 2

type Record struct {
    // ...existing fields...
    PinnedCertSHA256 string `json:"pinned_cert_sha256"` // empty = unpinned
    FirewallAllowVPN bool   `json:"firewall_allow_vpn"` // false = closed (default)
}
```

`Load` accepts schema 1 and 2; a v1 record yields empty pin + firewall off. `Save` writes version 2.

## 3. `internal/client/firewall` — host inbound allowance (NEW package)

```go
const RuleName = "lanweave-vpn-inbound"
const VPNSubnet = "100.127.0.0/16"

// Control is the seam the panel controller drives; *system satisfies it on Windows,
// a no-op satisfies it elsewhere; tests supply a fake.
type Control interface {
    Allow() error // delete-by-name then add the inbound-allow rule (idempotent)
    Clear() error // delete-by-name; ignore "no rules match"
}

func System() Control // platform default (netsh on Windows, no-op otherwise)
func Clear() error    // package-level convenience for the startup sweep == System().Clear()
```

## 4. `internal/client/panel.Controller` — firewall preference + reconcile

```go
// New gains the firewall seam (keeps the controller headless-testable).
func New(a api, record state.Record, keys keyring.Store, statePath string,
        insecure bool, fw firewall.Control) *Controller

func (c *Controller) FirewallAllowed() bool

// Persist the preference to state.json, then apply: Allow() if (on && connected) else Clear().
func (c *Controller) SetFirewallAllowed(on, connected bool) error

// Converge OS state after a connect/disconnect: Allow() if (preference && connected) else Clear().
func (c *Controller) ReconcileFirewall(connected bool) error
```

**Decision contract** (unit-tested with a fake `firewall.Control` + fake `api`):

| preference | connected | action |
|---|---|---|
| ON | true | `Allow()` |
| ON | false | `Clear()` |
| OFF | true | `Clear()` |
| OFF | false | `Clear()` |

`SetFirewallAllowed` also writes `Record.FirewallAllowVPN` via `state.Save`. `Logout` additionally
calls `Clear()` (defensive) before clearing state. Re-applying `Allow()` twice produces no duplicate
rule (delete-then-add).

## 5. UI wiring (gui-tagged; manual matrix)

- `ui/panel.go`: footer `widget.Check` "Allow inbound from VPN peers (100.127.0.0/16)" + persistent
  inline warning label; handler → `SetFirewallAllowed`. `onConnect` (on success) → `ReconcileFirewall(true)`;
  `onDisconnect`/`confirmLogout` → `ReconcileFirewall(false)`. Replace `offerInsecure` with TOFU
  first-trust dialog (shows fingerprint), cert-changed heavier dialog, and the neutral
  "self-signed (trusted on this device)" indicator.
- `ui/wizard.go`: `runProvision` builds the client with the stored pin; on `*CertError` (first-trust)
  shows the trust prompt, persists the pin on accept, rebuilds + retries; on `ErrCertChanged` shows
  the heavier warning.
- `main.go`: build `apiclient.New(url, WithPinnedCert(rec.PinnedCertSHA256))` (or insecure);
  `firewall.Clear()` startup sweep; `defer firewall.Clear()`.

## Non-contract (explicitly unchanged)

- `DELETE /api/v1/nodes/{id}` and all other server endpoints — unchanged.
- Keyring names (`DeviceKeyName`, `SessionTokenName`) — unchanged; secrets stay out of `state.json`.
