package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"lanweave/internal/server/ipam"
)

// ErrUserExists is returned when inserting a user whose username already exists.
var ErrUserExists = errors.New("user already exists")

// ErrUserNotFound is returned when a user id does not exist.
var ErrUserNotFound = errors.New("user not found")

// ErrLastAdmin is returned when deleting a user would leave no administrator.
var ErrLastAdmin = errors.New("cannot delete the last administrator")

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

// GetByID returns the matching user, or (nil, nil) when none exists. Used by the
// refresh flow to mint a fresh access token for the refresh token's owner.
func (r *UserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	const q = `SELECT id, username, password_hash, is_admin, created_at
	           FROM users WHERE id = ?`
	var (
		u         User
		isAdmin   int
		createdAt string
	)
	err := r.db.QueryRowContext(ctx, q, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user %d: %w", id, err)
	}
	u.IsAdmin = isAdmin != 0
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at for user %d: %w", id, err)
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

// SurvivingMembership is a removed node's address in a zone owned by *someone else*.
// That zone survives the deletion, so its isolation-set element must be cleared
// individually (unlike the deleted user's own zones, which are destroyed wholesale).
type SurvivingMembership struct {
	IP     netip.Addr
	ZoneID int64
}

// DeletionResult reports the data-plane footprint of a deleted user so the caller can
// reconcile the live tunnel and isolation rules with the post-delete database (the rows
// themselves are already gone).
type DeletionResult struct {
	NodePubKeys          []string
	SurvivingMemberships []SurvivingMembership
	OwnedZoneIDs         []int64
}

// DeleteCascade removes a user and everything attributable to them in one atomic
// transaction. Foreign keys (enabled in the store DSN) cascade the delete to the user's
// nodes, the zones they own, and all related memberships, and SET NULL on invite
// references. It returns ErrUserNotFound if the id has no user, or ErrLastAdmin if the
// target is the only administrator (leaving zero admins is refused). The data-plane
// footprint is gathered *before* the delete, inside the transaction, so it is a
// consistent snapshot of exactly what is removed.
func (r *UserRepo) DeleteCascade(ctx context.Context, targetID int64) (*DeletionResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	var isAdmin int
	err = tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = ?`, targetID).Scan(&isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("look up user: %w", err)
	}
	if isAdmin != 0 {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&admins); err != nil {
			return nil, fmt.Errorf("count admins: %w", err)
		}
		if admins <= 1 {
			return nil, ErrLastAdmin
		}
	}

	result := &DeletionResult{}
	if result.OwnedZoneIDs, err = gatherInts(ctx, tx, `SELECT id FROM zones WHERE owner_user_id = ?`, targetID); err != nil {
		return nil, fmt.Errorf("gather owned zones: %w", err)
	}
	if result.NodePubKeys, err = gatherStrings(ctx, tx, `SELECT wg_pubkey FROM nodes WHERE user_id = ?`, targetID); err != nil {
		return nil, fmt.Errorf("gather node keys: %w", err)
	}

	// The user's nodes' addresses in zones owned by someone else (those zones survive).
	memRows, err := tx.QueryContext(ctx, `
SELECT n.ip, zm.zone_id
FROM zone_members zm
JOIN nodes n ON n.id = zm.node_id
JOIN zones z ON z.id = zm.zone_id
WHERE n.user_id = ? AND z.owner_user_id <> ?`, targetID, targetID)
	if err != nil {
		return nil, fmt.Errorf("gather surviving memberships: %w", err)
	}
	for memRows.Next() {
		var ipVal, zoneID int64
		if err := memRows.Scan(&ipVal, &zoneID); err != nil {
			_ = memRows.Close()
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		result.SurvivingMemberships = append(result.SurvivingMemberships, SurvivingMembership{
			IP:     ipam.Uint32ToAddr(uint32(ipVal)),
			ZoneID: zoneID,
		})
	}
	if err := memRows.Err(); err != nil {
		_ = memRows.Close()
		return nil, fmt.Errorf("iterate memberships: %w", err)
	}
	_ = memRows.Close()

	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, targetID); err != nil {
		return nil, fmt.Errorf("delete user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete: %w", err)
	}
	return result, nil
}

// gatherInts/gatherStrings read a single-column result into a slice (cursor fully
// consumed and closed before the caller issues the next statement on the same tx).
func gatherInts(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func gatherStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
