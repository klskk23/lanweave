-- +goose Up
ALTER TABLE nodes ADD COLUMN platform TEXT NOT NULL DEFAULT 'unknown';

CREATE TABLE announcements (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id        INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    real_base      INTEGER NOT NULL,
    prefix_len     INTEGER NOT NULL,
    synthetic_base INTEGER NOT NULL,
    created_at     TEXT    NOT NULL,
    UNIQUE (node_id, real_base, prefix_len),
    UNIQUE (synthetic_base, prefix_len)
);

CREATE TABLE announcement_zones (
    announcement_id INTEGER NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    zone_id         INTEGER NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    UNIQUE (announcement_id, zone_id)
);

-- +goose Down
DROP TABLE announcement_zones;
DROP TABLE announcements;
ALTER TABLE nodes DROP COLUMN platform;
