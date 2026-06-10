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
	ErrAnnounceLimit          = errors.New("announced subnet limit reached for this user")
	ErrSubnetOverlap          = errors.New("subnet overlaps an existing announcement of the same node")
	ErrSyntheticPoolExhausted = errors.New("no free synthetic block in the pool")
	ErrAnnouncementNotFound   = errors.New("announcement not found")
)

// Announcement is one (node, real subnet) → synthetic subnet mapping. NodeName
// and Owner are joined display fields, populated by listing queries.
type Announcement struct {
	ID        int64
	NodeID    int64
	Real      ipam.Block
	Synthetic ipam.Block
	CreatedAt time.Time
	NodeName  string
	Owner     string
}

// AnnouncementRepo provides access to announcements and their zone attachments.
type AnnouncementRepo struct {
	db *sql.DB
}

// Announcements returns a repository bound to this store.
func (s *Store) Announcements() *AnnouncementRepo { return &AnnouncementRepo{db: s.db} }

// Create announces the node's real subnet into the zone, allocating a synthetic
// block on first announcement and reusing it when the same (node, subnet) is
// attached to further zones. The whole operation — membership check, self-overlap
// check, quota check (limit > 0; <= 0 is unlimited), allocation and inserts — runs
// in one transaction, so SQLite's writer lock serializes concurrent announcements
// and the pool can never double-allocate. Attaching an already attached zone is
// idempotent: the existing announcement is returned with attached=false so the
// caller can skip dataplane work.
func (r *AnnouncementRepo) Create(ctx context.Context, userID, nodeID int64, real ipam.Block, zoneID int64, limit int, pool netip.Prefix) (ann *Announcement, attached bool, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin announce tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Membership is checked inside the transaction: the attachment must never be
	// inserted for a node that already left the zone.
	var one int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM zone_members WHERE zone_id = ? AND node_id = ?`, zoneID, nodeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotMember
	}
	if err != nil {
		return nil, false, fmt.Errorf("check membership: %w", err)
	}

	// Reuse path: same (node, real subnet) keeps its synthetic block (FR-005).
	var (
		annID     int64
		synthBase int64
		createdAt string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, synthetic_base, created_at FROM announcements
		 WHERE node_id = ? AND real_base = ? AND prefix_len = ?`,
		nodeID, int64(real.Base), real.PrefixLen).Scan(&annID, &synthBase, &createdAt)
	switch {
	case err == nil:
		res, aerr := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO announcement_zones (announcement_id, zone_id) VALUES (?, ?)`,
			annID, zoneID)
		if aerr != nil {
			return nil, false, fmt.Errorf("attach zone: %w", aerr)
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit attach: %w", err)
		}
		n, _ := res.RowsAffected()
		ann, err := announcementFromRow(annID, nodeID, real, uint32(synthBase), createdAt)
		return ann, n > 0, err
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, fmt.Errorf("look up announcement: %w", err)
	}

	// New announcement: same-node overlap is a caller error; cross-node overlap is
	// the whole point of synthetic mapping and is deliberately not checked.
	rows, err := tx.QueryContext(ctx,
		`SELECT real_base, prefix_len FROM announcements WHERE node_id = ?`, nodeID)
	if err != nil {
		return nil, false, fmt.Errorf("list node announcements: %w", err)
	}
	nodeBlocks, err := scanBlocks(rows)
	if err != nil {
		return nil, false, err
	}
	for _, b := range nodeBlocks {
		if ipam.Overlaps(real, b) {
			return nil, false, ErrSubnetOverlap
		}
	}

	if limit > 0 {
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM announcements a JOIN nodes n ON n.id = a.node_id WHERE n.user_id = ?`,
			userID).Scan(&count); err != nil {
			return nil, false, fmt.Errorf("count user announcements: %w", err)
		}
		if count >= limit {
			return nil, false, ErrAnnounceLimit
		}
	}

	rows, err = tx.QueryContext(ctx, `SELECT synthetic_base, prefix_len FROM announcements`)
	if err != nil {
		return nil, false, fmt.Errorf("list synthetic blocks: %w", err)
	}
	used, err := scanBlocks(rows)
	if err != nil {
		return nil, false, err
	}
	synthetic, err := ipam.AllocateBlock(pool, real.PrefixLen, used)
	if err != nil {
		if errors.Is(err, ipam.ErrNoSpace) {
			return nil, false, ErrSyntheticPoolExhausted
		}
		return nil, false, fmt.Errorf("allocate synthetic block: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO announcements (node_id, real_base, prefix_len, synthetic_base, created_at) VALUES (?, ?, ?, ?, ?)`,
		nodeID, int64(real.Base), real.PrefixLen, int64(synthetic.Base), now)
	if err != nil {
		return nil, false, fmt.Errorf("insert announcement: %w", err)
	}
	annID, _ = res.LastInsertId()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO announcement_zones (announcement_id, zone_id) VALUES (?, ?)`, annID, zoneID); err != nil {
		return nil, false, fmt.Errorf("attach zone: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit announce: %w", err)
	}
	created, err := announcementFromRow(annID, nodeID, real, synthetic.Base, now)
	return created, true, err
}

// Detach removes the announcement's attachment to the zone; when that was the
// last attachment the announcement body is deleted in the same transaction and
// its synthetic block becomes reusable. It returns the announcement (for the
// caller's dataplane shrink) and whether the body was reclaimed.
// ErrAnnouncementNotFound when the announcement does not exist or is not
// attached to this zone.
func (r *AnnouncementRepo) Detach(ctx context.Context, zoneID, annID int64) (*Announcement, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin detach tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ann, err := getAnnouncementTx(ctx, tx, annID)
	if err != nil {
		return nil, false, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM announcement_zones WHERE announcement_id = ? AND zone_id = ?`, annID, zoneID)
	if err != nil {
		return nil, false, fmt.Errorf("detach zone: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, false, ErrAnnouncementNotFound
	}
	var remaining int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM announcement_zones WHERE announcement_id = ?`, annID).Scan(&remaining); err != nil {
		return nil, false, fmt.Errorf("count attachments: %w", err)
	}
	reclaimed := remaining == 0
	if reclaimed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM announcements WHERE id = ?`, annID); err != nil {
			return nil, false, fmt.Errorf("delete announcement: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit detach: %w", err)
	}
	return ann, reclaimed, nil
}

// Get returns one announcement by id (no zone scoping — callers authorize).
func (r *AnnouncementRepo) Get(ctx context.Context, annID int64) (*Announcement, error) {
	return getAnnouncementTx(ctx, r.db, annID)
}

// rowQueryer is satisfied by both *sql.DB and *sql.Tx.
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getAnnouncementTx(ctx context.Context, tx rowQueryer, annID int64) (*Announcement, error) {
	var (
		nodeID              int64
		realBase, synthBase int64
		prefixLen           int
		createdAt           string
	)
	err := tx.QueryRowContext(ctx,
		`SELECT node_id, real_base, prefix_len, synthetic_base, created_at FROM announcements WHERE id = ?`,
		annID).Scan(&nodeID, &realBase, &prefixLen, &synthBase, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAnnouncementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get announcement: %w", err)
	}
	return announcementFromRow(annID, nodeID, ipam.Block{Base: uint32(realBase), PrefixLen: prefixLen}, uint32(synthBase), createdAt)
}

// ListByZone returns the zone's announcements with display fields, oldest first.
func (r *AnnouncementRepo) ListByZone(ctx context.Context, zoneID int64) ([]Announcement, error) {
	const q = `
SELECT a.id, a.node_id, a.real_base, a.prefix_len, a.synthetic_base, a.created_at, n.name, u.username
FROM announcement_zones az
JOIN announcements a ON a.id = az.announcement_id
JOIN nodes n ON n.id = a.node_id
JOIN users u ON u.id = n.user_id
WHERE az.zone_id = ?
ORDER BY a.id`
	rows, err := r.db.QueryContext(ctx, q, zoneID)
	if err != nil {
		return nil, fmt.Errorf("list zone announcements: %w", err)
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var (
			a                   Announcement
			realBase, synthBase int64
			prefixLen           int
			createdAt           string
		)
		if err := rows.Scan(&a.ID, &a.NodeID, &realBase, &prefixLen, &synthBase, &createdAt, &a.NodeName, &a.Owner); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		filled, err := announcementFromRow(a.ID, a.NodeID, ipam.Block{Base: uint32(realBase), PrefixLen: prefixLen}, uint32(synthBase), createdAt)
		if err != nil {
			return nil, err
		}
		filled.NodeName, filled.Owner = a.NodeName, a.Owner
		out = append(out, *filled)
	}
	return out, rows.Err()
}

// RoutesForNode returns the node's announced synthetic blocks — the extra
// AllowedIPs prefixes its peer carries. Used whenever a peer is (re)configured.
func (r *AnnouncementRepo) RoutesForNode(ctx context.Context, nodeID int64) ([]netip.Prefix, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT synthetic_base, prefix_len FROM announcements WHERE node_id = ? ORDER BY id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node routes: %w", err)
	}
	blocks, err := scanBlocks(rows)
	if err != nil {
		return nil, err
	}
	return blocksToPrefixes(blocks), nil
}

// RoutesByZone returns every zone's attached synthetic blocks (startup rebuild).
func (r *AnnouncementRepo) RoutesByZone(ctx context.Context) (map[int64][]netip.Prefix, error) {
	const q = `
SELECT az.zone_id, a.synthetic_base, a.prefix_len
FROM announcement_zones az JOIN announcements a ON a.id = az.announcement_id
ORDER BY az.zone_id, a.id`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list zone routes: %w", err)
	}
	defer rows.Close()
	out := map[int64][]netip.Prefix{}
	for rows.Next() {
		var (
			zoneID, base int64
			prefixLen    int
		)
		if err := rows.Scan(&zoneID, &base, &prefixLen); err != nil {
			return nil, fmt.Errorf("scan zone route: %w", err)
		}
		out[zoneID] = append(out[zoneID], ipam.Block{Base: uint32(base), PrefixLen: prefixLen}.Prefix())
	}
	return out, rows.Err()
}

// RoutesByNode returns every node's announced synthetic blocks keyed by node id
// (startup peer rebuild).
func (r *AnnouncementRepo) RoutesByNode(ctx context.Context) (map[int64][]netip.Prefix, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT node_id, synthetic_base, prefix_len FROM announcements ORDER BY node_id, id`)
	if err != nil {
		return nil, fmt.Errorf("list node routes: %w", err)
	}
	defer rows.Close()
	out := map[int64][]netip.Prefix{}
	for rows.Next() {
		var (
			nodeID, base int64
			prefixLen    int
		)
		if err := rows.Scan(&nodeID, &base, &prefixLen); err != nil {
			return nil, fmt.Errorf("scan node route: %w", err)
		}
		out[nodeID] = append(out[nodeID], ipam.Block{Base: uint32(base), PrefixLen: prefixLen}.Prefix())
	}
	return out, rows.Err()
}

// DetachAllForNodeZone removes the node's attachments to one zone (leave/kick),
// reclaiming announcements whose last attachment vanished. It returns, per
// affected zone-detached announcement, the synthetic prefix and whether the body
// was reclaimed, so the caller can shrink the dataplane precisely.
type DetachedRoute struct {
	AnnouncementID int64
	Synthetic      netip.Prefix
	Reclaimed      bool
}

func (r *AnnouncementRepo) DetachAllForNodeZone(ctx context.Context, nodeID, zoneID int64) ([]DetachedRoute, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin detach-all tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT a.id, a.synthetic_base, a.prefix_len
FROM announcement_zones az JOIN announcements a ON a.id = az.announcement_id
WHERE az.zone_id = ? AND a.node_id = ?`, zoneID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node-zone attachments: %w", err)
	}
	type rec struct {
		id    int64
		block ipam.Block
	}
	var recs []rec
	for rows.Next() {
		var (
			id, base  int64
			prefixLen int
		)
		if err := rows.Scan(&id, &base, &prefixLen); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		recs = append(recs, rec{id: id, block: ipam.Block{Base: uint32(base), PrefixLen: prefixLen}})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []DetachedRoute
	for _, rc := range recs {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM announcement_zones WHERE announcement_id = ? AND zone_id = ?`, rc.id, zoneID); err != nil {
			return nil, fmt.Errorf("detach: %w", err)
		}
		var remaining int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM announcement_zones WHERE announcement_id = ?`, rc.id).Scan(&remaining); err != nil {
			return nil, fmt.Errorf("count attachments: %w", err)
		}
		reclaimed := remaining == 0
		if reclaimed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM announcements WHERE id = ?`, rc.id); err != nil {
				return nil, fmt.Errorf("reclaim announcement: %w", err)
			}
		}
		out = append(out, DetachedRoute{AnnouncementID: rc.id, Synthetic: rc.block.Prefix(), Reclaimed: reclaimed})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit detach-all: %w", err)
	}
	return out, nil
}

func announcementFromRow(id, nodeID int64, real ipam.Block, synthBase uint32, createdAt string) (*Announcement, error) {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return &Announcement{
		ID:        id,
		NodeID:    nodeID,
		Real:      real,
		Synthetic: ipam.Block{Base: synthBase, PrefixLen: real.PrefixLen},
		CreatedAt: t,
	}, nil
}

func scanBlocks(rows *sql.Rows) ([]ipam.Block, error) {
	defer rows.Close()
	var out []ipam.Block
	for rows.Next() {
		var (
			base      int64
			prefixLen int
		)
		if err := rows.Scan(&base, &prefixLen); err != nil {
			return nil, fmt.Errorf("scan block: %w", err)
		}
		out = append(out, ipam.Block{Base: uint32(base), PrefixLen: prefixLen})
	}
	return out, rows.Err()
}

func blocksToPrefixes(blocks []ipam.Block) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.Prefix())
	}
	return out
}

// ZoneDetachment describes one attachment removed by a zone-scoped cascade,
// carrying everything the caller needs to shrink the dataplane.
type ZoneDetachment struct {
	AnnouncementID int64
	NodeID         int64
	ZoneID         int64
	Synthetic      netip.Prefix
	Reclaimed      bool
}

// AttachmentsForNode returns the node's current (zone, synthetic) attachments —
// a pre-delete snapshot for the node-deletion cascade (the announcement rows
// themselves die with the node via ON DELETE CASCADE; the zone routes sets do
// not and must be shrunk element by element).
func (r *AnnouncementRepo) AttachmentsForNode(ctx context.Context, nodeID int64) ([]ZoneDetachment, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT az.announcement_id, az.zone_id, a.synthetic_base, a.prefix_len
FROM announcement_zones az JOIN announcements a ON a.id = az.announcement_id
WHERE a.node_id = ?`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node attachments: %w", err)
	}
	defer rows.Close()
	var out []ZoneDetachment
	for rows.Next() {
		var (
			annID, zoneID, base int64
			prefixLen           int
		)
		if err := rows.Scan(&annID, &zoneID, &base, &prefixLen); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		out = append(out, ZoneDetachment{
			AnnouncementID: annID, NodeID: nodeID, ZoneID: zoneID,
			Synthetic: ipam.Block{Base: uint32(base), PrefixLen: prefixLen}.Prefix(),
		})
	}
	return out, rows.Err()
}

// DetachAllForZone removes every attachment of the zone (zone deletion cascade),
// reclaiming announcements whose last attachment vanished — without this, an
// announcement another user made into this zone alone would leak its synthetic
// block forever. Runs in one transaction; the caller shrinks peers for the
// reclaimed entries.
func (r *AnnouncementRepo) DetachAllForZone(ctx context.Context, zoneID int64) ([]ZoneDetachment, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin zone-detach tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT az.announcement_id, a.node_id, a.synthetic_base, a.prefix_len
FROM announcement_zones az JOIN announcements a ON a.id = az.announcement_id
WHERE az.zone_id = ?`, zoneID)
	if err != nil {
		return nil, fmt.Errorf("list zone attachments: %w", err)
	}
	var dets []ZoneDetachment
	for rows.Next() {
		var (
			annID, nodeID, base int64
			prefixLen           int
		)
		if err := rows.Scan(&annID, &nodeID, &base, &prefixLen); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		dets = append(dets, ZoneDetachment{
			AnnouncementID: annID, NodeID: nodeID, ZoneID: zoneID,
			Synthetic: ipam.Block{Base: uint32(base), PrefixLen: prefixLen}.Prefix(),
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM announcement_zones WHERE zone_id = ?`, zoneID); err != nil {
		return nil, fmt.Errorf("detach zone: %w", err)
	}
	for i := range dets {
		var remaining int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM announcement_zones WHERE announcement_id = ?`, dets[i].AnnouncementID).Scan(&remaining); err != nil {
			return nil, fmt.Errorf("count attachments: %w", err)
		}
		if remaining == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM announcements WHERE id = ?`, dets[i].AnnouncementID); err != nil {
				return nil, fmt.Errorf("reclaim announcement: %w", err)
			}
			dets[i].Reclaimed = true
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit zone-detach: %w", err)
	}
	return dets, nil
}
