-- Persistent update logs and run state for the panel (node_id = 0) and nodes (node_id > 0),
-- so logs and statuses survive panel/node restarts.

-- +goose Up
CREATE TABLE update_runs (
    node_id      INTEGER PRIMARY KEY,
    from_version TEXT    NOT NULL DEFAULT '',
    to_version   TEXT    NOT NULL DEFAULT '',
    state        TEXT    NOT NULL DEFAULT '',
    ok           INTEGER NOT NULL DEFAULT 0,
    sent_at      INTEGER NOT NULL DEFAULT 0,
    started_at   INTEGER NOT NULL DEFAULT 0,
    finished_at  INTEGER NOT NULL DEFAULT 0,
    dismissed_at INTEGER NOT NULL DEFAULT 0,
    log          TEXT    NOT NULL DEFAULT '',
    updated_at   INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE update_runs;
