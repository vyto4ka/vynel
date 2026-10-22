-- Node load over time for the dashboard: CPU steal is kept apart, and the 10-second samples
-- (kept 7 days) are rolled up into 5-minute rows kept 90 days (docs/USER_GUIDE.md §7).

-- +goose Up
ALTER TABLE node_metrics ADD COLUMN steal REAL NOT NULL DEFAULT 0;

CREATE TABLE node_metrics_5m (
    node_id   INTEGER NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    ts        INTEGER NOT NULL, -- start of the 5 minutes
    cpu       REAL    NOT NULL, -- average percent
    cpu_max   REAL    NOT NULL,
    steal     REAL    NOT NULL, -- average percent
    mem_used  INTEGER NOT NULL, -- average bytes
    mem_total INTEGER NOT NULL,
    rx_bps    INTEGER NOT NULL, -- average bytes per second
    tx_bps    INTEGER NOT NULL,
    rx_max    INTEGER NOT NULL,
    tx_max    INTEGER NOT NULL,
    online    INTEGER NOT NULL, -- most users online
    PRIMARY KEY (node_id, ts)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE node_metrics_5m;
ALTER TABLE node_metrics DROP COLUMN steal;
