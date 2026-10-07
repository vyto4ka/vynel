-- One-time web login links issued by the Telegram bot (docs/STEALTH.md §3.2). Only hashes.

-- +goose Up
CREATE TABLE login_tokens (
    hash       TEXT    PRIMARY KEY, -- sha256 of the token, hex
    issued_by  TEXT    NOT NULL,    -- e.g. bot:123456789
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

-- +goose Down
DROP TABLE login_tokens;
