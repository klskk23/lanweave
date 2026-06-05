package wg_test

import (
	"testing"

	"lanweave/internal/server/wg"
)

func TestFirstUsableAddress(t *testing.T) {
	ok := []struct {
		cidr     string
		wantAddr string
		wantBits int
	}{
		{"100.127.0.0/16", "100.127.0.1", 16},
		{"10.0.0.0/24", "10.0.0.1", 24},
		{"192.168.1.0/30", "192.168.1.1", 30},
	}
	for _, tc := range ok {
		addr, bits, err := wg.FirstUsableAddress(tc.cidr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.cidr, err)
			continue
		}
		if addr.String() != tc.wantAddr || bits != tc.wantBits {
			t.Errorf("%s: got %s/%d, want %s/%d", tc.cidr, addr, bits, tc.wantAddr, tc.wantBits)
		}
	}

	bad := []string{
		"100.127.0.0/31", // point-to-point, no usable host for our purposes
		"100.127.0.5/32", // single host
		"not-a-cidr",
		"fd00::/64", // IPv6 not supported for the pool
	}
	for _, cidr := range bad {
		if _, _, err := wg.FirstUsableAddress(cidr); err == nil {
			t.Errorf("%s: expected an error, got nil", cidr)
		}
	}
}
