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

var (
	ErrNodeNameTaken = errors.New("node name already exists for this user")
	ErrPubKeyTaken   = errors.New("public key already registered")
	ErrPoolExhausted = errors.New("no addresses available in the pool")
	ErrNodeNotFound  = errors.New("node not found")
)

// maxAllocRetries bounds the retry loop when concurrent registrations race for the
// same lowest-free address.
const maxAllocRetries = 100

type Node struct {
	ID        int64
	UserID    int64
	Name      string
	PubKey    string
	IP        netip.Addr
	CreatedAt time.Time
}

// NodeRepo provides access to the nodes table.
type NodeRepo struct {
	db *sql.DB
}

// Nodes returns a repository bound to this store.
func (s *Store) Nodes() *NodeRepo { return &NodeRepo{db: s.db} }

// Create allocates the lowest free address in [first, last] and inserts the node.
// On a concurrent ip collision it retries with the next lowest-free address; name
// and pubkey conflicts return typed errors; an empty pool returns ErrPoolExhausted.
func (r *NodeRepo) Create(ctx context.Context, userID int64, name, pubKey string, first, last uint32) (*Node, error) {
	now := time.Now().UTC().Truncate(time.Second)
	for range maxAllocRetries {
		ipVal, ok, err := r.lowestFree(ctx, first, last)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrPoolExhausted
		}
		const q = `INSERT INTO nodes (user_id, name, wg_pubkey, ip, created_at) VALUES (?, ?, ?, ?, ?)`
		res, err := r.db.ExecContext(ctx, q, userID, name, pubKey, ipVal, now.Format(time.RFC3339))
		if err != nil {
			switch {
			case isUniqueViolationOn(err, "nodes.ip"):
				continue // lost the race for this address; try the next free one
			case isUniqueViolationOn(err, "nodes.wg_pubkey"):
				return nil, ErrPubKeyTaken
			case isUniqueViolationOn(err, "nodes.user_id"), isUniqueViolationOn(err, "nodes.name"):
				return nil, ErrNodeNameTaken
			default:
				return nil, fmt.Errorf("insert node: %w", err)
			}
		}
		id, _ := res.LastInsertId()
		return &Node{
			ID:        id,
			UserID:    userID,
			Name:      name,
			PubKey:    pubKey,
			IP:        ipam.Uint32ToAddr(ipVal),
			CreatedAt: now,
		}, nil
	}
	return nil, fmt.Errorf("allocate address: exhausted %d retries under contention", maxAllocRetries)
}

// lowestFree returns the smallest free address (uint32) in [first, last], or ok=false
// when none is available.
func (r *NodeRepo) lowestFree(ctx context.Context, first, last uint32) (uint32, bool, error) {
	const q = `
SELECT c FROM (
    SELECT ? AS c
    UNION ALL
    SELECT ip + 1 FROM nodes WHERE ip >= ? AND ip < ?
) WHERE c NOT IN (SELECT ip FROM nodes) AND c <= ?
ORDER BY c LIMIT 1`
	var c int64
	err := r.db.QueryRowContext(ctx, q, first, first, last, last).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find free address: %w", err)
	}
	return uint32(c), true, nil
}

// GetByID returns any node by id, regardless of owner (used to resolve a kicked
// member's address — the owner's authority is over the zone, not the node).
func (r *NodeRepo) GetByID(ctx context.Context, nodeID int64) (*Node, error) {
	const q = `SELECT id, user_id, name, wg_pubkey, ip, created_at FROM nodes WHERE id = ?`
	var (
		n         Node
		ipVal     int64
		createdAt string
	)
	err := r.db.QueryRowContext(ctx, q, nodeID).
		Scan(&n.ID, &n.UserID, &n.Name, &n.PubKey, &ipVal, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node by id: %w", err)
	}
	n.IP = ipam.Uint32ToAddr(uint32(ipVal))
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	n.CreatedAt = t
	return &n, nil
}

// GetOwned returns the node only if it is owned by userID, else ErrNodeNotFound.
func (r *NodeRepo) GetOwned(ctx context.Context, userID, nodeID int64) (*Node, error) {
	const q = `SELECT id, user_id, name, wg_pubkey, ip, created_at FROM nodes WHERE id = ? AND user_id = ?`
	var (
		n         Node
		ipVal     int64
		createdAt string
	)
	err := r.db.QueryRowContext(ctx, q, nodeID, userID).
		Scan(&n.ID, &n.UserID, &n.Name, &n.PubKey, &ipVal, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get owned node: %w", err)
	}
	n.IP = ipam.Uint32ToAddr(uint32(ipVal))
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	n.CreatedAt = t
	return &n, nil
}

// ListByUser returns the user's nodes, newest first.
func (r *NodeRepo) ListByUser(ctx context.Context, userID int64) ([]Node, error) {
	const q = `SELECT id, user_id, name, wg_pubkey, ip, created_at FROM nodes WHERE user_id = ? ORDER BY id DESC`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// AllForPeers returns every node (for the startup peer rebuild).
func (r *NodeRepo) AllForPeers(ctx context.Context) ([]Node, error) {
	const q = `SELECT id, user_id, name, wg_pubkey, ip, created_at FROM nodes ORDER BY id`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// DeleteOwned removes the node only if owned by userID, returning its public key so
// the caller can remove the peer. ErrNodeNotFound if missing or not owned.
func (r *NodeRepo) DeleteOwned(ctx context.Context, userID, nodeID int64) (string, error) {
	var pubKey string
	err := r.db.QueryRowContext(ctx,
		`SELECT wg_pubkey FROM nodes WHERE id = ? AND user_id = ?`, nodeID, userID).Scan(&pubKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNodeNotFound
	}
	if err != nil {
		return "", fmt.Errorf("look up node: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ? AND user_id = ?`, nodeID, userID); err != nil {
		return "", fmt.Errorf("delete node: %w", err)
	}
	return pubKey, nil
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	var out []Node
	for rows.Next() {
		var (
			n         Node
			ipVal     int64
			createdAt string
		)
		if err := rows.Scan(&n.ID, &n.UserID, &n.Name, &n.PubKey, &ipVal, &createdAt); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		n.IP = ipam.Uint32ToAddr(uint32(ipVal))
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		n.CreatedAt = t
		out = append(out, n)
	}
	return out, rows.Err()
}

// isUniqueViolationOn reports whether err is a UNIQUE violation naming the given
// column/constraint (SQLite includes the column list in the message).
func isUniqueViolationOn(err error, col string) bool {
	return err != nil &&
		strings.Contains(err.Error(), "UNIQUE constraint failed") &&
		strings.Contains(err.Error(), col)
}
