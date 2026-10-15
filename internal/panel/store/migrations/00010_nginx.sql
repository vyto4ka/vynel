-- nginx replaces Caddy on nodes (docs/ARCHITECTURE.md §4.4). Agents report what they can run;
-- the panel sends an nginx config to agents with the "nginx" capability and a Caddy config
-- to older ones.

-- +goose Up
ALTER TABLE nodes ADD COLUMN nginx_version TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN capabilities TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE nodes DROP COLUMN capabilities;
ALTER TABLE nodes DROP COLUMN nginx_version;
