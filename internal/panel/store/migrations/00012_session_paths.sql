-- A random URL path per web session (docs/STEALTH.md §1): a login link carries its own path
-- and the session it starts lives under it only.

-- +goose Up
ALTER TABLE login_tokens ADD COLUMN path TEXT NOT NULL DEFAULT '';
ALTER TABLE web_sessions ADD COLUMN path TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE web_sessions DROP COLUMN path;
ALTER TABLE login_tokens DROP COLUMN path;
