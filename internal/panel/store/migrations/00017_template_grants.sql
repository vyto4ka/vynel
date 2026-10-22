-- A user template may give cascades and merged subscriptions: a new user from it gets them as
-- own grants (docs/USER_GUIDE.md §5.4).

-- +goose Up
ALTER TABLE user_templates ADD COLUMN grants TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE user_templates DROP COLUMN grants;
