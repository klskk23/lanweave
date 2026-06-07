package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"lanweave/internal/server/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestMigrateIdempotent(t *testing.T) {
	st := newStore(t)
	// A second migration on an already-migrated DB must be a no-op, not an error.
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// TestInviteExpiresAtColumn verifies migration 0006: the invites table carries a
// nullable expires_at column, and a row inserted without it reads back NULL — the
// substrate for grandfathering pre-existing codes as never-expire.
func TestInviteExpiresAtColumn(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// Insert a 0002-shape row (no expires_at) and confirm it is NULL afterward.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO invites (code, created_by_user_id, created_at) VALUES (?, ?, ?)`,
		"legacy-code", admin.ID, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert legacy invite: %v", err)
	}
	var expires sql.NullString
	if err := st.DB().QueryRowContext(ctx,
		`SELECT expires_at FROM invites WHERE code = ?`, "legacy-code").Scan(&expires); err != nil {
		t.Fatalf("select expires_at: %v", err)
	}
	if expires.Valid {
		t.Errorf("expires_at should be NULL for a row inserted without it, got %q", expires.String)
	}
}

func TestUserRepoRoundTrip(t *testing.T) {
	st := newStore(t)
	repo := st.Users()
	ctx := context.Background()

	got, err := repo.GetByUsername(ctx, "nobody")
	if err != nil {
		t.Fatalf("get absent: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for absent user, got %+v", got)
	}

	created, err := repo.CreateAdmin(ctx, "alice", "hash-value")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if !created.IsAdmin || created.ID == 0 {
		t.Fatalf("unexpected created user: %+v", created)
	}

	// Case-insensitive lookup per COLLATE NOCASE.
	fetched, err := repo.GetByUsername(ctx, "ALICE")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched == nil || fetched.Username != "alice" || !fetched.IsAdmin {
		t.Fatalf("unexpected fetched user: %+v", fetched)
	}
	if fetched.PasswordHash != "hash-value" {
		t.Errorf("hash mismatch: %q", fetched.PasswordHash)
	}
}

func TestCreateAdminDuplicate(t *testing.T) {
	st := newStore(t)
	repo := st.Users()
	ctx := context.Background()

	if _, err := repo.CreateAdmin(ctx, "alice", "h1"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.CreateAdmin(ctx, "alice", "h2")
	if !errors.Is(err, store.ErrUserExists) {
		t.Fatalf("expected ErrUserExists, got %v", err)
	}
}
