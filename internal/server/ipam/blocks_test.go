package ipam_test

import (
	"errors"
	"net/netip"
	"testing"

	"lanweave/internal/server/ipam"
)

func block(t *testing.T, cidr string) ipam.Block {
	t.Helper()
	return ipam.BlockFromPrefix(netip.MustParsePrefix(cidr))
}

func TestAllocateBlockFirstFitAligned(t *testing.T) {
	pool := netip.MustParsePrefix("100.100.0.0/16")
	for _, tc := range []struct {
		name      string
		prefixLen int
		used      []string
		want      string
		wantErr   error
	}{
		{"empty pool takes the base block", 24, nil, "100.100.0.0/24", nil},
		{"first fit skips the used base", 24, []string{"100.100.0.0/24"}, "100.100.1.0/24", nil},
		{"gap after reclaim is reused", 24, []string{"100.100.1.0/24"}, "100.100.0.0/24", nil},
		{"mixed sizes: /23 jumps the aligned slots", 23, []string{"100.100.0.0/24"}, "100.100.2.0/23", nil},
		{"a /30 packs tightly", 30, []string{"100.100.0.0/30"}, "100.100.0.4/30", nil},
		{"whole-pool block when empty", 16, nil, "100.100.0.0/16", nil},
		{"whole-pool block blocked by any allocation", 16, []string{"100.100.5.0/24"}, "", ipam.ErrNoSpace},
		{"exhausted /17 pair", 17, []string{"100.100.0.0/17", "100.100.128.0/17"}, "", ipam.ErrNoSpace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			used := make([]ipam.Block, 0, len(tc.used))
			for _, u := range tc.used {
				used = append(used, block(t, u))
			}
			got, err := ipam.AllocateBlock(pool, tc.prefixLen, used)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("allocate: %v", err)
			}
			if got.Prefix().String() != tc.want {
				t.Errorf("block = %s, want %s", got.Prefix(), tc.want)
			}
		})
	}
}

func TestAllocateBlockRejectsImpossibleSizes(t *testing.T) {
	pool := netip.MustParsePrefix("100.100.0.0/24")
	if _, err := ipam.AllocateBlock(pool, 16, nil); err == nil {
		t.Error("block larger than pool must error")
	}
	if _, err := ipam.AllocateBlock(netip.MustParsePrefix("fd00::/64"), 80, nil); err == nil {
		t.Error("IPv6 pool must error")
	}
}

func TestOverlaps(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"192.168.1.0/24", "192.168.1.0/24", true},
		{"192.168.1.0/24", "192.168.1.128/25", true},
		{"192.168.1.0/24", "192.168.2.0/24", false},
		{"10.0.0.0/8", "10.255.255.0/24", true},
		{"172.16.0.0/12", "172.32.0.0/16", false},
	} {
		a, b := block(t, tc.a), block(t, tc.b)
		if got := ipam.Overlaps(a, b); got != tc.want {
			t.Errorf("Overlaps(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := ipam.Overlaps(b, a); got != tc.want {
			t.Errorf("Overlaps(%s, %s) = %v, want %v (symmetry)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestIsRFC1918(t *testing.T) {
	for cidr, want := range map[string]bool{
		"192.168.1.0/24": true,
		"10.0.0.0/8":     true,
		"172.16.5.0/24":  true,
		"172.32.0.0/16":  false, // just past 172.16/12
		"8.8.8.0/24":     false,
		"100.64.0.0/16":  false, // CGNAT is not RFC1918
		"192.168.0.0/15": false, // wider than the 192.168/16 range itself
	} {
		if got := ipam.IsRFC1918(netip.MustParsePrefix(cidr)); got != want {
			t.Errorf("IsRFC1918(%s) = %v, want %v", cidr, got, want)
		}
	}
}
