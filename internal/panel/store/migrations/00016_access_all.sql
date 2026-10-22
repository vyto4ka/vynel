-- A group may get everything the servers offer (access kind 'all'): every inbound and every
-- cascade, those added later too. The default group gets it (docs/USER_GUIDE.md §3).

-- +goose Up
CREATE TABLE group_access_new (
    id       INTEGER PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL CHECK (kind IN ('node_inbound', 'profile', 'node', 'all')),
    ref_id   INTEGER NOT NULL,
    UNIQUE (group_id, kind, ref_id)
);
INSERT INTO group_access_new (id, group_id, kind, ref_id) SELECT id, group_id, kind, ref_id FROM group_access;
DROP TABLE group_access;
ALTER TABLE group_access_new RENAME TO group_access;
INSERT OR IGNORE INTO group_access (group_id, kind, ref_id) SELECT id, 'all', 0 FROM groups WHERE name = 'Основная';

-- +goose Down
DELETE FROM group_access WHERE kind = 'all';
