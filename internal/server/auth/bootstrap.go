package auth

import (
	"context"
	"errors"
	"log/slog"

	"lanweave/internal/server/config"
	"lanweave/internal/server/store"
)

// EnsureAdmin creates the configured admin user on first start. If the user
// already exists it is left untouched (the stored hash is never overwritten,
// even if the configured password changed). Missing credentials are an error.
func EnsureAdmin(ctx context.Context, repo *store.UserRepo, cfg *config.Config, log *slog.Logger) error {
	username := cfg.Admin.Username
	password := cfg.Admin.Password.Reveal()
	if username == "" || password == "" {
		return errors.New("admin credential not provided")
	}

	existing, err := repo.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	if existing != nil {
		log.Info("admin exists, skipping bootstrap", "username", username)
		return nil
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	u, err := repo.CreateAdmin(ctx, username, hash)
	if err != nil {
		// Concurrent first-start race: another process created it first.
		if errors.Is(err, store.ErrUserExists) {
			log.Info("admin exists, skipping bootstrap", "username", username)
			return nil
		}
		return err
	}
	log.Info("admin created", "username", username, "user_id", u.ID)
	return nil
}
