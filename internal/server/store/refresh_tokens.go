package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// refreshTTL is the sliding lifetime of a refresh token. Each successful
// validation slides expires_at to now+refreshTTL, so an actively used session
// never expires while an abandoned device stops working after this idle window.
// Fixed (not configurable) this slice (research D3).
const refreshTTL = 30 * 24 * time.Hour

// refreshTokenBytes is the entropy of a refresh token before encoding. 32 bytes
// (256 bits) is far beyond brute-force reach, which is why the table stores only a
// fast SHA-256 digest (not argon2id): there is no low-entropy secret to protect,
// and a deterministic digest keeps token_hash a usable UNIQUE lookup key.
const refreshTokenBytes = 32

// ErrRefreshInvalid is returned when a refresh token is unknown, revoked, or
// expired. It is deliberately undifferentiated so callers cannot use it to probe
// token state.
var ErrRefreshInvalid = errors.New("refresh token invalid")

// RefreshTokenRepo owns the refresh_tokens table: issue, validate (with slide),
// and revoke. The server stores only the SHA-256 hash of each token; the opaque
// plaintext is returned to the caller once at issuance.
type RefreshTokenRepo struct {
	db *sql.DB
	// now is injectable so expiry/sliding can be tested with a controlled clock
	// instead of wall-clock sleeps (Constitution II). Defaults to time.Now().UTC.
	now func() time.Time
}

// RefreshTokens returns the refresh-token repository.
func (s *Store) RefreshTokens() *RefreshTokenRepo {
	return &RefreshTokenRepo{
		db:  s.db,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the repo's time source. Tests use it to drive expiry and the
// sliding window deterministically instead of sleeping on the wall clock.
func (r *RefreshTokenRepo) SetClock(now func() time.Time) { r.now = now }

// hashRefreshToken maps a plaintext refresh token to the lowercase-hex SHA-256
// digest stored in token_hash. A fast hash is correct: the input is high-entropy
// random (refreshTokenBytes), not a password, so there is nothing to brute-force.
func hashRefreshToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// Issue mints a new opaque refresh token for userID, persists only its hash with a
// now+refreshTTL expiry, and returns the plaintext exactly once. The plaintext is
// never stored server-side and never logged.
func (r *RefreshTokenRepo) Issue(ctx context.Context, userID int64) (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(b)

	now := r.now().Truncate(time.Second)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at, created_at)
		 VALUES (?, ?, ?, NULL, ?)`,
		userID, hashRefreshToken(plaintext),
		now.Add(refreshTTL).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return "", fmt.Errorf("insert refresh token: %w", err)
	}
	return plaintext, nil
}

// Validate checks an opaque refresh token and, if it is live, slides its expiry to
// now+refreshTTL and returns the owning user id. An unknown, revoked, or expired
// token yields ErrRefreshInvalid (undifferentiated — never an oracle for token state).
func (r *RefreshTokenRepo) Validate(ctx context.Context, plaintext string) (int64, error) {
	now := r.now().Truncate(time.Second)
	hash := hashRefreshToken(plaintext)

	var (
		userID    int64
		expiresAt string
		revokedAt sql.NullString
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT user_id, expires_at, revoked_at FROM refresh_tokens WHERE token_hash = ?`, hash).
		Scan(&userID, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrRefreshInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("select refresh token: %w", err)
	}
	if revokedAt.Valid {
		return 0, ErrRefreshInvalid
	}
	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return 0, fmt.Errorf("parse refresh expiry: %w", err)
	}
	if !exp.After(now) {
		return 0, ErrRefreshInvalid
	}

	// Slide the window forward on every successful use.
	if _, err := r.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET expires_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		now.Add(refreshTTL).Format(time.RFC3339), hash); err != nil {
		return 0, fmt.Errorf("slide refresh expiry: %w", err)
	}
	return userID, nil
}

// Revoke marks a refresh token revoked so it can no longer be validated. It is
// idempotent: an unknown or already-revoked token is a no-op (nil error), so a
// repeated logout never fails and the endpoint is never an oracle for token state.
// Deleting the owning user needs no call here — the FK ON DELETE CASCADE removes the
// rows. The revocation is permanent (only the access-token TTL bounds the window
// during which an already-minted JWT keeps working).
func (r *RefreshTokenRepo) Revoke(ctx context.Context, plaintext string) error {
	now := r.now().Truncate(time.Second)
	if _, err := r.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		now.Format(time.RFC3339), hashRefreshToken(plaintext)); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}
