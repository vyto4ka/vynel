-- A profile's own client config for subscriptions (docs/USER_GUIDE.md §5.1): its own routing or
-- a whole Xray client config. Empty = the subscription routing.

-- +goose Up
ALTER TABLE profiles ADD COLUMN client_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE profiles DROP COLUMN client_json;
