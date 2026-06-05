package netfw

import (
	"fmt"
	"log/slog"

	"github.com/google/nftables"
)

// Manager owns the relay's dedicated nftables isolation table (family inet).
type Manager struct {
	table string
}

// NewManager returns a manager for the named table (family inet).
func NewManager(table string) *Manager {
	return &Manager{table: table}
}

// Rebuild brings the isolation table to a deterministic clean state: the table
// with a single `forward` chain whose policy is drop and which holds no sets and
// no rules. Any pre-existing/stale table of the same name is removed first, so
// the rebuild is idempotent and the database remains the single source of truth.
//
// Feature 005 will extend this to populate zone sets and accept rules; in this
// feature the desired state is empty (default-deny).
func (m *Manager) Rebuild(log *slog.Logger) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}

	// Remove a stale table of the same name, if present, and commit the delete.
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

	// Create the table + default-deny forward chain in one flush.
	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyINet,
		Name:   m.table,
	})
	policy := nftables.ChainPolicyDrop
	conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &policy,
	})
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("create nftables table %q: %w", m.table, err)
	}

	log.Info("nftables isolation table rebuilt", "table", m.table, "policy", "drop")
	return nil
}
