-- +goose Up
CREATE TABLE refresh_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT    NOT NULL UNIQUE,
    expires_at TEXT    NOT NULL,
    revoked_at TEXT,
    created_at TEXT    NOT NULL
);

-- +goose Down
DROP TABLE refresh_tokens;
