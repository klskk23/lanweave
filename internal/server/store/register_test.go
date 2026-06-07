package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lanweave/internal/server/store"
)

// seedAdminID creates the admin and returns its id, for tests that insert invite
// rows directly (e.g. with a chosen expires_at).
func seedAdminID(t *testing.T, st *store.Store) int64 {
	t.Helper()
	admin, err := st.Users().CreateAdmin(context.Background(), "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return admin.ID
}

// insertInvite inserts an unused invite with an explicit expires_at. An empty
// expiresAt stores SQL NULL (never expires). This lets expiry be exercised with a
// past-dated row instead of a wall-clock sleep.
func insertInvite(t *testing.T, st *store.Store, adminID int64, code, expiresAt string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	exp := sql.NullString{String: expiresAt, Valid: expiresAt != ""}
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO invites (code, created_by_user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		code, adminID, now, exp); err != nil {
		t.Fatalf("insert invite: %v", err)
	}
}

func seedAdminAndCode(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	code, _, err := st.Invites().Create(ctx, admin.ID, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	return code
}

func TestRegisterHappyPath(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	code := seedAdminAndCode(t, st)

	u, err := st.Register(ctx, "bob", "bob-hash", code)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.IsAdmin {
		t.Error("registered user must not be admin")
	}
	got, _ := st.Users().GetByUsername(ctx, "bob")
	if got == nil {
		t.Fatal("user not persisted")
	}
	// The code is now consumed.
	list, _ := st.Invites().List(ctx)
	if list[0].UsedAt == nil || list[0].UsedByName == nil || *list[0].UsedByName != "bob" {
		t.Errorf("invite not marked used by bob: %+v", list[0])
	}
}

func TestRegisterUsedCode(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	code := seedAdminAndCode(t, st)

	if _, err := st.Register(ctx, "bob", "h", code); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err := st.Register(ctx, "carol", "h", code)
	if !errors.Is(err, store.ErrInviteInvalid) {
		t.Fatalf("expected ErrInviteInvalid for reused code, got %v", err)
	}
	if got, _ := st.Users().GetByUsername(ctx, "carol"); got != nil {
		t.Error("no account should be created on reused code")
	}
}

func TestRegisterUnknownCode(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	seedAdminAndCode(t, st)

	_, err := st.Register(ctx, "dave", "h", "no-such-code")
	if !errors.Is(err, store.ErrInviteInvalid) {
		t.Fatalf("expected ErrInviteInvalid for unknown code, got %v", err)
	}
	if got, _ := st.Users().GetByUsername(ctx, "dave"); got != nil {
		t.Error("no account should be created on unknown code")
	}
}

// TestRegisterExpiredCodeRejected — a code whose expires_at is in the past is
// rejected with the generic ErrInviteInvalid and leaves no account behind (US1).
func TestRegisterExpiredCodeRejected(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	adminID := seedAdminID(t, st)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	insertInvite(t, st, adminID, "expired-code", past)

	_, err := st.Register(ctx, "bob", "h", "expired-code")
	if !errors.Is(err, store.ErrInviteInvalid) {
		t.Fatalf("expected ErrInviteInvalid for expired code, got %v", err)
	}
	if got, _ := st.Users().GetByUsername(ctx, "bob"); got != nil {
		t.Error("no account should be created on an expired code")
	}
	list, _ := st.Invites().List(ctx)
	if list[0].UsedAt != nil {
		t.Error("expired invite must remain unused")
	}
}

// TestRegisterFutureAndNullExpiryAccepted — a future expires_at and a NULL
// expires_at (grandfathered / never-expire) both redeem successfully (US1 / FR-006
// / FR-007 / SC-004).
func TestRegisterFutureAndNullExpiryAccepted(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	adminID := seedAdminID(t, st)

	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	insertInvite(t, st, adminID, "future-code", future)
	if _, err := st.Register(ctx, "bob", "h", "future-code"); err != nil {
		t.Fatalf("future-dated code must register: %v", err)
	}

	insertInvite(t, st, adminID, "null-code", "") // NULL expires_at
	if _, err := st.Register(ctx, "carol", "h", "null-code"); err != nil {
		t.Fatalf("NULL-expiry (grandfathered) code must register: %v", err)
	}
}

func TestRegisterUsernameTakenLeavesCodeUnused(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	code := seedAdminAndCode(t, st)

	// "admin" already exists.
	_, err := st.Register(ctx, "admin", "h", code)
	if !errors.Is(err, store.ErrUserExists) {
		t.Fatalf("expected ErrUserExists, got %v", err)
	}
	list, _ := st.Invites().List(ctx)
	if list[0].UsedAt != nil {
		t.Error("invite must remain unused when username is taken")
	}
}

// TestRegisterOneTimeRace fires many concurrent registrations with ONE code and
// distinct usernames; exactly one must succeed (SC-002, FR-018).
func TestRegisterOneTimeRace(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	code := seedAdminAndCode(t, st)

	const n = 30
	var wg sync.WaitGroup
	results := make(chan error, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_, err := st.Register(ctx, fmt.Sprintf("racer%d", i), "h", code)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	var success, invalid, other int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, store.ErrInviteInvalid):
			invalid++
		default:
			other++
			t.Errorf("unexpected error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 success, got %d (invalid=%d other=%d)", success, invalid, other)
	}
	if invalid != n-1 {
		t.Fatalf("expected %d ErrInviteInvalid, got %d", n-1, invalid)
	}

	// Exactly one racer account exists.
	var count int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username LIKE 'racer%'").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 racer user row, got %d", count)
	}
}
