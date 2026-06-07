package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"lanweave/internal/server/store"
)

// sha256hex mirrors auth.HashRefreshToken without importing the auth package, so
// the store test can assert the persisted column holds the hash, never the plaintext.
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestRefreshIssueValidate covers US1: Issue returns a plaintext RT and stores only
// its SHA-256 hash; Validate(valid) returns the owning user; Validate(unknown) → invalid.
func TestRefreshIssueValidate(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	rt := st.RefreshTokens()

	plaintext, err := rt.Issue(ctx, uid)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if plaintext == "" {
		t.Fatal("issue returned an empty token")
	}

	// The plaintext must never be persisted in any column; only its hash may appear.
	var rows int
	if err := st.DB().QueryRowContext(ctx,
		"SELECT count(*) FROM refresh_tokens WHERE token_hash = ?", plaintext).Scan(&rows); err != nil {
		t.Fatalf("query plaintext: %v", err)
	}
	if rows != 0 {
		t.Errorf("plaintext refresh token found in token_hash column (should be hashed)")
	}
	if err := st.DB().QueryRowContext(ctx,
		"SELECT count(*) FROM refresh_tokens WHERE token_hash = ?", sha256hex(plaintext)).Scan(&rows); err != nil {
		t.Fatalf("query hash: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected exactly one row keyed by the SHA-256 hash, got %d", rows)
	}

	gotUID, err := rt.Validate(ctx, plaintext)
	if err != nil {
		t.Fatalf("validate valid: %v", err)
	}
	if gotUID != uid {
		t.Errorf("validate returned uid %d, want %d", gotUID, uid)
	}

	if _, err := rt.Validate(ctx, "this-token-was-never-issued"); !errors.Is(err, store.ErrRefreshInvalid) {
		t.Errorf("validate unknown: got %v, want ErrRefreshInvalid", err)
	}
}

// TestRefreshValidateConcurrent covers the spec "concurrent expiry" edge case and D4
// (no rotation): the same RT validated from many goroutines at once all succeed and
// return the same user — no call invalidates another.
func TestRefreshValidateConcurrent(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	rt := st.RefreshTokens()

	plaintext, err := rt.Issue(ctx, uid)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	const n = 30
	var wg sync.WaitGroup
	type res struct {
		uid int64
		err error
	}
	results := make(chan res, n)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			got, err := rt.Validate(ctx, plaintext)
			results <- res{uid: got, err: err}
		}()
	}
	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			t.Fatalf("concurrent validate errored: %v", r.err)
		}
		if r.uid != uid {
			t.Fatalf("concurrent validate uid = %d, want %d", r.uid, uid)
		}
	}
}

// TestRefreshRevoke covers US2: Revoke makes a live RT fail Validate, and is idempotent
// for unknown and already-revoked tokens (so a double logout is never an error).
func TestRefreshRevoke(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	rt := st.RefreshTokens()

	plaintext, err := rt.Issue(ctx, uid)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := rt.Validate(ctx, plaintext); err != nil {
		t.Fatalf("precondition validate: %v", err)
	}

	if err := rt.Revoke(ctx, plaintext); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := rt.Validate(ctx, plaintext); !errors.Is(err, store.ErrRefreshInvalid) {
		t.Errorf("validate after revoke: got %v, want ErrRefreshInvalid", err)
	}

	// Idempotent: revoking again, or revoking a token that was never issued, is not an error.
	if err := rt.Revoke(ctx, plaintext); err != nil {
		t.Errorf("revoke already-revoked: got %v, want nil (idempotent)", err)
	}
	if err := rt.Revoke(ctx, "this-token-was-never-issued"); err != nil {
		t.Errorf("revoke unknown: got %v, want nil (idempotent)", err)
	}
}

// TestRefreshCascadeOnUserDelete covers US2 / FR-009: deleting the owning user removes
// their refresh_tokens rows via the FK cascade, so a stale RT can never be renewed.
func TestRefreshCascadeOnUserDelete(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	// seedUser creates admins, so a second user keeps DeleteCascade's last-admin guard
	// from blocking alice's deletion.
	seedUser(t, st, "root")
	uid := seedUser(t, st, "alice")
	rt := st.RefreshTokens()

	plaintext, err := rt.Issue(ctx, uid)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := st.Users().DeleteCascade(ctx, uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var rows int
	if err := st.DB().QueryRowContext(ctx,
		"SELECT count(*) FROM refresh_tokens WHERE user_id = ?", uid).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("refresh_tokens rows survived user delete: got %d, want 0 (FK cascade)", rows)
	}
	if _, err := rt.Validate(ctx, plaintext); !errors.Is(err, store.ErrRefreshInvalid) {
		t.Errorf("validate after user delete: got %v, want ErrRefreshInvalid", err)
	}
}

// newClockedRepo returns a refresh-token repo whose clock the test controls, for
// expiry/sliding tests without wall-clock sleeps.
func newClockedRepo(t *testing.T, st *store.Store, now *time.Time) *store.RefreshTokenRepo {
	t.Helper()
	r := st.RefreshTokens()
	r.SetClock(func() time.Time { return *now })
	return r
}

const day = 24 * time.Hour

// TestRefreshSlidingExpiry covers US3: an RT unused for more than 30 days expires, while
// each successful Validate slides the window forward (now+30d) so an actively used token
// never expires. Driven entirely by an injected clock — no wall-clock sleeps (Constitution II).
func TestRefreshSlidingExpiry(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rt := newClockedRepo(t, st, &now)

	plaintext, err := rt.Issue(ctx, uid)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Abandoned >30 days → expired.
	now = now.Add(31 * day)
	if _, err := rt.Validate(ctx, plaintext); !errors.Is(err, store.ErrRefreshInvalid) {
		t.Fatalf("validate after 31d idle: got %v, want ErrRefreshInvalid", err)
	}
}

// TestRefreshSlideKeepsAlive covers US3: a token renewed on a cadence inside the 30-day
// window never expires, because each Validate slides expires_at to now+30d.
func TestRefreshSlideKeepsAlive(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rt := newClockedRepo(t, st, &now)

	plaintext, err := rt.Issue(ctx, uid)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Use it every 20 days for a year — well past the original 30-day expiry — and it
	// stays valid because every use slides the window forward.
	for i := range 18 {
		now = now.Add(20 * day)
		got, err := rt.Validate(ctx, plaintext)
		if err != nil {
			t.Fatalf("validate at +%dd: %v", (i+1)*20, err)
		}
		if got != uid {
			t.Fatalf("validate uid = %d, want %d", got, uid)
		}
	}

	// After the last use at +360d, jumping another 31 idle days finally expires it.
	now = now.Add(31 * day)
	if _, err := rt.Validate(ctx, plaintext); !errors.Is(err, store.ErrRefreshInvalid) {
		t.Errorf("validate 31d after last use: got %v, want ErrRefreshInvalid", err)
	}
}
