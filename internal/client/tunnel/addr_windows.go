//go:build windows

package tunnel

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
)

// configureAdapter assigns the device's VPN address to the WinTun adapter and routes the
// VPN range through it, via `netsh` (no extra dependency). Creating/addressing the adapter
// requires administrator rights (the app requests elevation). This path is validated
// manually on Windows.
func configureAdapter(ifName string, ip netip.Addr, network netip.Prefix) error {
	mask := prefixToMask(network.Bits())
	if out, err := exec.Command("netsh", "interface", "ip", "set", "address",
		"name="+ifName, "static", ip.String(), mask).CombinedOutput(); err != nil {
		return fmt.Errorf("set address: %w (%s)", err, out)
	}
	if out, err := exec.Command("netsh", "interface", "ip", "add", "route",
		network.Masked().String(), ifName).CombinedOutput(); err != nil {
		return fmt.Errorf("add route: %w (%s)", err, out)
	}
	return nil
}

// teardownAdapter is best-effort; closing the user-space device removes the WinTun adapter.
func teardownAdapter(ifName string) error {
	_ = ifName
	return nil
}

// interfacePresent is approximated on Windows (the adapter goes away with the device).
func interfacePresent(ifName string) bool {
	err := exec.Command("netsh", "interface", "show", "interface", "name="+ifName).Run()
	return err == nil
}

func prefixToMask(bits int) string {
	var m [4]int
	for i := range 4 {
		b := bits - i*8
		switch {
		case b >= 8:
			m[i] = 255
		case b <= 0:
			m[i] = 0
		default:
			m[i] = 256 - (1 << (8 - b))
		}
	}
	return strconv.Itoa(m[0]) + "." + strconv.Itoa(m[1]) + "." + strconv.Itoa(m[2]) + "." + strconv.Itoa(m[3])
}

// addRoute/delRoute manage a single consumer route (feature 033) on the WinTun
// adapter, the same netsh surface configureAdapter uses.
func addRoute(ifName string, p netip.Prefix) error {
	if out, err := exec.Command("netsh", "interface", "ip", "add", "route",
		p.Masked().String(), ifName, "store=active").CombinedOutput(); err != nil {
		return fmt.Errorf("add route %s: %w (%s)", p, err, out)
	}
	return nil
}

func delRoute(ifName string, p netip.Prefix) error {
	out, err := exec.Command("netsh", "interface", "ip", "delete", "route",
		p.Masked().String(), ifName, "store=active").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") && !strings.Contains(string(out), "Element not found") {
		return fmt.Errorf("delete route %s: %w (%s)", p, err, out)
	}
	return nil
}
