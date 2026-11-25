-- Custom decoy site path (directory with index.html or HTML file) on the node's filesystem.
-- Empty means the built-in decoy site.

-- +goose Up
ALTER TABLE nodes ADD COLUMN decoy_path TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE nodes DROP COLUMN decoy_path;
