package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInviteInvalid is returned when an invite code does not exist or is already used.
var ErrInviteInvalid = errors.New("invite code invalid or already used")

// Register atomically creates a non-admin user and consumes a one-time invite
// code. It returns ErrUserExists (username taken) or ErrInviteInvalid (bad/used
// code), leaving the database unchanged on either.
//
// Concurrency: the user INSERT is the transaction's first write, so it takes the
// SQLite write lock immediately and serializes concurrent registrations without a
// read-then-write deadlock (busy_timeout absorbs the brief wait). The invite is
// then consumed with a conditional UPDATE whose RowsAffected==1 check is the
// authoritative one-time guard — independent of any driver-specific txlock mode.
func (s *Store) Register(ctx context.Context, username, passwordHash, code string) (*User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	now := time.Now().UTC().Truncate(time.Second)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?, ?, 0, ?)`,
		username, passwordHash, now.Format(time.RFC3339))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUserExists
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	uid, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	upd, err := tx.ExecContext(ctx,
		`UPDATE invites SET used_by_user_id = ?, used_at = ? WHERE code = ? AND used_at IS NULL`,
		uid, now.Format(time.RFC3339), code)
	if err != nil {
		return nil, fmt.Errorf("consume invite: %w", err)
	}
	n, err := upd.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if n != 1 {
		return nil, ErrInviteInvalid
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &User{
		ID:           uid,
		Username:     username,
		PasswordHash: passwordHash,
		IsAdmin:      false,
		CreatedAt:    now,
	}, nil
}
