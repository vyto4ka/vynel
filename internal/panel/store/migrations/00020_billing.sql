-- Infrastructure billing (docs/USER_GUIDE.md §9.1): hosting providers, what each node costs and
-- until when it is paid, and a monthly traffic limit after which the node is switched off. The
-- interface traffic for the limit is counted hourly from the node's metrics.

-- +goose Up
CREATE TABLE providers (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    url        TEXT    NOT NULL DEFAULT '', -- the provider's site
    login_url  TEXT    NOT NULL DEFAULT '', -- its client area, optional
    icon       TEXT    NOT NULL DEFAULT '', -- the site's icon as a data: URI
    created_at INTEGER NOT NULL
);

CREATE TABLE node_billing (
    node_id         INTEGER PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    provider_id     INTEGER REFERENCES providers (id) ON DELETE SET NULL,
    cost            REAL    NOT NULL DEFAULT 0,
    currency        TEXT    NOT NULL DEFAULT '₽',
    cost_period     TEXT    NOT NULL DEFAULT 'month', -- month | year
    paid_until      INTEGER,                          -- NULL = not tracked
    reminded_for    INTEGER NOT NULL DEFAULT 0,       -- the paid_until already reminded about
    traffic_limit   INTEGER NOT NULL DEFAULT 0,       -- bytes per period, 0 = none
    reset_day       INTEGER NOT NULL DEFAULT 1,       -- day of month the period starts
    traffic_count   TEXT    NOT NULL DEFAULT 'both',  -- both | out | in
    off_period      INTEGER NOT NULL DEFAULT 0,       -- period start for which the limit switched it off
    override_period INTEGER NOT NULL DEFAULT 0        -- period the admin switched it back on anyway
);

CREATE TABLE node_iface_hourly (
    node_id INTEGER NOT NULL,
    hour    INTEGER NOT NULL,
    rx      INTEGER NOT NULL DEFAULT 0, -- bytes into the server
    tx      INTEGER NOT NULL DEFAULT 0, -- bytes out of it
    PRIMARY KEY (node_id, hour)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE node_iface_hourly;
DROP TABLE node_billing;
DROP TABLE providers;
