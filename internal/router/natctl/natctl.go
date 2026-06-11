// Package natctl owns the announcer router's address-translation table: for
// every announced subnet one prefix-DNAT rule (synthetic → real, host bits
// preserved via the kernel's NETMAP/prefix NAT) and one masquerade rule (VPN
// member sources → router's egress address) live in a dedicated nftables table
// that never touches the device's own firewall (fw4). The table is pure
// derived state — the server's announcement list is the single source of
// truth — so every mutation is a full atomic rebuild (the netfw.Rebuild
// pattern, feature 030).
package natctl

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"lanweave/internal/server/ipam"
)

// DefaultTable is the production table name on the router.
const DefaultTable = "lanweave_rt"

// lockTable takes an exclusive cross-process flock for the table's mutations:
// the daemon's reconcile loop and a CLI command run in separate processes and
// must not interleave their delete-table/add-table flushes. The lock file
// lives in the OS temp dir keyed by table name; the returned func releases it.
func lockTable(table string) (func(), error) {
	path := filepath.Join(os.TempDir(), "lanweave-nat-"+table+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open nat lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock nat table: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

// Rule is one announcement's translation: traffic to Synthetic is prefix-DNAT'd
// to Real; forwarded traffic from the VPN pool toward Real is masqueraded.
type Rule struct {
	Synthetic netip.Prefix
	Real      netip.Prefix
}

// userData encodes the rule mapping into the nftables rule's opaque userdata,
// so Current can read the table back without decoding NAT expressions.
func (r Rule) userData() []byte {
	return []byte("lw " + r.Synthetic.String() + ">" + r.Real.String())
}

func ruleFromUserData(b []byte) (Rule, bool) {
	s, ok := strings.CutPrefix(string(b), "lw ")
	if !ok {
		return Rule{}, false
	}
	synth, real, ok := strings.Cut(s, ">")
	if !ok {
		return Rule{}, false
	}
	sp, err1 := netip.ParsePrefix(synth)
	rp, err2 := netip.ParsePrefix(real)
	if err1 != nil || err2 != nil {
		return Rule{}, false
	}
	return Rule{Synthetic: sp, Real: rp}, true
}

// Rebuild brings the translation table to exactly the given rule set: drop any
// existing table, recreate it with a prerouting (dstnat) and a postrouting
// (srcnat) chain, and lay one DNAT-prefix + one masquerade rule per entry, all
// in one atomic flush.
func Rebuild(table string, vpnPool netip.Prefix, rules []Rule) error {
	unlock, err := lockTable(table)
	if err != nil {
		return err
	}
	defer unlock()
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	if err := deleteTable(conn, table); err != nil {
		return err
	}

	t := conn.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
	pre := conn.AddChain(&nftables.Chain{
		Name:     "prerouting",
		Table:    t,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityNATDest,
	})
	post := conn.AddChain(&nftables.Chain{
		Name:     "postrouting",
		Table:    t,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
	})

	for _, r := range rules {
		conn.AddRule(&nftables.Rule{
			Table:    t,
			Chain:    pre,
			Exprs:    dnatPrefixExprs(r),
			UserData: r.userData(),
		})
		conn.AddRule(&nftables.Rule{
			Table: t,
			Chain: post,
			Exprs: masqueradeExprs(vpnPool, r.Real),
		})
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("build table %q: %w", table, err)
	}
	return nil
}

// Teardown removes the translation table; a missing table is success.
func Teardown(table string) error {
	unlock, err := lockTable(table)
	if err != nil {
		return err
	}
	defer unlock()
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	if err := deleteTable(conn, table); err != nil {
		return err
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("teardown table %q: %w", table, err)
	}
	return nil
}

// Current reads the rule set back from the live table (via the userdata
// mapping). A missing table reads as empty — derived state that simply has not
// been built yet.
func Current(table string) ([]Rule, error) {
	conn, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("open nftables: %w", err)
	}
	t, chain := findTableChain(conn, table, "prerouting")
	if t == nil || chain == nil {
		return nil, nil
	}
	rules, err := conn.GetRules(t, chain)
	if err != nil {
		return nil, fmt.Errorf("read table %q: %w", table, err)
	}
	var out []Rule
	for _, r := range rules {
		if rule, ok := ruleFromUserData(r.UserData); ok {
			out = append(out, rule)
		}
	}
	return out, nil
}

func deleteTable(conn *nftables.Conn, table string) error {
	existing, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	for _, t := range existing {
		if t.Name == table {
			conn.DelTable(t)
		}
	}
	return nil
}

func findTableChain(conn *nftables.Conn, table, chain string) (*nftables.Table, *nftables.Chain) {
	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		return nil, nil
	}
	for _, c := range chains {
		if c.Table != nil && c.Table.Name == table && c.Name == chain {
			return c.Table, c
		}
	}
	return nil, nil
}

// dnatPrefixExprs builds `ip daddr <synthetic> dnat prefix to <real>`: match
// the synthetic prefix on the destination, then hand the kernel the real
// prefix's first/last addresses with the PREFIX flag — NETMAP semantics, host
// bits preserved 1:1 (research D1; kernel >= 5.8).
func dnatPrefixExprs(r Rule) []expr.Any {
	synth := ipam.BlockFromPrefix(r.Synthetic)
	real := ipam.BlockFromPrefix(r.Real)
	realLast := real.Base + uint32(1)<<(32-real.PrefixLen) - 1
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
		// daddr inside the synthetic prefix.
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: prefixMask(synth.PrefixLen), Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: addrBytes(synth.Base)},
		// Translation range: real prefix first..last, PREFIX flag = netmap.
		&expr.Immediate{Register: 1, Data: addrBytes(real.Base)},
		&expr.Immediate{Register: 2, Data: addrBytes(realLast)},
		&expr.NAT{
			Type:       expr.NATTypeDestNAT,
			Family:     unix.NFPROTO_IPV4,
			RegAddrMin: 1,
			RegAddrMax: 2,
			Prefix:     true,
		},
	}
}

// masqueradeExprs builds `ip saddr <vpnPool> ip daddr <real> masquerade`: the
// return-path disguise that lets LAN hosts answer without knowing the VPN.
func masqueradeExprs(vpnPool netip.Prefix, real netip.Prefix) []expr.Any {
	pool := ipam.BlockFromPrefix(vpnPool)
	dst := ipam.BlockFromPrefix(real)
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: prefixMask(pool.PrefixLen), Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: addrBytes(pool.Base)},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: prefixMask(dst.PrefixLen), Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: addrBytes(dst.Base)},
		&expr.Masq{},
	}
}

func prefixMask(bits int) []byte {
	m := ^uint32(0) << (32 - bits)
	return []byte{byte(m >> 24), byte(m >> 16), byte(m >> 8), byte(m)}
}

func addrBytes(base uint32) []byte {
	a := ipam.Uint32ToAddr(base).As4()
	return a[:]
}
