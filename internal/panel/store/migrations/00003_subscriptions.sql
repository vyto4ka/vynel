-- Subscriptions and HWID (docs/ARCHITECTURE.md §8).

-- +goose Up
ALTER TABLE users ADD COLUMN sub_last_at INTEGER;
ALTER TABLE users ADD COLUMN sub_last_ua TEXT NOT NULL DEFAULT '';

-- Per-inbound overrides of the automatically built connection point (remark, address, sni, hidden...).
ALTER TABLE node_inbounds ADD COLUMN host_json TEXT NOT NULL DEFAULT '{}';

CREATE TABLE user_devices (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    hwid       TEXT    NOT NULL,
    platform   TEXT    NOT NULL DEFAULT '',
    os_version TEXT    NOT NULL DEFAULT '',
    model      TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    last_ip    TEXT    NOT NULL DEFAULT '',
    UNIQUE (user_id, hwid)
);

-- +goose Down
DROP TABLE user_devices;
ALTER TABLE node_inbounds DROP COLUMN host_json;
ALTER TABLE users DROP COLUMN sub_last_ua;
ALTER TABLE users DROP COLUMN sub_last_at;
