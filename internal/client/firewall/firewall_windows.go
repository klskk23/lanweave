//go:build windows

package firewall

import (
	"fmt"
	"os/exec"
	"strings"
)

// system returns the netsh-backed Control. Adding/removing a firewall rule needs administrator
// rights (the app already runs elevated to create the WinTun adapter). Validated manually on
// Windows (DESIGN §11 GUI/exec exception).
func system() Control { return netshControl{} }

type netshControl struct{}

// Allow installs the inbound-allow rule idempotently: it first deletes any rule of the same name
// (ignoring "no match"), then adds a fresh one scoped to the VPN subnet on all ports and profiles.
// Delete-then-add means repeated calls never accumulate duplicates.
func (netshControl) Allow() error {
	_ = del() // best-effort: a missing rule is fine; the add below establishes the desired state.
	if out, err := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+RuleName,
		"dir=in",
		"action=allow",
		"remoteip="+VPNSubnet,
		"profile=any",
	).CombinedOutput(); err != nil {
		return fmt.Errorf("add firewall rule: %w (%s)", err, out)
	}
	return nil
}

// Clear removes the rule, treating an absent rule as success so the startup sweep and teardown are
// idempotent.
func (netshControl) Clear() error { return del() }

// del removes every rule with RuleName. netsh exits non-zero when no rule matches; that is the
// already-clean case, so it is not treated as an error.
func del() error {
	out, err := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		"name="+RuleName).CombinedOutput()
	if err != nil && !noMatch(out) {
		return fmt.Errorf("delete firewall rule: %w (%s)", err, out)
	}
	return nil
}

// noMatch reports whether netsh's output indicates there was simply no rule to delete.
func noMatch(out []byte) bool {
	return strings.Contains(strings.ToLower(string(out)), "no rules match")
}
