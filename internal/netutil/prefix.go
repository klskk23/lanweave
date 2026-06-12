// Package netutil holds the small IPv4-prefix helpers the consumer-route
// machinery (feature 033) needs on both clients — one implementation so the
// Windows tunnel and the OpenWrt engine can never drift apart.
package netutil

import (
	"net"
	"net/netip"
	"sort"
)

// CanonicalPrefixes masks, dedupes and sorts (by address, then prefix length).
func CanonicalPrefixes(in []netip.Prefix) []netip.Prefix {
	seen := map[netip.Prefix]bool{}
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		m := p.Masked()
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr() != out[j].Addr() {
			return out[i].Addr().Less(out[j].Addr())
		}
		return out[i].Bits() < out[j].Bits()
	})
	return out
}

// LocalOverlap reports whether the prefix overlaps a local IPv4 interface
// network other than the named tunnel interface, and names the first
// offending interface. IPv6 addresses cannot overlap an IPv4 prefix and are
// skipped.
func LocalOverlap(p netip.Prefix, excludeIface string) (string, bool) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}
	for _, ifc := range ifaces {
		if ifc.Name == excludeIface {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			addr, ok := netip.AddrFromSlice(ipnet.IP.To4())
			if !ok {
				continue
			}
			if netip.PrefixFrom(addr, ones).Masked().Overlaps(p) {
				return ifc.Name, true
			}
		}
	}
	return "", false
}
