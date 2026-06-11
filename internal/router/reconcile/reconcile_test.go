package reconcile_test

import (
	"errors"
	"testing"

	"lanweave/internal/router/reconcile"
	"lanweave/pkg/protocol"
)

func zones(names ...string) []protocol.ZoneResponse {
	out := make([]protocol.ZoneResponse, 0, len(names))
	for i, n := range names {
		out = append(out, protocol.ZoneResponse{ID: int64(i + 1), Name: n})
	}
	return out
}

func TestDesired(t *testing.T) {
	lists := map[string][]protocol.AnnouncementResponse{
		"home": {
			{ID: 1, NodeID: 7, Subnet: "192.168.1.0/24", Synthetic: "100.100.1.0/24"},
			{ID: 2, NodeID: 99, Subnet: "192.168.2.0/24", Synthetic: "100.100.2.0/24"}, // someone else's
		},
		"office": {
			{ID: 1, NodeID: 7, Subnet: "192.168.1.0/24", Synthetic: "100.100.1.0/24"}, // same ann, 2nd zone
			{ID: 3, NodeID: 7, Subnet: "10.8.0.0/24", Synthetic: "100.100.3.0/24"},
		},
	}
	list := func(zone string) (protocol.AnnouncementListResponse, error) {
		return protocol.AnnouncementListResponse{Announcements: lists[zone]}, nil
	}

	rules, entries, err := reconcile.Desired(zones("home", "office"), list, 7)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(rules) != 2 || len(entries) != 2 {
		t.Fatalf("rules=%d entries=%d, want 2/2 (dedup + foreign filtered)", len(rules), len(entries))
	}
	if entries[0].ID != 1 || len(entries[0].Zones) != 2 || entries[0].Zones[0] != "home" {
		t.Errorf("entry 1 = %+v, want zones [home office]", entries[0])
	}
	if entries[1].ID != 3 || entries[1].Synthetic.String() != "100.100.3.0/24" {
		t.Errorf("entry 3 = %+v", entries[1])
	}
}

func TestDesiredEmptyAndErrors(t *testing.T) {
	// No zones → empty desired set.
	rules, entries, err := reconcile.Desired(nil, nil, 7)
	if err != nil || len(rules) != 0 || len(entries) != 0 {
		t.Fatalf("empty = %v/%v (%v)", rules, entries, err)
	}

	// A failing zone listing surfaces the error (the caller skips the cycle).
	boom := errors.New("api down")
	_, _, err = reconcile.Desired(zones("home"), func(string) (protocol.AnnouncementListResponse, error) {
		return protocol.AnnouncementListResponse{}, boom
	}, 7)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped api error", err)
	}

	// A malformed server CIDR is a protocol break, not silently skipped.
	bad := func(string) (protocol.AnnouncementListResponse, error) {
		return protocol.AnnouncementListResponse{
			Announcements: []protocol.AnnouncementResponse{{ID: 1, NodeID: 7, Subnet: "not-a-cidr", Synthetic: "100.100.1.0/24"}},
		}, nil
	}
	if _, _, err := reconcile.Desired(zones("home"), bad, 7); err == nil {
		t.Fatal("malformed subnet accepted")
	}
}
