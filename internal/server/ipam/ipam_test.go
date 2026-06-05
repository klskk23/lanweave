package ipam_test

import (
	"net/netip"
	"testing"

	"lanweave/internal/server/ipam"
)

func TestPoolRange(t *testing.T) {
	ok := []struct {
		cidr        string
		first, last string
	}{
		{"100.127.0.0/16", "100.127.0.2", "100.127.255.254"},
		{"10.0.0.0/24", "10.0.0.2", "10.0.0.254"},
		{"192.168.1.0/30", "192.168.1.2", "192.168.1.2"}, // base .0, server .1, client .2, broadcast .3
	}
	for _, tc := range ok {
		first, last, err := ipam.PoolRange(tc.cidr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.cidr, err)
			continue
		}
		if got := ipam.Uint32ToAddr(first).String(); got != tc.first {
			t.Errorf("%s: first = %s, want %s", tc.cidr, got, tc.first)
		}
		if got := ipam.Uint32ToAddr(last).String(); got != tc.last {
			t.Errorf("%s: last = %s, want %s", tc.cidr, got, tc.last)
		}
		if last < first {
			t.Errorf("%s: last < first", tc.cidr)
		}
	}

	bad := []string{
		"192.168.1.0/31", // no client room
		"192.168.1.0/32",
		"fd00::/64", // IPv6
		"nonsense",
	}
	for _, cidr := range bad {
		if _, _, err := ipam.PoolRange(cidr); err == nil {
			t.Errorf("%s: expected error, got nil", cidr)
		}
	}
}

func TestUint32AddrRoundTrip(t *testing.T) {
	for _, s := range []string{"0.0.0.0", "100.127.0.2", "255.255.255.255", "10.1.2.3"} {
		addr := netip.MustParseAddr(s)
		if got := ipam.Uint32ToAddr(ipam.AddrToUint32(addr)); got != addr {
			t.Errorf("round-trip %s -> %s", s, got)
		}
	}
}
