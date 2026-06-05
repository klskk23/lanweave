package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Invite is a one-time onboarding code. Pointer fields are nil when the
// referenced user is absent (unused, or creator/consumer deleted).
type Invite struct {
	ID            int64
	Code          string
	CreatedByID   *int64
	CreatedByName *string
	UsedByID      *int64
	UsedByName    *string
	UsedAt        *time.Time
	CreatedAt     time.Time
}

// InviteRepo provides access to the invites table.
type InviteRepo struct {
	db *sql.DB
}

// Invites returns a repository bound to this store.
func (s *Store) Invites() *InviteRepo { return &InviteRepo{db: s.db} }

// Create mints a new unguessable invite code created by the given admin.
func (r *InviteRepo) Create(ctx context.Context, createdByUserID int64) (string, error) {
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	for range 5 {
		code, err := generateInviteCode()
		if err != nil {
			return "", err
		}
		_, err = r.db.ExecContext(ctx,
			`INSERT INTO invites (code, created_by_user_id, created_at) VALUES (?, ?, ?)`,
			code, createdByUserID, now)
		if err == nil {
			return code, nil
		}
		if isUniqueViolation(err) {
			continue // astronomically unlikely collision; try a fresh code
		}
		return "", fmt.Errorf("create invite: %w", err)
	}
	return "", errors.New("create invite: exhausted unique-code retries")
}

func generateInviteCode() (string, error) {
	b := make([]byte, 20) // 160 bits
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// List returns all invites, newest first, with consumer/creator usernames joined
// (nil when the referenced user no longer exists).
func (r *InviteRepo) List(ctx context.Context) ([]Invite, error) {
	const q = `
SELECT i.id, i.code,
       i.created_by_user_id, cu.username,
       i.used_by_user_id,    uu.username,
       i.used_at, i.created_at
FROM invites i
LEFT JOIN users cu ON cu.id = i.created_by_user_id
LEFT JOIN users uu ON uu.id = i.used_by_user_id
ORDER BY i.id DESC`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()

	var out []Invite
	for rows.Next() {
		var (
			inv           Invite
			createdByID   sql.NullInt64
			createdByName sql.NullString
			usedByID      sql.NullInt64
			usedByName    sql.NullString
			usedAt        sql.NullString
			createdAt     string
		)
		if err := rows.Scan(&inv.ID, &inv.Code, &createdByID, &createdByName,
			&usedByID, &usedByName, &usedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		if createdByID.Valid {
			inv.CreatedByID = &createdByID.Int64
		}
		if createdByName.Valid {
			inv.CreatedByName = &createdByName.String
		}
		if usedByID.Valid {
			inv.UsedByID = &usedByID.Int64
		}
		if usedByName.Valid {
			inv.UsedByName = &usedByName.String
		}
		if usedAt.Valid {
			t, err := time.Parse(time.RFC3339, usedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse used_at: %w", err)
			}
			inv.UsedAt = &t
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		inv.CreatedAt = t
		out = append(out, inv)
	}
	return out, rows.Err()
}
