package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUserExists is returned when inserting a user whose username already exists.
var ErrUserExists = errors.New("user already exists")

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
}

// UserRepo provides access to the users table.
type UserRepo struct {
	db *sql.DB
}

// Users returns a repository bound to this store.
func (s *Store) Users() *UserRepo { return &UserRepo{db: s.db} }

// GetByUsername returns the matching user, or (nil, nil) when none exists.
// Lookup is case-insensitive to match the schema's COLLATE NOCASE uniqueness.
func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*User, error) {
	const q = `SELECT id, username, password_hash, is_admin, created_at
	           FROM users WHERE username = ? COLLATE NOCASE`
	var (
		u         User
		isAdmin   int
		createdAt string
	)
	err := r.db.QueryRowContext(ctx, q, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user %q: %w", username, err)
	}
	u.IsAdmin = isAdmin != 0
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at for user %q: %w", username, err)
	}
	u.CreatedAt = t
	return &u, nil
}

// CreateAdmin inserts a new admin user. It returns ErrUserExists if the username
// is already taken (the unique constraint is the authority, making this race-safe).
func (r *UserRepo) CreateAdmin(ctx context.Context, username, passwordHash string) (*User, error) {
	now := time.Now().UTC().Truncate(time.Second)
	const q = `INSERT INTO users (username, password_hash, is_admin, created_at)
	           VALUES (?, ?, 1, ?)`
	res, err := r.db.ExecContext(ctx, q, username, passwordHash, now.Format(time.RFC3339))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUserExists
		}
		return nil, fmt.Errorf("create admin %q: %w", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create admin %q: %w", username, err)
	}
	return &User{
		ID:           id,
		Username:     username,
		PasswordHash: passwordHash,
		IsAdmin:      true,
		CreatedAt:    now,
	}, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
