-- A user's own limit of addresses connected at once (docs/USER_GUIDE.md §6.1); NULL = the
-- default (ip.default_limit), 0 = unlimited.

-- +goose Up
ALTER TABLE users ADD COLUMN ip_limit INTEGER;

-- +goose Down
ALTER TABLE users DROP COLUMN ip_limit;
