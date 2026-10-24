-- Grants point at cascades and external subscriptions by id without a foreign key (one column
-- serves both). A cascade also goes when its inbound or node does, by the foreign key, which
-- left its grants behind; and SQLite gave a deleted row's id to the next one, so a new cascade
-- inherited them, as did user templates naming the old id. Now the ids are never reused and a
-- deleted row takes its grants with it, however it was deleted.

-- +goose Up
CREATE TABLE cascades_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL UNIQUE,
    entry_inbound_id INTEGER NOT NULL REFERENCES node_inbounds (id) ON DELETE CASCADE,
    exit_inbound_id  INTEGER NOT NULL REFERENCES node_inbounds (id) ON DELETE CASCADE,
    remark           TEXT    NOT NULL DEFAULT '',
    service_uuid     TEXT    NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       INTEGER NOT NULL
);
INSERT INTO cascades_new SELECT id, name, entry_inbound_id, exit_inbound_id, remark, service_uuid, enabled, created_at FROM cascades;
DROP TABLE cascades;
ALTER TABLE cascades_new RENAME TO cascades;

CREATE TABLE external_subs_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    url        TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    links      TEXT    NOT NULL DEFAULT '',
    fetched_at INTEGER,
    error      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
INSERT INTO external_subs_new SELECT id, name, url, enabled, links, fetched_at, error, created_at FROM external_subs;
DROP TABLE external_subs;
ALTER TABLE external_subs_new RENAME TO external_subs;

-- Grants left behind by cascades that are gone.
DELETE FROM grants WHERE kind = 'cascade' AND ref_id NOT IN (SELECT id FROM cascades);
DELETE FROM grants WHERE kind = 'external' AND ref_id NOT IN (SELECT id FROM external_subs);

-- +goose StatementBegin
CREATE TRIGGER cascades_grants AFTER DELETE ON cascades BEGIN
    DELETE FROM grants WHERE kind = 'cascade' AND ref_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER external_subs_grants AFTER DELETE ON external_subs BEGIN
    DELETE FROM grants WHERE kind = 'external' AND ref_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER external_subs_grants;
DROP TRIGGER cascades_grants;
