// Package reconcile computes the announcer's desired translation state from
// the single source of truth — the server's announcement lists — so the local
// NAT table can be rebuilt to match (feature 032). Since feature 033 the
// aggregation itself lives in the shared routesync package (one fetch feeds
// both the announcer view here and the consumer route view); this package
// keeps the NAT-facing shape.
package reconcile

import (
	"net/netip"

	"lanweave/internal/client/routesync"
	"lanweave/internal/router/natctl"
	"lanweave/pkg/protocol"
)

// Entry is one of this node's announcements, aggregated across zones.
type Entry struct {
	ID        int64
	Subnet    netip.Prefix
	Synthetic netip.Prefix
	Zones     []string
}

// Desired returns this node's NAT rule set and display entries (the announcer
// view of routesync.Fetch).
func Desired(zones []protocol.ZoneResponse, list func(zone string) (protocol.AnnouncementListResponse, error), nodeID int64) ([]natctl.Rule, []Entry, error) {
	all, err := routesync.Fetch(zones, list)
	if err != nil {
		return nil, nil, err
	}
	mine := routesync.Mine(all, nodeID)
	entries := make([]Entry, 0, len(mine))
	rules := make([]natctl.Rule, 0, len(mine))
	for _, e := range mine {
		entries = append(entries, Entry{ID: e.ID, Subnet: e.Subnet, Synthetic: e.Synthetic, Zones: e.Zones})
		rules = append(rules, natctl.Rule{Synthetic: e.Synthetic, Real: e.Subnet})
	}
	return rules, entries, nil
}
