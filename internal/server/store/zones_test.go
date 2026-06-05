package store_test

import (
	"context"
	"errors"
	"testing"

	"lanweave/internal/server/store"
)

// seedNode registers a node for a user and returns it.
func seedNode(t *testing.T, st *store.Store, userID int64, name string) *store.Node {
	t.Helper()
	first, last := poolBounds(t)
	n, err := st.Nodes().Create(context.Background(), userID, name, freshPubKey(t), first, last)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return n
}

func TestZoneCreateAndGet(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	owner := seedUser(t, st, "alice")

	z, err := st.Zones().Create(ctx, owner, "devteam", "hash")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if z.Name != "devteam" || z.OwnerID != owner {
		t.Fatalf("unexpected zone: %+v", z)
	}
	if _, err := st.Zones().Create(ctx, owner, "devteam", "hash2"); !errors.Is(err, store.ErrZoneNameTaken) {
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

func TestZoneJoinIdempotentAndLeave(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	owner := seedUser(t, st, "alice")
	z, _ := st.Zones().Create(ctx, owner, "z1", "hash")
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
	z, _ := st.Zones().Create(ctx, alice, "z1", "hash")
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
	for _, m := range members {
		owners[m.NodeName] = m.OwnerName
	}
	if owners["a"] != "alice" || owners["b"] != "bob" {
		t.Errorf("cross-user transparency wrong: %+v", owners)
	}
}

func TestZoneListAndParticipant(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")

	owned, _ := st.Zones().Create(ctx, alice, "owned", "hash") // alice owns, no member
	joined, _ := st.Zones().Create(ctx, bob, "joined", "hash") // bob owns
	other, _ := st.Zones().Create(ctx, bob, "other", "hash")   // bob owns, alice not in
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
	z, _ := st.Zones().Create(ctx, owner, "z1", "oldhash")
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
	z1, _ := st.Zones().Create(ctx, owner, "z1", "hash")
	z2, _ := st.Zones().Create(ctx, owner, "z2", "hash")
	empty, _ := st.Zones().Create(ctx, owner, "empty", "hash")
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
