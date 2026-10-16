-- Cascades (traffic enters one node and leaves through another), subscriptions merged in from
-- other links, and who gets them: a group or a user, each user with its own end date
-- (docs/USER_GUIDE.md §5.2, §5.3).

-- +goose Up
CREATE TABLE cascades (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL UNIQUE,
    entry_inbound_id INTEGER NOT NULL REFERENCES node_inbounds (id) ON DELETE CASCADE,
    exit_inbound_id  INTEGER NOT NULL REFERENCES node_inbounds (id) ON DELETE CASCADE,
    remark           TEXT    NOT NULL DEFAULT '',
    service_uuid     TEXT    NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       INTEGER NOT NULL
);

CREATE TABLE external_subs (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    url        TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    links      TEXT    NOT NULL DEFAULT '',
    fetched_at INTEGER,
    error      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE grants (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER REFERENCES users (id) ON DELETE CASCADE,
    group_id   INTEGER REFERENCES groups (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,
    ref_id     INTEGER NOT NULL,
    expire_at  INTEGER,
    created_at INTEGER NOT NULL,
    CHECK ((user_id IS NULL) <> (group_id IS NULL))
);
CREATE UNIQUE INDEX grants_user ON grants (user_id, kind, ref_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX grants_group ON grants (group_id, kind, ref_id) WHERE group_id IS NOT NULL;

-- +goose Down
DROP TABLE grants;
DROP TABLE external_subs;
DROP TABLE cascades;
