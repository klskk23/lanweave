// Package reconcile computes the announcer's desired translation state from
// the single source of truth — the server's announcement lists — so the local
// NAT table can be rebuilt to match (feature 032). Pure data transformation;
// no kernel or network side effects live here.
package reconcile

import (
	"fmt"
	"net/netip"
	"sort"

	"lanweave/internal/router/natctl"
	"lanweave/pkg/protocol"
)

// Entry is one of this node's announcements, aggregated across zones (the
// same announcement attached to several zones is one entry — 030 reuses the
// synthetic block).
type Entry struct {
	ID        int64
	Subnet    netip.Prefix
	Synthetic netip.Prefix
	Zones     []string
}

// Desired aggregates the zones' announcement lists, keeps this node's entries,
// dedupes by announcement id and returns both the NAT rule set and the display
// entries. A malformed CIDR from the server is reported as an error (it would
// mean a protocol break, not a user mistake).
func Desired(zones []protocol.ZoneResponse, list func(zone string) (protocol.AnnouncementListResponse, error), nodeID int64) ([]natctl.Rule, []Entry, error) {
	byID := map[int64]*Entry{}
	for _, z := range zones {
		resp, err := list(z.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("list announcements for zone %s: %w", z.Name, err)
		}
		for _, a := range resp.Announcements {
			if a.NodeID != nodeID {
				continue
			}
			if e, ok := byID[a.ID]; ok {
				e.Zones = append(e.Zones, z.Name)
				continue
			}
			real, err := netip.ParsePrefix(a.Subnet)
			if err != nil {
				return nil, nil, fmt.Errorf("server sent invalid subnet %q: %w", a.Subnet, err)
			}
			synth, err := netip.ParsePrefix(a.Synthetic)
			if err != nil {
				return nil, nil, fmt.Errorf("server sent invalid synthetic %q: %w", a.Synthetic, err)
			}
			byID[a.ID] = &Entry{ID: a.ID, Subnet: real, Synthetic: synth, Zones: []string{z.Name}}
		}
	}

	entries := make([]Entry, 0, len(byID))
	for _, e := range byID {
		sort.Strings(e.Zones)
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	rules := make([]natctl.Rule, 0, len(entries))
	for _, e := range entries {
		rules = append(rules, natctl.Rule{Synthetic: e.Synthetic, Real: e.Subnet})
	}
	return rules, entries, nil
}
