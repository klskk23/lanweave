package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"lanweave/internal/server/store"
)

func seedAdminAndCode(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	admin, err := st.Users().CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	code, err := st.Invites().Create(ctx, admin.ID)
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
