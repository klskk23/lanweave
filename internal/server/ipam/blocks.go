package ipam

import (
	"errors"
	"fmt"
	"net/netip"
)

// ErrNoSpace reports that the synthetic pool has no free, aligned block of the
// requested size left.
var ErrNoSpace = errors.New("no free block of the requested size in the pool")

// Block is a CIDR as pool math sees it: base address (uint32, big-endian) plus
// prefix length. This is also exactly how blocks are persisted.
type Block struct {
	Base      uint32
	PrefixLen int
}

// BlockFromPrefix converts a parsed IPv4 prefix to its Block form (masked).
func BlockFromPrefix(p netip.Prefix) Block {
	p = p.Masked()
	return Block{Base: AddrToUint32(p.Addr()), PrefixLen: p.Bits()}
}

// Prefix converts the block back to a netip.Prefix.
func (b Block) Prefix() netip.Prefix {
	return netip.PrefixFrom(Uint32ToAddr(b.Base), b.PrefixLen)
}

// size returns the number of addresses the block spans (uint64: a /0 would
// overflow uint32).
func (b Block) size() uint64 { return uint64(1) << (32 - b.PrefixLen) }

// Overlaps reports whether two blocks share any address.
func Overlaps(a, b Block) bool {
	aStart, aEnd := uint64(a.Base), uint64(a.Base)+a.size()
	bStart, bEnd := uint64(b.Base), uint64(b.Base)+b.size()
	return aStart < bEnd && bStart < aEnd
}

// rfc1918 are the private IPv4 ranges announced subnets must live in.
var rfc1918 = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

// IsRFC1918 reports whether the prefix lies entirely inside one private range.
func IsRFC1918(p netip.Prefix) bool {
	for _, r := range rfc1918 {
		if r.Contains(p.Addr()) && p.Bits() >= r.Bits() {
			return true
		}
	}
	return false
}

// AllocateBlock returns the first naturally aligned free block of the requested
// prefix length inside pool, skipping every block in used (which may hold blocks
// of any size). It returns ErrNoSpace when the pool has no room left. The whole
// block is usable (no network/server/broadcast reservations): synthetic blocks
// are translated 1:1 by the announcing router, never addressed as a subnet here.
func AllocateBlock(pool netip.Prefix, prefixLen int, used []Block) (Block, error) {
	if !pool.Addr().Is4() {
		return Block{}, fmt.Errorf("pool %s is not IPv4", pool)
	}
	if prefixLen < pool.Bits() || prefixLen > 32 {
		return Block{}, fmt.Errorf("prefix length /%d does not fit pool %s", prefixLen, pool)
	}
	poolBlock := BlockFromPrefix(pool)
	step := Block{PrefixLen: prefixLen}.size()
	poolEnd := uint64(poolBlock.Base) + poolBlock.size()
	for base := uint64(poolBlock.Base); base+step <= poolEnd; base += step {
		candidate := Block{Base: uint32(base), PrefixLen: prefixLen}
		free := true
		for _, u := range used {
			if Overlaps(candidate, u) {
				free = false
				break
			}
		}
		if free {
			return candidate, nil
		}
	}
	return Block{}, ErrNoSpace
}
