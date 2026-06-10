package store_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"lanweave/internal/server/ipam"
	"lanweave/internal/server/store"
)

var announcePool = netip.MustParsePrefix("100.100.0.0/16")

func realBlock(t *testing.T, cidr string) ipam.Block {
	t.Helper()
	return ipam.BlockFromPrefix(netip.MustParsePrefix(cidr))
}

// announceFixture seeds a user, two nodes and a zone with both nodes joined,
// returning (store, userID, nodeA, nodeB, zoneID).
func announceFixture(t *testing.T) (*store.Store, int64, int64, int64, int64) {
	t.Helper()
	st := newStore(t)
	ctx := context.Background()
	u, err := st.Users().CreateAdmin(ctx, "alice", "hash")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	nodeA, err := st.Nodes().Create(ctx, u.ID, "router-a", "pk-a", "openwrt", first, last, 0)
	if err != nil {
		t.Fatalf("node a: %v", err)
	}
	nodeB, err := st.Nodes().Create(ctx, u.ID, "router-b", "pk-b", "openwrt", first, last, 0)
	if err != nil {
		t.Fatalf("node b: %v", err)
	}
	z, err := st.Zones().Create(ctx, u.ID, "home", "hash", 0)
	if err != nil {
		t.Fatalf("zone: %v", err)
	}
	for _, n := range []int64{nodeA.ID, nodeB.ID} {
		if err := st.Zones().Join(ctx, z.ID, n); err != nil {
			t.Fatalf("join: %v", err)
		}
	}
	return st, u.ID, nodeA.ID, nodeB.ID, z.ID
}

func TestAnnouncementCreateAllocatesAndAttaches(t *testing.T) {
	st, uid, nodeA, _, zone := announceFixture(t)
	ctx := context.Background()

	ann, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ann.Synthetic.PrefixLen != 24 {
		t.Errorf("synthetic prefix len = %d, want equal-length 24", ann.Synthetic.PrefixLen)
	}
	if !announcePool.Contains(ann.Synthetic.Prefix().Addr()) {
		t.Errorf("synthetic %s outside pool %s", ann.Synthetic.Prefix(), announcePool)
	}
	list, err := st.Announcements().ListByZone(ctx, zone)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].NodeName != "router-a" || list[0].Owner != "alice" {
		t.Fatalf("list = %+v, want one row with display fields", list)
	}
}

func TestAnnouncementCrossNodeSameSubnetAllowed(t *testing.T) {
	st, uid, nodeA, nodeB, zone := announceFixture(t)
	ctx := context.Background()

	a1, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool)
	if err != nil {
		t.Fatalf("node a announce: %v", err)
	}
	// The core value of synthetic mapping: a different node announcing the very
	// same real subnet must succeed with a distinct synthetic block.
	a2, _, err := st.Announcements().Create(ctx, uid, nodeB, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool)
	if err != nil {
		t.Fatalf("node b same subnet: %v", err)
	}
	if a1.Synthetic == a2.Synthetic {
		t.Errorf("both announcements share synthetic block %v", a1.Synthetic)
	}
	// And the first announcement is untouched (no preemption).
	got, err := st.Announcements().Get(ctx, a1.ID)
	if err != nil || got.Synthetic != a1.Synthetic {
		t.Errorf("a1 after a2 = %+v (%v), want unchanged", got, err)
	}
}

func TestAnnouncementSelfOverlapRejected(t *testing.T) {
	st, uid, nodeA, _, zone := announceFixture(t)
	ctx := context.Background()

	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.128/25"), zone, 0, announcePool)
	if !errors.Is(err, store.ErrSubnetOverlap) {
		t.Fatalf("self overlap err = %v, want ErrSubnetOverlap", err)
	}
}

func TestAnnouncementMultiZoneReuse(t *testing.T) {
	st, uid, nodeA, _, zone := announceFixture(t)
	ctx := context.Background()
	z2, err := st.Zones().Create(ctx, uid, "office", "hash", 0)
	if err != nil {
		t.Fatalf("zone2: %v", err)
	}
	if err := st.Zones().Join(ctx, z2.ID, nodeA); err != nil {
		t.Fatalf("join z2: %v", err)
	}

	a1, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool)
	if err != nil {
		t.Fatalf("announce zone1: %v", err)
	}
	a2, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), z2.ID, 0, announcePool)
	if err != nil {
		t.Fatalf("announce zone2: %v", err)
	}
	if a1.ID != a2.ID || a1.Synthetic != a2.Synthetic {
		t.Errorf("multi-zone announce did not reuse: %+v vs %+v", a1, a2)
	}
	// Re-attaching the same zone is idempotent.
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool); err != nil {
		t.Errorf("idempotent re-attach: %v", err)
	}
	// Quota counts announcements, not attachments: with limit 1 this user can
	// still attach the same announcement anywhere, but not announce a new subnet.
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.50.0/24"), zone, 1, announcePool); !errors.Is(err, store.ErrAnnounceLimit) {
		t.Errorf("limit err = %v, want ErrAnnounceLimit", err)
	}
}

func TestAnnouncementQuotaAndUnlimited(t *testing.T) {
	st, uid, nodeA, _, zone := announceFixture(t)
	ctx := context.Background()

	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 2, announcePool); err != nil {
		t.Fatalf("1st: %v", err)
	}
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.2.0/24"), zone, 2, announcePool); err != nil {
		t.Fatalf("2nd: %v", err)
	}
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.3.0/24"), zone, 2, announcePool); !errors.Is(err, store.ErrAnnounceLimit) {
		t.Fatalf("over quota err = %v, want ErrAnnounceLimit", err)
	}
	// limit 0 = unlimited (the admin-exemption path).
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.3.0/24"), zone, 0, announcePool); err != nil {
		t.Fatalf("unlimited: %v", err)
	}
}

func TestAnnouncementNonMemberRejected(t *testing.T) {
	st, uid, nodeA, _, _ := announceFixture(t)
	ctx := context.Background()
	z2, err := st.Zones().Create(ctx, uid, "lonely", "hash", 0)
	if err != nil {
		t.Fatalf("zone2: %v", err)
	}
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), z2.ID, 0, announcePool); !errors.Is(err, store.ErrNotMember) {
		t.Fatalf("non-member err = %v, want ErrNotMember", err)
	}
}

func TestAnnouncementPoolExhaustedAndReuseAfterReclaim(t *testing.T) {
	st, uid, nodeA, nodeB, zone := announceFixture(t)
	ctx := context.Background()
	tinyPool := netip.MustParsePrefix("100.100.0.0/24") // exactly one /24

	a1, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, tinyPool)
	if err != nil {
		t.Fatalf("fill pool: %v", err)
	}
	if _, _, err := st.Announcements().Create(ctx, uid, nodeB, realBlock(t, "192.168.2.0/24"), zone, 0, tinyPool); !errors.Is(err, store.ErrSyntheticPoolExhausted) {
		t.Fatalf("exhausted err = %v, want ErrSyntheticPoolExhausted", err)
	}
	// Reclaim frees the block for immediate reuse (SC-003).
	ann, reclaimed, err := st.Announcements().Detach(ctx, zone, a1.ID)
	if err != nil || !reclaimed {
		t.Fatalf("detach = (%+v, %v, %v), want reclaimed", ann, reclaimed, err)
	}
	a2, _, err := st.Announcements().Create(ctx, uid, nodeB, realBlock(t, "192.168.2.0/24"), zone, 0, tinyPool)
	if err != nil {
		t.Fatalf("reuse after reclaim: %v", err)
	}
	if a2.Synthetic != a1.Synthetic {
		t.Errorf("reused block = %v, want %v", a2.Synthetic, a1.Synthetic)
	}
}

func TestAnnouncementDetachKeepsOtherZones(t *testing.T) {
	st, uid, nodeA, _, zone := announceFixture(t)
	ctx := context.Background()
	z2, _ := st.Zones().Create(ctx, uid, "office", "hash", 0)
	if err := st.Zones().Join(ctx, z2.ID, nodeA); err != nil {
		t.Fatalf("join: %v", err)
	}
	a, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), zone, 0, announcePool)
	if err != nil {
		t.Fatalf("announce: %v", err)
	}
	if _, _, err := st.Announcements().Create(ctx, uid, nodeA, realBlock(t, "192.168.1.0/24"), z2.ID, 0, announcePool); err != nil {
		t.Fatalf("attach z2: %v", err)
	}

	_, reclaimed, err := st.Announcements().Detach(ctx, zone, a.ID)
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	if reclaimed {
		t.Error("detach of one of two attachments must not reclaim the body")
	}
	if _, err := st.Announcements().Get(ctx, a.ID); err != nil {
		t.Errorf("announcement gone after partial detach: %v", err)
	}
	// Second detach is the last one → reclaim.
	if _, reclaimed, err = st.Announcements().Detach(ctx, z2.ID, a.ID); err != nil || !reclaimed {
		t.Fatalf("final detach = (%v, %v), want reclaimed", reclaimed, err)
	}
	if _, err := st.Announcements().Get(ctx, a.ID); !errors.Is(err, store.ErrAnnouncementNotFound) {
		t.Errorf("announcement still present after reclaim: %v", err)
	}
	// Detaching a non-attached zone is a typed not-found.
	if _, _, err := st.Announcements().Detach(ctx, zone, a.ID); !errors.Is(err, store.ErrAnnouncementNotFound) {
		t.Errorf("stale detach err = %v, want ErrAnnouncementNotFound", err)
	}
}

func TestAnnouncementConcurrentAllocationUnique(t *testing.T) {
	st, uid, _, _, zone := announceFixture(t)
	ctx := context.Background()
	first, last, _ := ipam.PoolRange("100.127.0.0/16")

	const n = 8
	nodes := make([]int64, n)
	for i := range nodes {
		nd, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("r%d", i), fmt.Sprintf("pk-%d", i), "openwrt", first, last, 0)
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		if err := st.Zones().Join(ctx, zone, nd.ID); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
		nodes[i] = nd.ID
	}

	var wg sync.WaitGroup
	results := make([]*store.Announcement, n)
	errs := make([]error, n)
	for i := range nodes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = st.Announcements().Create(ctx, uid, nodes[i],
				realBlock(t, "192.168.77.0/24"), zone, 0, announcePool)
		}(i)
	}
	wg.Wait()

	seen := map[uint32]int{}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent create %d: %v", i, errs[i])
		}
		seen[results[i].Synthetic.Base]++
	}
	if len(seen) != n {
		t.Errorf("synthetic bases not unique under concurrency: %v", seen)
	}
}
