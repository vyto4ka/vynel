-- The «Аналитика» page: each user's traffic by profile and the addresses users connected from.
-- A client's Xray email names where it is ("<id>.i<inbound>", "<id>.c<cascade>"), and its
-- suffix is the ref here; both tables are kept 90 days.

-- +goose Up
CREATE TABLE user_ref_daily (
    user_id INTEGER NOT NULL,
    ref     TEXT    NOT NULL, -- "i<node inbound id>", "c<cascade id>" or "" (recorded before refs)
    day     INTEGER NOT NULL,
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, user_id, ref)
) WITHOUT ROWID;

CREATE TABLE user_ips (
    user_id  INTEGER NOT NULL,
    ip       TEXT    NOT NULL,
    node_id  INTEGER NOT NULL, -- where it was seen last
    first_at INTEGER NOT NULL,
    last_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, ip)
) WITHOUT ROWID;
CREATE INDEX user_ips_last ON user_ips (last_at);

-- +goose Down
DROP TABLE user_ips;
DROP TABLE user_ref_daily;
