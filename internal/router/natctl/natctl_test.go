package natctl_test

import (
	"net/netip"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"

	"lanweave/internal/router/natctl"
	"lanweave/internal/testutil"
)

var testPool = netip.MustParsePrefix("100.127.0.0/16")

func rule(synth, real string) natctl.Rule {
	return natctl.Rule{Synthetic: netip.MustParsePrefix(synth), Real: netip.MustParsePrefix(real)}
}

func cleanup(t *testing.T, table string) {
	t.Cleanup(func() { _ = natctl.Teardown(table) })
}

// TestPrefixDNATSpike is the D1 gate (privileged): the kernel must accept a
// dnat rule carrying the PREFIX (netmap) flag built via google/nftables, and
// the flag must survive a read-back. If this fails the whole approach falls
// back to exec'ing the nft binary (research D1).
func TestPrefixDNATSpike(t *testing.T) {
	testutil.RequireNetAdmin(t)
	const table = "lwspike"
	cleanup(t, table)

	if err := natctl.Rebuild(table, testPool, []natctl.Rule{rule("100.100.1.0/24", "192.168.50.0/24")}); err != nil {
		t.Fatalf("rebuild with prefix-dnat: %v", err)
	}

	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("chains: %v", err)
	}
	var pre *nftables.Chain
	for _, c := range chains {
		if c.Table != nil && c.Table.Name == table && c.Name == "prerouting" {
			pre = c
		}
	}
	if pre == nil {
		t.Fatal("prerouting chain missing")
	}
	rules, err := conn.GetRules(pre.Table, pre)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("prerouting rules = %d, want 1", len(rules))
	}
	foundNAT := false
	for _, e := range rules[0].Exprs {
		if nat, ok := e.(*expr.NAT); ok {
			foundNAT = true
			if nat.Type != expr.NATTypeDestNAT || !nat.Prefix {
				t.Errorf("NAT expr = type %v prefix %v, want DNAT with prefix flag", nat.Type, nat.Prefix)
			}
		}
	}
	if !foundNAT {
		t.Fatal("no NAT expression read back — prefix DNAT not accepted by kernel/library")
	}
}

// TestRebuildIdempotentAndConverging (privileged): Rebuild is a full swap —
// repeated calls converge, extra rules vanish, missing rules appear, and
// Current reads the mapping back via userdata.
func TestRebuildIdempotentAndConverging(t *testing.T) {
	testutil.RequireNetAdmin(t)
	const table = "lwrebuild"
	cleanup(t, table)

	r1 := rule("100.100.1.0/24", "192.168.50.0/24")
	r2 := rule("100.100.2.0/23", "10.8.0.0/23")

	if err := natctl.Rebuild(table, testPool, []natctl.Rule{r1, r2}); err != nil {
		t.Fatalf("rebuild 2: %v", err)
	}
	cur, err := natctl.Current(table)
	if err != nil || len(cur) != 2 {
		t.Fatalf("current = %v (%v), want 2 rules", cur, err)
	}

	// Idempotent: same desired set, same result.
	if err := natctl.Rebuild(table, testPool, []natctl.Rule{r1, r2}); err != nil {
		t.Fatalf("rebuild again: %v", err)
	}
	if cur, _ = natctl.Current(table); len(cur) != 2 {
		t.Fatalf("after idempotent rebuild: %d rules, want 2", len(cur))
	}

	// Converging: shrink to one — the extra rule must vanish.
	if err := natctl.Rebuild(table, testPool, []natctl.Rule{r1}); err != nil {
		t.Fatalf("rebuild 1: %v", err)
	}
	cur, _ = natctl.Current(table)
	if len(cur) != 1 || cur[0] != r1 {
		t.Fatalf("after shrink: %v, want exactly r1", cur)
	}

	// Empty desired set: chains stay, zero rules.
	if err := natctl.Rebuild(table, testPool, nil); err != nil {
		t.Fatalf("rebuild empty: %v", err)
	}
	if cur, _ = natctl.Current(table); len(cur) != 0 {
		t.Fatalf("after empty rebuild: %v, want none", cur)
	}
}

// TestTeardownIdempotent (privileged): teardown removes the table and is safe
// to repeat; Current on a missing table reads as empty.
func TestTeardownIdempotent(t *testing.T) {
	testutil.RequireNetAdmin(t)
	const table = "lwteardown"

	if err := natctl.Rebuild(table, testPool, []natctl.Rule{rule("100.100.9.0/24", "192.168.9.0/24")}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := natctl.Teardown(table); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if cur, err := natctl.Current(table); err != nil || len(cur) != 0 {
		t.Fatalf("current after teardown = %v (%v), want empty", cur, err)
	}
	if err := natctl.Teardown(table); err != nil {
		t.Fatalf("second teardown: %v", err)
	}
}
