-- +goose Up
-- Optional expiry for invite codes. NULL = never expires, which covers both
-- pre-existing rows (grandfathered on upgrade) and codes minted while the
-- configured invite_ttl is 0/empty. Enforcement lives at registration.
ALTER TABLE invites ADD COLUMN expires_at TEXT;

-- +goose Down
ALTER TABLE invites DROP COLUMN expires_at;
