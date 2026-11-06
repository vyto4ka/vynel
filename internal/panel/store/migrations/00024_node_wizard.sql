-- node_wizard: requirements confirmation for the node setup wizard. The wizard's step state is
-- derived (no profiles = step 2, node variables empty = step 3, requirements unconfirmed = step 4);
-- only the confirmation itself is stored. Local nodes start confirmed: the panel itself checks
-- nothing on them.

-- +goose Up
ALTER TABLE nodes ADD COLUMN requirements_confirmed_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE nodes DROP COLUMN requirements_confirmed_at;
