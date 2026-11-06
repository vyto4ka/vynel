-- A manual node: the panel manages only the Xray config; nginx, certificates, sysctl and the
-- firewall stay with the admin. Set when the node is created (or by the manual install mode).

-- +goose Up
ALTER TABLE nodes ADD COLUMN manual INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE nodes DROP COLUMN manual;
