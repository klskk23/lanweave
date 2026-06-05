package store_test

import (
	"context"
	"path/filepath"
	"testing"

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

	c1, err := st.Invites().Create(ctx, admin.ID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	c2, err := st.Invites().Create(ctx, admin.ID)
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

func TestInviteListSurvivesDeletedCreator(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	admin, _ := st.Users().CreateAdmin(ctx, "admin", "hash")
	if _, err := st.Invites().Create(ctx, admin.ID); err != nil {
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
