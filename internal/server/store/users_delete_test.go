package store_test

import (
	"context"
	"errors"
	"testing"

	"lanweave/internal/server/store"
)

// TestUserDeleteCascade verifies the full cascade over a real store: the user, their
// nodes, and their owned zone are removed; a foreign user's zone survives but loses the
// deleted user's node; the data-plane plan (DeletionResult) is correct; the freed
// address is reused; an invite the user created has its creator reference nulled (audit
// preserved, FR-008); a missing id reports ErrUserNotFound.
func TestUserDeleteCascade(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")

	n1 := seedNode(t, st, alice, "n1") // 100.127.0.2
	n2 := seedNode(t, st, alice, "n2") // 100.127.0.3

	az, err := st.Zones().Create(ctx, alice, "az", "hash")
	if err != nil {
		t.Fatal(err)
	}
	bz, err := st.Zones().Create(ctx, bob, "bz", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Zones().Join(ctx, az.ID, n1.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Zones().Join(ctx, bz.ID, n2.ID); err != nil { // alice's node in bob's zone
		t.Fatal(err)
	}
	if _, err := st.Invites().Create(ctx, alice); err != nil {
		t.Fatal(err)
	}

	result, err := st.Users().DeleteCascade(ctx, alice)
	if err != nil {
		t.Fatalf("delete cascade: %v", err)
	}

	// Data-plane plan.
	if len(result.NodePubKeys) != 2 {
		t.Errorf("node pubkeys = %d, want 2", len(result.NodePubKeys))
	}
	if len(result.OwnedZoneIDs) != 1 || result.OwnedZoneIDs[0] != az.ID {
		t.Errorf("owned zones = %v, want [%d]", result.OwnedZoneIDs, az.ID)
	}
	if len(result.SurvivingMemberships) != 1 ||
		result.SurvivingMemberships[0].ZoneID != bz.ID ||
		result.SurvivingMemberships[0].IP.String() != n2.IP.String() {
		t.Errorf("surviving memberships = %+v, want [{%s %d}]", result.SurvivingMemberships, n2.IP, bz.ID)
	}

	// Records: alice + her nodes + owned zone gone; bob + bz survive; bz membership cleared.
	if u, _ := st.Users().GetByUsername(ctx, "alice"); u != nil {
		t.Error("alice not removed")
	}
	if nodes, _ := st.Nodes().ListByUser(ctx, alice); len(nodes) != 0 {
		t.Errorf("alice nodes remain: %d", len(nodes))
	}
	if z, _ := st.Zones().GetByName(ctx, "az"); z != nil {
		t.Error("owned zone remains")
	}
	if z, _ := st.Zones().GetByName(ctx, "bz"); z == nil {
		t.Fatal("foreign zone wrongly removed")
	}
	if m, _ := st.Zones().MembersByZone(ctx, bz.ID); len(m) != 0 {
		t.Errorf("foreign zone still has %d members after the deleted user's node left", len(m))
	}
	if u, _ := st.Users().GetByUsername(ctx, "bob"); u == nil {
		t.Error("bob wrongly removed")
	}

	// Freed address reused (lowest free == n1's address).
	fresh := seedNode(t, st, bob, "fresh")
	if fresh.IP.String() != n1.IP.String() {
		t.Errorf("freed address not reused: got %s, want %s", fresh.IP, n1.IP)
	}

	// Invite audit row preserved with the creator reference nulled (FR-008).
	invs, err := st.Invites().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 1 {
		t.Fatalf("invites = %d, want 1 (audit preserved)", len(invs))
	}
	if invs[0].CreatedByID != nil {
		t.Errorf("invite creator reference not cleared, got %d", *invs[0].CreatedByID)
	}

	// Missing id.
	if _, err := st.Users().DeleteCascade(ctx, 99999); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("missing id: got %v, want ErrUserNotFound", err)
	}
}

// TestUserDeleteCascadeGuards verifies the admin-safety guards and that a rejected
// delete changes nothing (atomicity, SC-005).
func TestUserDeleteCascadeGuards(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice") // sole admin
	seedNode(t, st, alice, "laptop")

	// Sole admin → rejected, nothing removed.
	if _, err := st.Users().DeleteCascade(ctx, alice); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("delete sole admin: got %v, want ErrLastAdmin", err)
	}
	if u, _ := st.Users().GetByUsername(ctx, "alice"); u == nil {
		t.Error("sole-admin user removed despite rejection")
	}
	if nodes, _ := st.Nodes().ListByUser(ctx, alice); len(nodes) != 1 {
		t.Errorf("rejected delete altered node rows: %d, want 1", len(nodes))
	}

	// With a second admin, deleting one is allowed.
	bob := seedUser(t, st, "bob")
	if _, err := st.Users().DeleteCascade(ctx, bob); err != nil {
		t.Fatalf("delete non-last admin: %v", err)
	}
	if u, _ := st.Users().GetByUsername(ctx, "bob"); u != nil {
		t.Error("bob not removed")
	}
	// alice is the sole admin again → still protected.
	if _, err := st.Users().DeleteCascade(ctx, alice); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("alice sole admin again: got %v, want ErrLastAdmin", err)
	}
}
