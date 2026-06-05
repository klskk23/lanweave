// Package ipam holds pure IPv4 pool math for client address allocation.
package ipam

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

// PoolRange returns the inclusive client address range [first, last] as uint32 for
// the given IPv4 CIDR. The network base address (.0) and the server address (.1,
// the first usable) are reserved, so clients start at base+2; the broadcast address
// is reserved, so the range ends at broadcast-1. Returns an error for non-IPv4
// pools or ranges too small to hold a client address.
func PoolRange(cidr string) (first, last uint32, err error) {
	prefix, perr := netip.ParsePrefix(cidr)
	if perr != nil {
		return 0, 0, fmt.Errorf("parse pool %q: %w", cidr, perr)
	}
	if !prefix.Addr().Is4() {
		return 0, 0, fmt.Errorf("pool %q is not IPv4", cidr)
	}
	prefix = prefix.Masked()
	base := AddrToUint32(prefix.Addr())
	hostBits := 32 - prefix.Bits()
	broadcast := base | uint32((uint64(1)<<hostBits)-1)

	first = base + 2     // skip .0 (network) and .1 (server)
	last = broadcast - 1 // skip broadcast
	if last < first {
		return 0, 0, fmt.Errorf("pool %q is too small for client addresses (need /30 or larger)", cidr)
	}
	return first, last, nil
}

// AddrToUint32 converts an IPv4 address to its uint32 value (big-endian).
func AddrToUint32(a netip.Addr) uint32 {
	b := a.As4()
	return binary.BigEndian.Uint32(b[:])
}

// Uint32ToAddr converts a uint32 back to an IPv4 address.
func Uint32ToAddr(u uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], u)
	return netip.AddrFrom4(b)
}
