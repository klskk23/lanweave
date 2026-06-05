-- +goose Up
CREATE TABLE nodes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    wg_pubkey  TEXT    NOT NULL UNIQUE,
    ip         INTEGER NOT NULL UNIQUE,
    created_at TEXT    NOT NULL,
    UNIQUE (user_id, name)
);

-- +goose Down
DROP TABLE nodes;
