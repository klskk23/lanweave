package netfw_test

import (
	"net/netip"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"

	"lanweave/internal/server/netfw"
	"lanweave/internal/testutil"
)

// TestZoneRoutes (privileged) covers the announced-subnet dataplane shape: the
// per-zone interval routes set, incremental add/remove of synthetic CIDRs, the
// member→routes accept rule, and the baseline ct established/related rule at the
// head of the forward chain.
func TestZoneRoutes(t *testing.T) {
	testutil.RequireNetAdmin(t)
	const table = "lwroutestest"
	m := netfw.NewManager(table)
	t.Cleanup(func() {
		if conn, err := nftables.New(); err == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	if err := m.Rebuild(nil, quietLog()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := m.AddZone(1); err != nil {
		t.Fatalf("add zone: %v", err)
	}

	// The routes set exists, is empty, and the member→routes rule references it.
	if got := setElementCount(t, table, "zone_1_routes"); got != 0 {
		t.Errorf("fresh routes set has %d elements, want 0", got)
	}
	if got := lookupCountForSet(t, table, "zone_1_routes"); got != 1 {
		t.Errorf("zone_1_routes lookups = %d, want 1 (daddr in route rule)", got)
	}

	// Incremental add/remove of synthetic CIDRs (interval elements come back as
	// start + interval-end pairs, so one CIDR reads as 2 elements).
	p1 := netip.MustParsePrefix("100.100.1.0/24")
	p2 := netip.MustParsePrefix("100.100.2.0/23")
	if err := m.AddZoneRoute(1, p1); err != nil {
		t.Fatalf("add route: %v", err)
	}
	if err := m.AddZoneRoute(1, p2); err != nil {
		t.Fatalf("add route 2: %v", err)
	}
	if got := setElementCount(t, table, "zone_1_routes"); got != 4 {
		t.Errorf("routes elements = %d, want 4 (2 CIDRs x start+end)", got)
	}
	if err := m.RemoveZoneRoute(1, p1); err != nil {
		t.Fatalf("remove route: %v", err)
	}
	if got := setElementCount(t, table, "zone_1_routes"); got != 2 {
		t.Errorf("after remove, routes elements = %d, want 2", got)
	}

	// Rebuild reproduces routes from the given desired state.
	if err := m.Rebuild([]netfw.ZoneState{
		{
			ID:         1,
			MemberIPs:  []netip.Addr{netip.MustParseAddr("100.127.0.2")},
			RouteCIDRs: []netip.Prefix{p1, p2},
		},
	}, quietLog()); err != nil {
		t.Fatalf("rebuild with routes: %v", err)
	}
	if got := setElementCount(t, table, "zone_1_routes"); got != 4 {
		t.Errorf("rebuilt routes elements = %d, want 4", got)
	}
	assertCtRuleFirst(t, table)
}

// assertCtRuleFirst asserts the first forward-chain rule is the conntrack
// established/related accept — the return path that makes announced-subnet
// traffic one-way (new flows from the LAN side fall through to drop).
func assertCtRuleFirst(t *testing.T, table string) {
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
		t.Fatalf("forward chain not found")
	}
	rules, err := conn.GetRules(chain.Table, chain)
	if err != nil {
		t.Fatalf("get rules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no rules in forward chain")
	}
	for _, e := range rules[0].Exprs {
		if ct, ok := e.(*expr.Ct); ok && ct.Key == expr.CtKeySTATE {
			return
		}
	}
	t.Error("first forward rule is not the ct established/related accept")
}
