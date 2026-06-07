package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lanweave/internal/server/store"
)

func newStoreT(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestInviteCreateAndList(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	c1, _, err := st.Invites().Create(ctx, admin.ID, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	c2, _, err := st.Invites().Create(ctx, admin.ID, 0)
	if err != nil {
		t.Fatalf("create invite 2: %v", err)
	}
	if c1 == "" || c1 == c2 {
		t.Fatalf("codes should be non-empty and unique: %q %q", c1, c2)
	}

	list, err := st.Invites().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 invites, got %d", len(list))
	}
	// Newest first.
	if list[0].Code != c2 {
		t.Errorf("expected newest first; got %q", list[0].Code)
	}
	for _, inv := range list {
		if inv.UsedAt != nil {
			t.Errorf("new invite should be unused: %+v", inv)
		}
		if inv.CreatedByName == nil || *inv.CreatedByName != "admin" {
			t.Errorf("expected created_by admin, got %v", inv.CreatedByName)
		}
	}
}

// TestInviteCreateStampsExpiry — Create with a positive ttl stamps expires_at =
// created_at + ttl, returns the matching pointer, and persists it (FR-001).
func TestInviteCreateStampsExpiry(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	before := time.Now().UTC().Truncate(time.Second)
	code, exp, err := st.Invites().Create(ctx, admin.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil expiry for ttl>0")
	}
	if exp.Before(before.Add(24*time.Hour-time.Second)) || exp.After(time.Now().UTC().Add(24*time.Hour+time.Second)) {
		t.Errorf("expiry %v not ≈ created_at+24h", exp)
	}

	list, err := st.Invites().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *store.Invite
	for i := range list {
		if list[i].Code == code {
			found = &list[i]
		}
	}
	if found == nil || found.ExpiresAt == nil {
		t.Fatalf("created invite should carry expires_at: %+v", found)
	}
	if !found.ExpiresAt.Equal(*exp) {
		t.Errorf("persisted expires_at %v != returned %v", found.ExpiresAt, exp)
	}
}

// TestInviteCreateNoTTLNeverExpires — Create with ttl<=0 returns a nil expiry and
// stores a NULL expires_at (FR-005).
func TestInviteCreateNoTTLNeverExpires(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	code, exp, err := st.Invites().Create(ctx, admin.ID, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if exp != nil {
		t.Errorf("ttl=0 must return a nil expiry, got %v", exp)
	}
	list, _ := st.Invites().List(ctx)
	for _, inv := range list {
		if inv.Code == code && inv.ExpiresAt != nil {
			t.Errorf("ttl=0 invite must have NULL expires_at, got %v", inv.ExpiresAt)
		}
	}
}

func TestInviteListSurvivesDeletedCreator(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	admin, _ := st.Users().CreateAdmin(ctx, "admin", "hash")
	if _, _, err := st.Invites().Create(ctx, admin.ID, 0); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// Simulate the creator being deleted (FK ON DELETE SET NULL).
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM users WHERE id = ?", admin.ID); err != nil {
		t.Fatalf("delete creator: %v", err)
	}

	list, err := st.Invites().List(ctx)
	if err != nil {
		t.Fatalf("list after creator delete must succeed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(list))
	}
	if list[0].CreatedByName != nil || list[0].CreatedByID != nil {
		t.Errorf("expected dangling creator to be nil, got id=%v name=%v", list[0].CreatedByID, list[0].CreatedByName)
	}
}
