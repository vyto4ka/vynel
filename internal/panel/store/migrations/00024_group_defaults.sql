-- +goose Up
ALTER TABLE groups ADD COLUMN user_defaults TEXT NOT NULL DEFAULT 'null';

-- +goose Down
ALTER TABLE groups DROP COLUMN user_defaults;
