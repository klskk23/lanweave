package netfw_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/nftables"

	"lanweave/internal/server/netfw"
	"lanweave/internal/testutil"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// TestRebuild is a privileged integration test: it builds the real nftables table
// and asserts the default-deny forward chain with no rules/sets, idempotently.
func TestRebuild(t *testing.T) {
	testutil.RequireNetAdmin(t)
	const table = "lanweavetest"
	m := netfw.NewManager(table)

	t.Cleanup(func() {
		conn, err := nftables.New()
		if err != nil {
			return
		}
		conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
		_ = conn.Flush()
	})

	if err := m.Rebuild(nil, quietLog()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	assertCleanTable(t, table)

	// Idempotent: a second rebuild over the existing table yields the same state.
	if err := m.Rebuild(nil, quietLog()); err != nil {
		t.Fatalf("rebuild (2nd): %v", err)
	}
	assertCleanTable(t, table)
}

func assertCleanTable(t *testing.T, table string) {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("nftables conn: %v", err)
	}

	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var found *nftables.Table
	for _, tb := range tables {
		if tb.Name == table {
			found = tb
		}
	}
	if found == nil {
		t.Fatalf("table %q not found", table)
	}

	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatalf("list chains: %v", err)
	}
	var fwd *nftables.Chain
	for _, c := range chains {
		if c.Table.Name == table && c.Name == "forward" {
			fwd = c
		}
	}
	if fwd == nil {
		t.Fatalf("forward chain not found in table %q", table)
	}
	if fwd.Policy == nil || *fwd.Policy != nftables.ChainPolicyDrop {
		t.Errorf("forward chain policy = %v, want drop", fwd.Policy)
	}

	// No rules, no sets.
	rules, err := conn.GetRules(found, fwd)
	if err != nil {
		t.Fatalf("get rules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
	sets, err := conn.GetSets(found)
	if err != nil {
		t.Fatalf("get sets: %v", err)
	}
	if len(sets) != 0 {
		t.Errorf("expected 0 sets, got %d", len(sets))
	}
}
