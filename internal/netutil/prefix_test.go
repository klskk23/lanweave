package netutil_test

import (
	"net/netip"
	"testing"

	"lanweave/internal/netutil"
)

func TestCanonicalPrefixes(t *testing.T) {
	p := func(s string) netip.Prefix { return netip.MustParsePrefix(s) }
	got := netutil.CanonicalPrefixes([]netip.Prefix{p("100.100.2.7/24"), p("100.100.1.0/24"), p("100.100.2.0/24")})
	if len(got) != 2 || got[0] != p("100.100.1.0/24") || got[1] != p("100.100.2.0/24") {
		t.Errorf("canonical = %v, want masked+deduped+sorted", got)
	}
	// Same-address different-length ordering is deterministic (bits ascending).
	got = netutil.CanonicalPrefixes([]netip.Prefix{p("100.100.0.0/24"), p("100.100.0.0/23")})
	if got[0].Bits() != 23 || got[1].Bits() != 24 {
		t.Errorf("bits ordering = %v", got)
	}
}
