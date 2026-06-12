package routesync_test

import (
	"errors"
	"net/netip"
	"testing"

	"lanweave/internal/client/routesync"
	"lanweave/pkg/protocol"
)

func zones(names ...string) []protocol.ZoneResponse {
	out := make([]protocol.ZoneResponse, 0, len(names))
	for i, n := range names {
		out = append(out, protocol.ZoneResponse{ID: int64(i + 1), Name: n})
	}
	return out
}

var lists = map[string][]protocol.AnnouncementResponse{
	"home": {
		{ID: 1, NodeID: 7, NodeName: "gw", Subnet: "192.168.1.0/24", Synthetic: "100.100.1.0/24"},
		{ID: 2, NodeID: 9, NodeName: "bob-router", Subnet: "10.8.0.0/24", Synthetic: "100.100.2.0/24"},
	},
	"office": {
		{ID: 1, NodeID: 7, NodeName: "gw", Subnet: "192.168.1.0/24", Synthetic: "100.100.1.0/24"}, // same ann, 2nd zone
		{ID: 3, NodeID: 9, NodeName: "bob-router", Subnet: "172.16.0.0/24", Synthetic: "100.100.3.0/24"},
	},
}

func list(zone string) (protocol.AnnouncementListResponse, error) {
	return protocol.AnnouncementListResponse{Announcements: lists[zone]}, nil
}

func TestFetchAggregatesAndDedupes(t *testing.T) {
	entries, err := routesync.Fetch(zones("home", "office"), list)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (id-dedup across zones)", len(entries))
	}
	if entries[0].ID != 1 || len(entries[0].Zones) != 2 || entries[0].Zones[0] != "home" || entries[0].Announcer != "gw" {
		t.Errorf("entry 1 = %+v, want zones [home office], announcer gw", entries[0])
	}
}

func TestMineAndConsumedViews(t *testing.T) {
	entries, _ := routesync.Fetch(zones("home", "office"), list)

	mine := routesync.Mine(entries, 7)
	if len(mine) != 1 || mine[0].ID != 1 {
		t.Errorf("mine(7) = %+v, want only ann 1", mine)
	}

	consumed := routesync.Consumed(entries, 7)
	if len(consumed) != 2 {
		t.Fatalf("consumed(7) = %d entries, want 2 (bob's two)", len(consumed))
	}
	for _, e := range consumed {
		if e.NodeID == 7 {
			t.Errorf("own announcement leaked into consumer view: %+v", e)
		}
	}

	// Windows-style consumer (nodeID present but never announces, or zero on
	// pre-v3 state): gets everything.
	if got := routesync.Consumed(entries, 0); len(got) != 3 {
		t.Errorf("consumed(0) = %d, want all 3", len(got))
	}

	p := routesync.Prefixes(consumed)
	if len(p) != 2 || p[0] != netip.MustParsePrefix("100.100.2.0/24") {
		t.Errorf("prefixes = %v", p)
	}
}

func TestConsumedDedupesBySynthetic(t *testing.T) {
	// Two distinct announcement ids sharing one synthetic block must yield one
	// consumer entry (defensive: the server never does this today).
	entries := []routesync.Entry{
		{ID: 1, NodeID: 9, Synthetic: netip.MustParsePrefix("100.100.5.0/24")},
		{ID: 2, NodeID: 10, Synthetic: netip.MustParsePrefix("100.100.5.0/24")},
	}
	if got := routesync.Consumed(entries, 7); len(got) != 1 {
		t.Errorf("consumed = %d, want 1 (synthetic dedup)", len(got))
	}
}

func TestFetchErrors(t *testing.T) {
	// Empty zones → empty.
	if entries, err := routesync.Fetch(nil, nil); err != nil || len(entries) != 0 {
		t.Fatalf("empty = %v (%v)", entries, err)
	}
	// Listing failure surfaces.
	boom := errors.New("api down")
	_, err := routesync.Fetch(zones("home"), func(string) (protocol.AnnouncementListResponse, error) {
		return protocol.AnnouncementListResponse{}, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped", err)
	}
	// Malformed server CIDR is a protocol break.
	bad := func(string) (protocol.AnnouncementListResponse, error) {
		return protocol.AnnouncementListResponse{
			Announcements: []protocol.AnnouncementResponse{{ID: 1, Subnet: "junk", Synthetic: "100.100.1.0/24"}},
		}, nil
	}
	if _, err := routesync.Fetch(zones("home"), bad); err == nil {
		t.Fatal("malformed subnet accepted")
	}
}

// TestMemberZones: only zones containing THIS node (by VPN IP) survive —
// user-scoped ListZones must not leak sibling devices' zones into the
// consumer view (the per-node AllowedIPs contract, 030).
func TestMemberZones(t *testing.T) {
	members := func(zone string) (protocol.ZoneMembersResponse, error) {
		switch zone {
		case "mine":
			return protocol.ZoneMembersResponse{Members: []protocol.ZoneMemberResponse{
				{NodeID: 5, IP: "100.127.0.5"}, {NodeID: 7, IP: "100.127.0.7"},
			}}, nil
		case "siblings-only":
			return protocol.ZoneMembersResponse{Members: []protocol.ZoneMemberResponse{
				{NodeID: 7, IP: "100.127.0.7"},
			}}, nil
		default:
			return protocol.ZoneMembersResponse{}, errors.New("api down")
		}
	}
	got := routesync.MemberZones(zones("mine", "siblings-only", "flaky"), members, "100.127.0.5")
	if len(got) != 1 || got[0].Name != "mine" {
		t.Fatalf("member zones = %v, want only [mine]", got)
	}
}
