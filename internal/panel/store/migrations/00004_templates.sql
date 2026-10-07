-- Profile templates written in the web panel (docs/PROFILES.md §1). Built-in templates stay
-- embedded in the binary; these are added next to them (their ids may not collide).

-- +goose Up
CREATE TABLE profile_templates (
    id         TEXT    PRIMARY KEY,
    source     TEXT    NOT NULL, -- YAML in the same format as internal/xrayconf/templates/*.yaml
    rev        INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE profile_templates;
