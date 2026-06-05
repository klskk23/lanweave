-- +goose Up
CREATE TABLE zones (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TEXT    NOT NULL
);

CREATE TABLE zone_members (
    zone_id   INTEGER NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    node_id   INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    joined_at TEXT    NOT NULL,
    PRIMARY KEY (zone_id, node_id)
);

-- +goose Down
DROP TABLE zone_members;
DROP TABLE zones;
