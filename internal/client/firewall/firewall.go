// Package firewall manages the optional host inbound-allow rule that lets same-subnet VPN peers
// reach this device (feature 018). It is a thin wrapper over the platform firewall: on Windows it
// drives `netsh advfirewall` (a peer of the tunnel's addr_windows.go), elsewhere it is a no-op so
// the preference still persists and the UI stays uniform. The rule is named and idempotent so it
// can be added, removed, and swept on startup without leaving duplicates or orphans.
package firewall

const (
	// RuleName is the stable name of the inbound-allow rule, so it can be added idempotently
	// (delete-then-add), removed exactly, and swept on startup regardless of who created it.
	RuleName = "lanweave-vpn-inbound"
	// VPNSubnet scopes the rule to the VPN pool: only peers addressed in this range may reach
	// this device's local services. It mirrors the WireGuard AllowedIPs.
	VPNSubnet = "100.127.0.0/16"
)

// Control installs and removes the host inbound-allow rule. Allow is idempotent (it removes any
// existing rule of the same name before adding, so repeated calls converge on one rule); Clear
// removes the rule and is a no-op when it is absent.
type Control interface {
	Allow() error
	Clear() error
}

// System returns the Control backed by the host firewall (real netsh on Windows, no-op elsewhere).
func System() Control { return system() }

// Clear removes the rule via the system Control. It is a package-level convenience for the startup
// orphan sweep and the exit teardown, where no Controller is in hand.
func Clear() error { return System().Clear() }
