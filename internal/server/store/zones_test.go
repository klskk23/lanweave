package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"lanweave/internal/server/store"
)

// seedNode registers a node for a user and returns it.
func seedNode(t *testing.T, st *store.Store, userID int64, name string) *store.Node {
	t.Helper()
	first, last := poolBounds(t)
	n, err := st.Nodes().Create(context.Background(), userID, name, freshPubKey(t), "unknown", first, last, 0)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return n
}

func TestZoneCreateAndGet(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	owner := seedUser(t, st, "alice")

	z, err := st.Zones().Create(ctx, owner, "devteam", "hash", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if z.Name != "devteam" || z.OwnerID != owner {
		t.Fatalf("unexpected zone: %+v", z)
	}
	if _, err := st.Zones().Create(ctx, owner, "devteam", "hash2", 0); !errors.Is(err, store.ErrZoneNameTaken) {
		t.Errorf("dup name: got %v, want ErrZoneNameTaken", err)
	}
	got, err := st.Zones().GetByName(ctx, "devteam")
	if err != nil || got == nil || got.ID != z.ID || got.PasswordHash != "hash" {
		t.Fatalf("GetByName: %+v %v", got, err)
	}
	if miss, _ := st.Zones().GetByName(ctx, "nope"); miss != nil {
		t.Errorf("GetByName(nope) should be nil")
	}
}

// TestZoneCreateOwnedLimit covers the owned-zone cap: refused at the cap with no row,
// allowed one below, and deleting an owned zone frees exactly one slot (FR-004/006).
func TestZoneCreateOwnedLimit(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	const limit = 2

	for i := range limit {
		if _, err := st.Zones().Create(ctx, alice, fmt.Sprintf("z%d", i), "hash", limit); err != nil {
			t.Fatalf("create %d (under cap): %v", i, err)
		}
	}
	if _, err := st.Zones().Create(ctx, alice, "over", "hash", limit); !errors.Is(err, store.ErrOwnedZoneLimitReached) {
		t.Fatalf("at cap: got %v, want ErrOwnedZoneLimitReached", err)
	}
	list, _ := st.Zones().ListForUser(ctx, alice)
	if len(list) != limit {
		t.Fatalf("after refusal: %d owned zones, want %d (nothing inserted)", len(list), limit)
	}

	if err := st.Zones().Delete(ctx, list[0].ID); err != nil {
		t.Fatalf("delete owned zone: %v", err)
	}
	if _, err := st.Zones().Create(ctx, alice, "fresh", "hash", limit); err != nil {
		t.Fatalf("create after delete: %v", err)
	}
	if _, err := st.Zones().Create(ctx, alice, "again", "hash", limit); !errors.Is(err, store.ErrOwnedZoneLimitReached) {
		t.Fatalf("second extra create: got %v, want ErrOwnedZoneLimitReached", err)
	}
}

// TestZoneCreateUnlimited proves maxOwnedZones <= 0 disables the cap (FR-007).
func TestZoneCreateUnlimited(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	for i := range 12 {
		if _, err := st.Zones().Create(ctx, alice, fmt.Sprintf("z%d", i), "hash", 0); err != nil {
			t.Fatalf("unlimited create %d: %v", i, err)
		}
	}
}

// TestZoneOwnedCapDoesNotBlockJoin proves only owned zones count: a user at their
// owned-zone cap can still join a zone owned by someone else (FR-006).
func TestZoneOwnedCapDoesNotBlockJoin(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")
	const limit = 1

	if _, err := st.Zones().Create(ctx, alice, "alice-zone", "hash", limit); err != nil {
		t.Fatalf("alice create: %v", err)
	}
	if _, err := st.Zones().Create(ctx, alice, "extra", "hash", limit); !errors.Is(err, store.ErrOwnedZoneLimitReached) {
		t.Fatalf("alice at owned cap: got %v, want ErrOwnedZoneLimitReached", err)
	}
	// Bob owns a zone; Alice's node joins it while she is at her own owned-zone cap.
	bz, err := st.Zones().Create(ctx, bob, "bob-zone", "hash", limit)
	if err != nil {
		t.Fatalf("bob create: %v", err)
	}
	an := seedNode(t, st, alice, "alice-laptop")
	if err := st.Zones().Join(ctx, bz.ID, an.ID); err != nil {
		t.Fatalf("join while at owned cap should succeed (membership uncapped): %v", err)
	}
}

// TestZoneCreateLimitConcurrent is the SC-010 zone-side boundary test: with the owner
// one below the cap, many concurrent creates yield exactly one success and never exceed
// the cap (same conditional-INSERT atomicity as the node-side test).
func TestZoneCreateLimitConcurrent(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	const limit = 5
	for i := range limit - 1 {
		if _, err := st.Zones().Create(ctx, alice, fmt.Sprintf("seed%d", i), "hash", limit); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	const racers = 20
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	wg.Add(racers)
	for i := range racers {
		go func(i int) {
			defer wg.Done()
			_, err := st.Zones().Create(ctx, alice, fmt.Sprintf("race%d", i), "hash", limit)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	var ok, limited int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrOwnedZoneLimitReached):
			limited++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("concurrent at boundary: %d succeeded, want exactly 1", ok)
	}
	if limited != racers-1 {
		t.Errorf("refused = %d, want %d (every loser sees ErrOwnedZoneLimitReached)", limited, racers-1)
	}
	if list, _ := st.Zones().ListForUser(ctx, alice); len(list) != limit {
		t.Errorf("final owned zone count = %d, want %d (never exceeds cap)", len(list), limit)
	}
}

func TestZoneJoinIdempotentAndLeave(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	owner := seedUser(t, st, "alice")
	z, _ := st.Zones().Create(ctx, owner, "z1", "hash", 0)
	n := seedNode(t, st, owner, "laptop")

	if err := st.Zones().Join(ctx, z.ID, n.ID); err != nil {
		t.Fatalf("join: %v", err)
	}
	// Idempotent: second join is a no-op, no error.
	if err := st.Zones().Join(ctx, z.ID, n.ID); err != nil {
		t.Fatalf("re-join: %v", err)
	}
	members, _ := st.Zones().MembersByZone(ctx, z.ID)
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}

	if err := st.Zones().Leave(ctx, z.ID, n.ID); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if err := st.Zones().Leave(ctx, z.ID, n.ID); !errors.Is(err, store.ErrNotMember) {
		t.Errorf("leave non-member: got %v, want ErrNotMember", err)
	}
}

func TestZoneMembersTransparency(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")
	z, _ := st.Zones().Create(ctx, alice, "z1", "hash", 0)
	na := seedNode(t, st, alice, "a")
	nb := seedNode(t, st, bob, "b")
	_ = st.Zones().Join(ctx, z.ID, na.ID)
	_ = st.Zones().Join(ctx, z.ID, nb.ID)

	members, err := st.Zones().MembersByZone(ctx, z.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	owners := map[string]string{} // node name -> owner
	ids := map[string]int64{}     // node name -> node id
	for _, m := range members {
		owners[m.NodeName] = m.OwnerName
		ids[m.NodeName] = m.NodeID
	}
	if owners["a"] != "alice" || owners["b"] != "bob" {
		t.Errorf("cross-user transparency wrong: %+v", owners)
	}
	// node_id is exposed so an owner can remove a member by id (feature 011).
	if ids["a"] != na.ID || ids["b"] != nb.ID {
		t.Errorf("member node ids wrong: got %+v, want a=%d b=%d", ids, na.ID, nb.ID)
	}
}

func TestZoneListAndParticipant(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")

	owned, _ := st.Zones().Create(ctx, alice, "owned", "hash", 0) // alice owns, no member
	joined, _ := st.Zones().Create(ctx, bob, "joined", "hash", 0) // bob owns
	other, _ := st.Zones().Create(ctx, bob, "other", "hash", 0)   // bob owns, alice not in
	na := seedNode(t, st, alice, "a")
	_ = st.Zones().Join(ctx, joined.ID, na.ID) // alice participates via her node

	list, err := st.Zones().ListForUser(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	ownerFlag := map[string]bool{}
	for _, z := range list {
		names[z.Name] = true
		ownerFlag[z.Name] = z.IsOwner
	}
	if !names["owned"] || !names["joined"] || names["other"] {
		t.Fatalf("ListForUser wrong: %+v", names)
	}
	if !ownerFlag["owned"] || ownerFlag["joined"] {
		t.Errorf("is_owner wrong: owned=%v joined=%v", ownerFlag["owned"], ownerFlag["joined"])
	}

	if ok, _ := st.Zones().IsParticipant(ctx, owned.ID, alice); !ok {
		t.Error("alice should participate in owned (owner)")
	}
	if ok, _ := st.Zones().IsParticipant(ctx, joined.ID, alice); !ok {
		t.Error("alice should participate in joined (member node)")
	}
	if ok, _ := st.Zones().IsParticipant(ctx, other.ID, alice); ok {
		t.Error("alice should NOT participate in other")
	}
}

func TestZoneUpdatePassword(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	owner := seedUser(t, st, "alice")
	z, _ := st.Zones().Create(ctx, owner, "z1", "oldhash", 0)
	n := seedNode(t, st, owner, "laptop")
	_ = st.Zones().Join(ctx, z.ID, n.ID)

	if err := st.Zones().UpdatePassword(ctx, z.ID, "newhash"); err != nil {
		t.Fatalf("update password: %v", err)
	}
	got, _ := st.Zones().GetByName(ctx, "z1")
	if got.PasswordHash != "newhash" {
		t.Errorf("hash not updated: %q", got.PasswordHash)
	}
	// A password change must not touch membership.
	members, _ := st.Zones().MembersByZone(ctx, z.ID)
	if len(members) != 1 {
		t.Errorf("password change altered membership: %d members", len(members))
	}
}

func TestZonesForNodeAndRebuild(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	owner := seedUser(t, st, "alice")
	z1, _ := st.Zones().Create(ctx, owner, "z1", "hash", 0)
	z2, _ := st.Zones().Create(ctx, owner, "z2", "hash", 0)
	empty, _ := st.Zones().Create(ctx, owner, "empty", "hash", 0)
	n := seedNode(t, st, owner, "multi")
	_ = st.Zones().Join(ctx, z1.ID, n.ID)
	_ = st.Zones().Join(ctx, z2.ID, n.ID)

	zids, _ := st.Zones().ZonesForNode(ctx, n.ID)
	if len(zids) != 2 {
		t.Fatalf("ZonesForNode = %v, want 2", zids)
	}

	states, err := st.Zones().AllForRebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]int{}
	for _, s := range states {
		byID[s.ID] = len(s.MemberIPs)
	}
	if byID[z1.ID] != 1 || byID[z2.ID] != 1 {
		t.Errorf("rebuild member counts: z1=%d z2=%d, want 1/1", byID[z1.ID], byID[z2.ID])
	}
	if _, ok := byID[empty.ID]; !ok {
		t.Error("empty zone must appear in rebuild (with 0 members)")
	}
}
