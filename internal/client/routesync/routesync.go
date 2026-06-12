// Package routesync aggregates the zones' announcement lists into the two
// client-side views of feature 030's synthetic subnets: the announcer view
// (this node's own announcements → NAT rules, feature 032) and the consumer
// view (everyone else's announcements → tunnel routes, feature 033). One
// shared aggregation keeps the Windows panel and the OpenWrt router seeing
// the same reachable set — the FR-009 cross-platform guarantee is mechanical,
// not coincidental. Pure data transformation; the server list is the single
// source of truth.
package routesync

import (
	"fmt"
	"net/netip"
	"sort"

	"lanweave/pkg/protocol"
)

// Entry is one announcement aggregated across zones (the same announcement
// attached to several zones is one entry — 030 reuses the synthetic block).
type Entry struct {
	ID        int64
	Subnet    netip.Prefix
	Synthetic netip.Prefix
	Zones     []string
	NodeID    int64
	Announcer string
}

// Fetch aggregates all announcements of the given zones, deduped by
// announcement id, zones sorted. A malformed CIDR from the server is a
// protocol break, not a user mistake — it is reported, not skipped.
func Fetch(zones []protocol.ZoneResponse, list func(zone string) (protocol.AnnouncementListResponse, error)) ([]Entry, error) {
	byID := map[int64]*Entry{}
	for _, z := range zones {
		resp, err := list(z.Name)
		if err != nil {
			return nil, fmt.Errorf("list announcements for zone %s: %w", z.Name, err)
		}
		for _, a := range resp.Announcements {
			if e, ok := byID[a.ID]; ok {
				e.Zones = append(e.Zones, z.Name)
				continue
			}
			real, err := netip.ParsePrefix(a.Subnet)
			if err != nil {
				return nil, fmt.Errorf("server sent invalid subnet %q: %w", a.Subnet, err)
			}
			synth, err := netip.ParsePrefix(a.Synthetic)
			if err != nil {
				return nil, fmt.Errorf("server sent invalid synthetic %q: %w", a.Synthetic, err)
			}
			byID[a.ID] = &Entry{
				ID: a.ID, Subnet: real, Synthetic: synth,
				Zones: []string{z.Name}, NodeID: a.NodeID, Announcer: a.NodeName,
			}
		}
	}
	entries := make([]Entry, 0, len(byID))
	for _, e := range byID {
		sort.Strings(e.Zones)
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// Mine is the announcer view: this node's own entries (NAT translation).
func Mine(entries []Entry, nodeID int64) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.NodeID == nodeID {
			out = append(out, e)
		}
	}
	return out
}

// Consumed is the consumer view: other nodes' entries, deduped by synthetic
// block. Excluding this node's own announcements avoids hair-pinning traffic
// to the locally attached subnet through the server.
func Consumed(entries []Entry, nodeID int64) []Entry {
	seen := map[netip.Prefix]bool{}
	var out []Entry
	for _, e := range entries {
		if e.NodeID == nodeID || seen[e.Synthetic] {
			continue
		}
		seen[e.Synthetic] = true
		out = append(out, e)
	}
	return out
}

// Prefixes extracts the synthetic blocks of the given entries.
func Prefixes(entries []Entry) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Synthetic)
	}
	return out
}
