-- +goose Up
CREATE TABLE invites (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    code               TEXT    NOT NULL UNIQUE,
    created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    used_by_user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    used_at            TEXT,
    created_at         TEXT    NOT NULL
);

-- +goose Down
DROP TABLE invites;
