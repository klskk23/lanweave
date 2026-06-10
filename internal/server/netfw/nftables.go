package netfw

import (
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"lanweave/internal/server/ipam"
)

// Manager owns the relay's dedicated nftables isolation table (family inet).
type Manager struct {
	table string
}

// NewManager returns a manager for the named table (family inet).
func NewManager(table string) *Manager {
	return &Manager{table: table}
}

// ZoneState is one zone's id, member addresses and announced synthetic route
// CIDRs — the desired nftables state.
type ZoneState struct {
	ID        int64
	MemberIPs []netip.Addr
	// RouteCIDRs are the synthetic blocks announced into this zone (feature 030):
	// members may open connections toward them; replies ride conntrack.
	RouteCIDRs []netip.Prefix
}

func zoneSetName(id int64) string   { return fmt.Sprintf("zone_%d", id) }
func routesSetName(id int64) string { return fmt.Sprintf("zone_%d_routes", id) }

// newRoutesSet returns the (empty) interval set holding a zone's announced
// synthetic CIDRs. Interval sets store ranges, which is how nftables expresses
// CIDR membership.
func newRoutesSet(table *nftables.Table, zoneID int64) *nftables.Set {
	return &nftables.Set{Table: table, Name: routesSetName(zoneID), KeyType: nftables.TypeIPAddr, Interval: true}
}

// Rebuild brings the isolation table to a deterministic state matching the given
// zones: the table + a default-drop forward chain, and for each zone a set of
// member addresses plus a rule that accepts traffic whose source AND destination
// are both in that set. Any stale table is dropped first (idempotent). With no
// zones this is the empty default-deny skeleton (feature 003).
func (m *Manager) Rebuild(zones []ZoneState, log *slog.Logger) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}

	existing, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("list nftables tables: %w", err)
	}
	for _, t := range existing {
		if t.Name == m.table {
			conn.DelTable(t)
		}
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("clear stale nftables table: %w", err)
	}

	table := conn.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: m.table})
	policy := nftables.ChainPolicyDrop
	chain := conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &policy,
	})
	// Return traffic from announced synthetic subnets rides conntrack: replies
	// match established/related, while LAN-side initiated flows have no entry and
	// fall through to the drop policy — the one-way semantics of feature 030.
	conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: ctEstablishedAcceptExprs()})
	for _, z := range zones {
		set := &nftables.Set{Table: table, Name: zoneSetName(z.ID), KeyType: nftables.TypeIPAddr}
		if err := conn.AddSet(set, ipElements(z.MemberIPs)); err != nil {
			return fmt.Errorf("add set %s: %w", set.Name, err)
		}
		conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: sameZoneAcceptExprs(set)})
		routes := newRoutesSet(table, z.ID)
		if err := conn.AddSet(routes, nil); err != nil {
			return fmt.Errorf("add set %s: %w", routes.Name, err)
		}
		for _, p := range z.RouteCIDRs {
			if err := conn.SetAddElements(routes, rangeElements(p)); err != nil {
				return fmt.Errorf("add route %s to %s: %w", p, routes.Name, err)
			}
		}
		conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: zoneRouteAcceptExprs(set, routes)})
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("build nftables table %q: %w", m.table, err)
	}
	if log != nil {
		log.Info("nftables isolation table rebuilt", "table", m.table, "zones", len(zones))
	}
	return nil
}

// AddZone installs an empty set + same-zone accept rule for a newly created zone.
func (m *Manager) AddZone(zoneID int64) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	table, chain, err := m.tableAndChain(conn)
	if err != nil {
		return err
	}
	set := &nftables.Set{Table: table, Name: zoneSetName(zoneID), KeyType: nftables.TypeIPAddr}
	if err := conn.AddSet(set, nil); err != nil {
		return fmt.Errorf("add set for zone %d: %w", zoneID, err)
	}
	conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: sameZoneAcceptExprs(set)})
	routes := newRoutesSet(table, zoneID)
	if err := conn.AddSet(routes, nil); err != nil {
		return fmt.Errorf("add routes set for zone %d: %w", zoneID, err)
	}
	conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: zoneRouteAcceptExprs(set, routes)})
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("add zone %d: %w", zoneID, err)
	}
	return nil
}

// AddZoneRoute adds an announced synthetic CIDR to a zone's routes set.
func (m *Manager) AddZoneRoute(zoneID int64, prefix netip.Prefix) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	set, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: m.table}, routesSetName(zoneID))
	if err != nil {
		return fmt.Errorf("get routes set for zone %d: %w", zoneID, err)
	}
	if err := conn.SetAddElements(set, rangeElements(prefix)); err != nil {
		return fmt.Errorf("add route: %w", err)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("add route %s to zone %d: %w", prefix, zoneID, err)
	}
	return nil
}

// RemoveZoneRoute removes an announced synthetic CIDR from a zone's routes set.
func (m *Manager) RemoveZoneRoute(zoneID int64, prefix netip.Prefix) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	set, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: m.table}, routesSetName(zoneID))
	if err != nil {
		return fmt.Errorf("get routes set for zone %d: %w", zoneID, err)
	}
	if err := conn.SetDeleteElements(set, rangeElements(prefix)); err != nil {
		return fmt.Errorf("remove route: %w", err)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("remove route %s from zone %d: %w", prefix, zoneID, err)
	}
	return nil
}

// AddMember adds a node address to a zone's set.
func (m *Manager) AddMember(zoneID int64, ip netip.Addr) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	set, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: m.table}, zoneSetName(zoneID))
	if err != nil {
		return fmt.Errorf("get set for zone %d: %w", zoneID, err)
	}
	if err := conn.SetAddElements(set, ipElements([]netip.Addr{ip})); err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("add member to zone %d: %w", zoneID, err)
	}
	return nil
}

// RemoveMember removes a node address from a zone's set.
func (m *Manager) RemoveMember(zoneID int64, ip netip.Addr) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	set, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: m.table}, zoneSetName(zoneID))
	if err != nil {
		return fmt.Errorf("get set for zone %d: %w", zoneID, err)
	}
	if err := conn.SetDeleteElements(set, ipElements([]netip.Addr{ip})); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("remove member from zone %d: %w", zoneID, err)
	}
	return nil
}

// DeleteZone removes a zone's accept rule(s) and then its set. The rule MUST be
// deleted before the set it references (the kernel rejects deleting a referenced
// set). The deleted rules are the exact objects returned by GetRules (they carry
// the handles DelRule needs).
func (m *Manager) DeleteZone(zoneID int64) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	table, chain, err := m.tableAndChain(conn)
	if err != nil {
		return err
	}
	setNames := []string{zoneSetName(zoneID), routesSetName(zoneID)}

	rules, err := conn.GetRules(table, chain)
	if err != nil {
		return fmt.Errorf("get rules: %w", err)
	}
	deletedRule := false
	for _, rule := range rules {
		for _, setName := range setNames {
			if ruleReferencesSet(rule, setName) {
				if err := conn.DelRule(rule); err != nil {
					return fmt.Errorf("delete zone rule: %w", err)
				}
				deletedRule = true
				break
			}
		}
	}
	if deletedRule {
		if err := conn.Flush(); err != nil {
			return fmt.Errorf("flush rule delete: %w", err)
		}
	}

	for _, setName := range setNames {
		if set, err := conn.GetSetByName(table, setName); err == nil {
			conn.DelSet(set)
			if err := conn.Flush(); err != nil {
				return fmt.Errorf("delete set %s: %w", setName, err)
			}
		}
	}
	return nil
}

func ruleReferencesSet(rule *nftables.Rule, setName string) bool {
	for _, e := range rule.Exprs {
		if lk, ok := e.(*expr.Lookup); ok && lk.SetName == setName {
			return true
		}
	}
	return false
}

func (m *Manager) tableAndChain(conn *nftables.Conn) (*nftables.Table, *nftables.Chain, error) {
	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		return nil, nil, fmt.Errorf("list chains: %w", err)
	}
	for _, c := range chains {
		if c.Table != nil && c.Table.Name == m.table && c.Name == "forward" {
			return c.Table, c, nil
		}
	}
	return nil, nil, fmt.Errorf("forward chain not found in table %q", m.table)
}

func ipElements(ips []netip.Addr) []nftables.SetElement {
	out := make([]nftables.SetElement, 0, len(ips))
	for _, ip := range ips {
		b := ip.As4()
		out = append(out, nftables.SetElement{Key: b[:]})
	}
	return out
}

// sameZoneAcceptExprs builds `ip saddr @set && ip daddr @set accept`, guarded by
// nfproto ipv4 (the table is inet, so it also sees v6).
func sameZoneAcceptExprs(set *nftables.Set) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4}, // IPv4 saddr
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4}, // IPv4 daddr
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// zoneRouteAcceptExprs builds `ip saddr @members ip daddr @routes accept`: zone
// members may open connections toward the zone's announced synthetic CIDRs. The
// reverse direction is intentionally absent (one-way semantics, feature 030).
func zoneRouteAcceptExprs(members, routes *nftables.Set) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4}, // IPv4 saddr
		&expr.Lookup{SourceRegister: 1, SetName: members.Name, SetID: members.ID},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4}, // IPv4 daddr
		&expr.Lookup{SourceRegister: 1, SetName: routes.Name, SetID: routes.ID},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// ctEstablishedAcceptExprs builds `ct state established,related accept` — the
// return path for member-initiated flows toward announced subnets.
func ctEstablishedAcceptExprs() []expr.Any {
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            4,
			Mask:           binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
			Xor:            binaryutil.NativeEndian.PutUint32(0),
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}
}

// rangeElements expresses an IPv4 CIDR as interval-set elements: the inclusive
// start plus the exclusive end marked IntervalEnd, which is how nftables
// interval sets encode ranges.
func rangeElements(p netip.Prefix) []nftables.SetElement {
	b := ipam.BlockFromPrefix(p)
	start := ipam.Uint32ToAddr(b.Base).As4()
	endExclusive := uint64(b.Base) + (uint64(1) << (32 - b.PrefixLen))
	// A block ending at the address-space top would wrap; clamp to the all-ones
	// address as interval end (exclusive end 2^32 == 0.0.0.0 wrapped, never valid
	// for our pools but guarded anyway).
	var end [4]byte
	if endExclusive > 0xFFFFFFFF {
		end = [4]byte{0xFF, 0xFF, 0xFF, 0xFF}
	} else {
		end = ipam.Uint32ToAddr(uint32(endExclusive)).As4()
	}
	return []nftables.SetElement{
		{Key: start[:]},
		{Key: end[:], IntervalEnd: true},
	}
}
