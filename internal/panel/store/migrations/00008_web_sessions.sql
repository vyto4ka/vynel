-- Web panel sessions kept on the server, so they can be listed and ended (docs/STEALTH.md §3.3).

-- +goose Up
CREATE TABLE web_sessions (
    id           TEXT    PRIMARY KEY, -- random; the signed cookie carries it
    method       TEXT    NOT NULL,    -- password | link | telegram
    actor        TEXT    NOT NULL DEFAULT '', -- login, or bot:<telegram id>
    ip           TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    revoked_at   INTEGER
);

-- +goose Down
DROP TABLE web_sessions;
