-- +goose Up
ALTER TABLE nodes ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN xray_release TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE nodes DROP COLUMN xray_release;
ALTER TABLE nodes DROP COLUMN note;
