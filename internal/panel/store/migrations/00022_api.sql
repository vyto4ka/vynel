-- Public API: tokens for other programs (billing, bots, other panels), the events they can read
-- or get as webhooks, and the replies kept for idempotent requests.

-- +goose Up
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL,             -- the first characters of the token, to tell tokens apart
    hash         TEXT    NOT NULL UNIQUE,      -- sha256 of the token; the token itself is shown once
    scopes       TEXT    NOT NULL,             -- comma-separated, e.g. users:read,users:write
    cidrs        TEXT    NOT NULL DEFAULT '',  -- comma-separated networks the token may come from; empty = anywhere
    rate_limit   INTEGER NOT NULL DEFAULT 0,   -- requests per minute; 0 = the default
    expires_at   INTEGER,
    created_by   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    last_used_ip TEXT    NOT NULL DEFAULT '',
    uses         INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER
);

-- Everything that happened, in the order it happened: the feed behind GET /events and webhooks.
CREATE TABLE api_events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    ts        INTEGER NOT NULL,
    type      TEXT    NOT NULL,
    entity    TEXT    NOT NULL DEFAULT '',
    entity_id INTEGER NOT NULL DEFAULT 0,
    actor     TEXT    NOT NULL DEFAULT '',     -- kind:id of who did it
    data      TEXT    NOT NULL DEFAULT '{}'
);
CREATE INDEX api_events_ts ON api_events (ts);

CREATE TABLE webhooks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    url         TEXT    NOT NULL,
    secret      TEXT    NOT NULL,              -- signs every delivery
    events      TEXT    NOT NULL DEFAULT '*',  -- comma-separated types or prefixes ("user.*"); * = all
    enabled     INTEGER NOT NULL DEFAULT 1,
    allow_private INTEGER NOT NULL DEFAULT 0,  -- may deliver to local and private addresses (a bot on the same server)
    fails       INTEGER NOT NULL DEFAULT 0,    -- deliveries that gave up in a row; too many turn the webhook off
    off_reason  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    last_at     INTEGER,
    last_status INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE webhook_deliveries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id INTEGER NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event_id   INTEGER NOT NULL,
    type       TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    state      TEXT    NOT NULL DEFAULT 'pending',  -- pending|ok|failed
    attempts   INTEGER NOT NULL DEFAULT 0,
    next_at    INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    status     INTEGER NOT NULL DEFAULT 0,          -- HTTP status of the last attempt
    error      TEXT    NOT NULL DEFAULT '',
    took_ms    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX webhook_deliveries_due ON webhook_deliveries (state, next_at);
CREATE INDEX webhook_deliveries_hook ON webhook_deliveries (webhook_id, id);

-- A repeated request with the same Idempotency-Key gets the first answer, not a second action.
CREATE TABLE api_idempotency (
    token_id   INTEGER NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    key        TEXT    NOT NULL,
    fingerprint TEXT   NOT NULL,                    -- method, path and body: the same key must mean the same request
    status     INTEGER NOT NULL,
    response   TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (token_id, key)
);

-- A Telegram account is one user: billing and bots look users up by it.
CREATE UNIQUE INDEX users_telegram_id ON users (telegram_id) WHERE telegram_id IS NOT NULL;

-- +goose Down
DROP INDEX users_telegram_id;
DROP TABLE api_idempotency;
DROP TABLE webhook_deliveries;
DROP TABLE webhooks;
DROP TABLE api_events;
DROP TABLE api_tokens;
