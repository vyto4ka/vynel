-- Per-user HWID switch: 1 = the user's subscription skips the HWID check entirely.

-- +goose Up
ALTER TABLE users ADD COLUMN hwid_off INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN hwid_off;
