// Package wg manages the relay's WireGuard server identity and tunnel interface.
package wg

import (
	"fmt"
	"net/netip"
)

// FirstUsableAddress returns the first usable host address of an IPv4 CIDR pool
// (the network base address + 1, e.g. 100.127.0.1 for 100.127.0.0/16) together
// with the prefix length, so the interface can be addressed to route the whole
// pool. Ranges too small to host a server address (/31, /32) return an error.
func FirstUsableAddress(cidr string) (netip.Addr, int, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("parse pool %q: %w", cidr, err)
	}
	if !prefix.Addr().Is4() {
		return netip.Addr{}, 0, fmt.Errorf("pool %q is not IPv4", cidr)
	}
	bits := prefix.Bits()
	if bits > 30 {
		return netip.Addr{}, 0, fmt.Errorf("pool %q is too small for a server address (need /30 or larger)", cidr)
	}
	first := prefix.Masked().Addr().Next() // network base + 1
	if !prefix.Contains(first) {
		return netip.Addr{}, 0, fmt.Errorf("pool %q has no usable host address", cidr)
	}
	return first, bits, nil
}
