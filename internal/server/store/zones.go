package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"lanweave/internal/server/ipam"
)

var (
	ErrZoneNameTaken  = errors.New("zone name already exists")
	ErrZoneOrPassword = errors.New("invalid zone or password")
	ErrNotMember      = errors.New("node is not a member of the zone")
)

type Zone struct {
	ID           int64
	Name         string
	PasswordHash string
	OwnerID      int64
	CreatedAt    time.Time
}

// ZoneWithOwnership is a zone the caller participates in.
type ZoneWithOwnership struct {
	Zone
	IsOwner bool
}

// ZoneMember is one member node of a zone (with its owning username).
type ZoneMember struct {
	NodeName  string
	IP        netip.Addr
	OwnerName string
}

// ZoneState is one zone's id and member addresses, for the startup nft rebuild.
type ZoneState struct {
	ID        int64
	MemberIPs []netip.Addr
}

type ZoneRepo struct {
	db *sql.DB
}

func (s *Store) Zones() *ZoneRepo { return &ZoneRepo{db: s.db} }

// Create inserts a zone owned by ownerID. Returns ErrZoneNameTaken on a name clash.
func (r *ZoneRepo) Create(ctx context.Context, ownerID int64, name, passwordHash string) (*Zone, error) {
	now := time.Now().UTC().Truncate(time.Second)
	const q = `INSERT INTO zones (name, password_hash, owner_user_id, created_at) VALUES (?, ?, ?, ?)`
	res, err := r.db.ExecContext(ctx, q, name, passwordHash, ownerID, now.Format(time.RFC3339))
	if err != nil {
		if isUniqueViolationOn(err, "zones.name") {
			return nil, ErrZoneNameTaken
		}
		return nil, fmt.Errorf("create zone: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Zone{ID: id, Name: name, PasswordHash: passwordHash, OwnerID: ownerID, CreatedAt: now}, nil
}

// Delete removes a zone (cascading its memberships). Used here only to compensate a
// failed create; user-facing zone deletion is feature 006.
func (r *ZoneRepo) Delete(ctx context.Context, zoneID int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM zones WHERE id = ?`, zoneID); err != nil {
		return fmt.Errorf("delete zone: %w", err)
	}
	return nil
}

// GetByName returns the zone, or (nil, nil) if no zone has that name.
func (r *ZoneRepo) GetByName(ctx context.Context, name string) (*Zone, error) {
	const q = `SELECT id, name, password_hash, owner_user_id, created_at FROM zones WHERE name = ?`
	var (
		z         Zone
		createdAt string
	)
	err := r.db.QueryRowContext(ctx, q, name).Scan(&z.ID, &z.Name, &z.PasswordHash, &z.OwnerID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get zone %q: %w", name, err)
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	z.CreatedAt = t
	return &z, nil
}

// Join adds a node to a zone. Idempotent: re-joining is a no-op.
func (r *ZoneRepo) Join(ctx context.Context, zoneID, nodeID int64) error {
	const q = `INSERT INTO zone_members (zone_id, node_id, joined_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`
	if _, err := r.db.ExecContext(ctx, q, zoneID, nodeID, time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)); err != nil {
		return fmt.Errorf("join zone: %w", err)
	}
	return nil
}

// Leave removes a node from a zone. ErrNotMember if it was not a member.
func (r *ZoneRepo) Leave(ctx context.Context, zoneID, nodeID int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM zone_members WHERE zone_id = ? AND node_id = ?`, zoneID, nodeID)
	if err != nil {
		return fmt.Errorf("leave zone: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotMember
	}
	return nil
}

// MembersByZone returns every member node with its name, address, and owning username.
func (r *ZoneRepo) MembersByZone(ctx context.Context, zoneID int64) ([]ZoneMember, error) {
	const q = `
SELECT n.name, n.ip, u.username
FROM zone_members zm
JOIN nodes n ON n.id = zm.node_id
JOIN users u ON u.id = n.user_id
WHERE zm.zone_id = ?
ORDER BY n.ip`
	rows, err := r.db.QueryContext(ctx, q, zoneID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []ZoneMember
	for rows.Next() {
		var (
			m     ZoneMember
			ipVal int64
		)
		if err := rows.Scan(&m.NodeName, &ipVal, &m.OwnerName); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		m.IP = ipam.Uint32ToAddr(uint32(ipVal))
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListForUser returns zones the user owns or has a member node in, newest first.
func (r *ZoneRepo) ListForUser(ctx context.Context, userID int64) ([]ZoneWithOwnership, error) {
	const q = `
SELECT z.id, z.name, z.owner_user_id
FROM zones z
WHERE z.owner_user_id = ?
   OR z.id IN (
       SELECT zm.zone_id FROM zone_members zm
       JOIN nodes n ON n.id = zm.node_id
       WHERE n.user_id = ?
   )
ORDER BY z.id DESC`
	rows, err := r.db.QueryContext(ctx, q, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()
	var out []ZoneWithOwnership
	for rows.Next() {
		var z ZoneWithOwnership
		if err := rows.Scan(&z.ID, &z.Name, &z.OwnerID); err != nil {
			return nil, fmt.Errorf("scan zone: %w", err)
		}
		z.IsOwner = z.OwnerID == userID
		out = append(out, z)
	}
	return out, rows.Err()
}

// IsParticipant reports whether the user owns the zone or has a member node in it.
func (r *ZoneRepo) IsParticipant(ctx context.Context, zoneID, userID int64) (bool, error) {
	const q = `SELECT EXISTS(
        SELECT 1 FROM zones WHERE id = ? AND owner_user_id = ?
        UNION ALL
        SELECT 1 FROM zone_members zm JOIN nodes n ON n.id = zm.node_id
        WHERE zm.zone_id = ? AND n.user_id = ?
    )`
	var ok bool
	if err := r.db.QueryRowContext(ctx, q, zoneID, userID, zoneID, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check participant: %w", err)
	}
	return ok, nil
}

// AllForRebuild returns every zone with its member addresses (empty zones included),
// for the startup nftables rebuild.
func (r *ZoneRepo) AllForRebuild(ctx context.Context) ([]ZoneState, error) {
	const q = `
SELECT z.id, n.ip
FROM zones z
LEFT JOIN zone_members zm ON zm.zone_id = z.id
LEFT JOIN nodes n ON n.id = zm.node_id
ORDER BY z.id`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("rebuild query: %w", err)
	}
	defer rows.Close()

	var (
		states []ZoneState
		idx    = map[int64]int{}
	)
	for rows.Next() {
		var (
			zoneID int64
			ipVal  sql.NullInt64
		)
		if err := rows.Scan(&zoneID, &ipVal); err != nil {
			return nil, fmt.Errorf("scan rebuild row: %w", err)
		}
		i, ok := idx[zoneID]
		if !ok {
			states = append(states, ZoneState{ID: zoneID})
			i = len(states) - 1
			idx[zoneID] = i
		}
		if ipVal.Valid {
			states[i].MemberIPs = append(states[i].MemberIPs, ipam.Uint32ToAddr(uint32(ipVal.Int64)))
		}
	}
	return states, rows.Err()
}

// ZonesForNode returns the ids of every zone a node belongs to.
func (r *ZoneRepo) ZonesForNode(ctx context.Context, nodeID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT zone_id FROM zone_members WHERE node_id = ?`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("zones for node: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan zone id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
