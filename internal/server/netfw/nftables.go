package netfw

import (
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// Manager owns the relay's dedicated nftables isolation table (family inet).
type Manager struct {
	table string
}

// NewManager returns a manager for the named table (family inet).
func NewManager(table string) *Manager {
	return &Manager{table: table}
}

// ZoneState is one zone's id and member addresses, the desired nftables state.
type ZoneState struct {
	ID        int64
	MemberIPs []netip.Addr
}

func zoneSetName(id int64) string { return fmt.Sprintf("zone_%d", id) }

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
	for _, z := range zones {
		set := &nftables.Set{Table: table, Name: zoneSetName(z.ID), KeyType: nftables.TypeIPAddr}
		if err := conn.AddSet(set, ipElements(z.MemberIPs)); err != nil {
			return fmt.Errorf("add set %s: %w", set.Name, err)
		}
		conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: sameZoneAcceptExprs(set)})
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
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("add zone %d: %w", zoneID, err)
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
