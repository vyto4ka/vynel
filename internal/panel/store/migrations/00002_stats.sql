-- Statistics (docs/ARCHITECTURE.md §9). Hourly per-user rows are kept without the node split
-- so they do not multiply by the number of nodes; the per-node split is daily.

-- +goose Up
ALTER TABLE nodes ADD COLUMN stats_epoch TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN stats_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN warnings TEXT NOT NULL DEFAULT '[]';
ALTER TABLE nodes ADD COLUMN caddy_version TEXT NOT NULL DEFAULT '';

CREATE TABLE user_traffic_hourly (
    user_id INTEGER NOT NULL,
    hour    INTEGER NOT NULL, -- unix time truncated to the hour
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, hour)
) WITHOUT ROWID;

CREATE TABLE user_traffic_daily (
    user_id INTEGER NOT NULL,
    day     INTEGER NOT NULL, -- unix time truncated to the day (UTC)
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
) WITHOUT ROWID;

CREATE TABLE user_node_daily (
    user_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    day     INTEGER NOT NULL,
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, node_id, day)
) WITHOUT ROWID;

CREATE TABLE node_traffic_hourly (
    node_id INTEGER NOT NULL,
    hour    INTEGER NOT NULL,
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, hour)
) WITHOUT ROWID;

CREATE TABLE node_metrics (
    node_id   INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    cpu       REAL    NOT NULL,
    mem_used  INTEGER NOT NULL,
    mem_total INTEGER NOT NULL,
    load1     REAL    NOT NULL,
    rx_bps    INTEGER NOT NULL,
    tx_bps    INTEGER NOT NULL,
    uptime    INTEGER NOT NULL,
    online    INTEGER NOT NULL,
    PRIMARY KEY (node_id, ts)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE node_metrics;
DROP TABLE node_traffic_hourly;
DROP TABLE user_node_daily;
DROP TABLE user_traffic_daily;
DROP TABLE user_traffic_hourly;
ALTER TABLE nodes DROP COLUMN caddy_version;
ALTER TABLE nodes DROP COLUMN warnings;
ALTER TABLE nodes DROP COLUMN stats_seq;
ALTER TABLE nodes DROP COLUMN stats_epoch;
