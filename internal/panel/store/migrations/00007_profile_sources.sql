-- A profile's own copy of the template source (docs/PROFILES.md §1.4): the inbound JSON and the
-- connection point, both with ${VARIABLES}. Empty = the template's.

-- +goose Up
ALTER TABLE profiles ADD COLUMN inbound_json TEXT NOT NULL DEFAULT '';
ALTER TABLE profiles ADD COLUMN host_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE profiles DROP COLUMN inbound_json;
ALTER TABLE profiles DROP COLUMN host_json;
