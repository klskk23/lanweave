package auth_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/store"
)

func newRepo(t *testing.T) *store.UserRepo {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.Users()
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestEnsureAdminCreatesThenSkips(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	log := quietLogger()
	cfg := &config.Config{Admin: config.AdminConfig{Username: "admin", Password: config.Secret("pw1")}}

	if err := auth.EnsureAdmin(ctx, repo, cfg, log); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	u1, err := repo.GetByUsername(ctx, "admin")
	if err != nil || u1 == nil {
		t.Fatalf("admin not created: %v %v", u1, err)
	}
	if !u1.IsAdmin {
		t.Fatal("bootstrap user is not admin")
	}

	// Change the configured password and re-run: the stored hash MUST NOT change.
	cfg.Admin.Password = config.Secret("pw2-different")
	if err := auth.EnsureAdmin(ctx, repo, cfg, log); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	u2, err := repo.GetByUsername(ctx, "admin")
	if err != nil || u2 == nil {
		t.Fatalf("admin missing after second run: %v %v", u2, err)
	}
	if u1.PasswordHash != u2.PasswordHash {
		t.Fatal("stored hash changed across restart; bootstrap must be idempotent")
	}
}

func TestEnsureAdminEmptyPassword(t *testing.T) {
	repo := newRepo(t)
	cfg := &config.Config{Admin: config.AdminConfig{Username: "admin", Password: config.Secret("")}}
	err := auth.EnsureAdmin(context.Background(), repo, cfg, quietLogger())
	if err == nil {
		t.Fatal("expected error for empty admin password")
	}
	u, _ := repo.GetByUsername(context.Background(), "admin")
	if u != nil {
		t.Fatal("no user should be written when credentials are missing")
	}
}

func TestEnsureAdminStoredHashVerifies(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	cfg := &config.Config{Admin: config.AdminConfig{Username: "admin", Password: config.Secret("s3cret-pw")}}
	if err := auth.EnsureAdmin(ctx, repo, cfg, quietLogger()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u, _ := repo.GetByUsername(ctx, "admin")
	ok, err := auth.VerifyPassword("s3cret-pw", u.PasswordHash)
	if err != nil || !ok {
		t.Fatalf("stored hash does not verify: ok=%v err=%v", ok, err)
	}
}
