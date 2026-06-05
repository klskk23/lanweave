package netfw_test

import (
	"net/netip"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"

	"lanweave/internal/server/netfw"
	"lanweave/internal/testutil"
)

// TestZoneSetsAndRules is a privileged integration test asserting the real kernel
// ruleset: per-zone set + the same-zone accept rule (both saddr AND daddr in the
// set), incremental add/remove, and full rebuild.
func TestZoneSetsAndRules(t *testing.T) {
	testutil.RequireNetAdmin(t)
	const table = "lwzonetest"
	m := netfw.NewManager(table)
	t.Cleanup(func() {
		if conn, err := nftables.New(); err == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	if err := m.Rebuild(nil, quietLog()); err != nil {
		t.Fatalf("rebuild empty: %v", err)
	}
	if err := m.AddZone(1); err != nil {
		t.Fatalf("add zone: %v", err)
	}
	if err := m.AddMember(1, netip.MustParseAddr("100.127.0.2")); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := m.AddMember(1, netip.MustParseAddr("100.127.0.3")); err != nil {
		t.Fatalf("add member 2: %v", err)
	}

	if got := setElementCount(t, table, "zone_1"); got != 2 {
		t.Errorf("zone_1 element count = %d, want 2", got)
	}
	// Exact rule shape: the accept rule must look up the set on BOTH source and
	// destination (a rule with only one lookup would silently break isolation).
	if got := lookupCountForSet(t, table, "zone_1"); got != 2 {
		t.Errorf("zone_1 accept rule has %d set lookups, want 2 (saddr + daddr)", got)
	}

	if err := m.RemoveMember(1, netip.MustParseAddr("100.127.0.2")); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if got := setElementCount(t, table, "zone_1"); got != 1 {
		t.Errorf("after remove, zone_1 count = %d, want 1", got)
	}

	// Full rebuild reproduces exactly the given sets.
	if err := m.Rebuild([]netfw.ZoneState{
		{ID: 1, MemberIPs: []netip.Addr{netip.MustParseAddr("100.127.0.2"), netip.MustParseAddr("100.127.0.3")}},
		{ID: 2, MemberIPs: []netip.Addr{netip.MustParseAddr("100.127.0.5")}},
	}, quietLog()); err != nil {
		t.Fatalf("rebuild zones: %v", err)
	}
	if got := setElementCount(t, table, "zone_1"); got != 2 {
		t.Errorf("rebuilt zone_1 = %d, want 2", got)
	}
	if got := setElementCount(t, table, "zone_2"); got != 1 {
		t.Errorf("rebuilt zone_2 = %d, want 1", got)
	}
	if got := lookupCountForSet(t, table, "zone_2"); got != 2 {
		t.Errorf("zone_2 accept rule lookups = %d, want 2", got)
	}
}

func setElementCount(t *testing.T, table, set string) int {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	s, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: table}, set)
	if err != nil {
		t.Fatalf("get set %s: %v", set, err)
	}
	elems, err := conn.GetSetElements(s)
	if err != nil {
		t.Fatalf("get elements %s: %v", set, err)
	}
	return len(elems)
}

// lookupCountForSet returns how many set-lookups in the forward chain reference the
// named set (the same-zone accept rule should reference it twice: saddr + daddr).
func lookupCountForSet(t *testing.T, table, set string) int {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("list chains: %v", err)
	}
	var chain *nftables.Chain
	for _, c := range chains {
		if c.Table != nil && c.Table.Name == table && c.Name == "forward" {
			chain = c
		}
	}
	if chain == nil {
		t.Fatalf("forward chain not found in %s", table)
	}
	rules, err := conn.GetRules(chain.Table, chain)
	if err != nil {
		t.Fatalf("get rules: %v", err)
	}
	count := 0
	for _, rule := range rules {
		for _, e := range rule.Exprs {
			if lk, ok := e.(*expr.Lookup); ok && lk.SetName == set {
				count++
			}
		}
	}
	return count
}
