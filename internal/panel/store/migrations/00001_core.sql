-- Core model: docs/ARCHITECTURE.md §5 + docs/INBOUNDS.md §3.
-- Statistics, sessions and install jobs arrive with their own migrations in later stages.
-- Times are unix seconds.

-- +goose Up
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE audit_log (
    id        INTEGER PRIMARY KEY,
    ts        INTEGER NOT NULL,
    actor     TEXT    NOT NULL,             -- admin|bot|cli|system|api
    actor_id  TEXT    NOT NULL DEFAULT '',
    action    TEXT    NOT NULL,             -- e.g. user.create
    entity    TEXT    NOT NULL,
    entity_id INTEGER,
    diff      TEXT    NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_entity ON audit_log (entity, entity_id);

-- Change feed: the reconciler, the bot and (later) webhooks consume it.
CREATE TABLE events_outbox (
    id           INTEGER PRIMARY KEY,
    ts           INTEGER NOT NULL,
    type         TEXT    NOT NULL,
    entity_id    INTEGER,
    payload      TEXT    NOT NULL DEFAULT '{}',
    delivered_at INTEGER
);

CREATE TABLE base_configs (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    json       TEXT    NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE profiles (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL UNIQUE,
    template_id      TEXT    NOT NULL,
    template_version INTEGER NOT NULL,
    values_json      TEXT    NOT NULL DEFAULT '{}',  -- scope=profile variables
    override_json    TEXT    NOT NULL DEFAULT '{}',  -- merge patch over the template
    tag_pattern      TEXT    NOT NULL,
    remark_pattern   TEXT    NOT NULL,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE nodes (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL,
    code             TEXT    NOT NULL UNIQUE,         -- NODE_CODE, e.g. NL
    country          TEXT    NOT NULL DEFAULT '',
    domain           TEXT    NOT NULL DEFAULT '',
    base_config_id   INTEGER REFERENCES base_configs (id) ON DELETE SET NULL,
    tags             TEXT    NOT NULL DEFAULT '[]',
    local            INTEGER NOT NULL DEFAULT 0,      -- runs inside the panel process
    enabled          INTEGER NOT NULL DEFAULT 1,
    cert_serial      TEXT    NOT NULL DEFAULT '',     -- '' = no valid certificate
    agent_version    TEXT    NOT NULL DEFAULT '',
    xray_version     TEXT    NOT NULL DEFAULT '',
    last_seen_at     INTEGER,
    desired_revision INTEGER NOT NULL DEFAULT 0,
    desired_hash     TEXT    NOT NULL DEFAULT '',
    applied_hash     TEXT    NOT NULL DEFAULT '',
    last_error       TEXT    NOT NULL DEFAULT '',
    sort             INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE node_addresses (
    id           INTEGER PRIMARY KEY,
    node_id      INTEGER NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    ip           TEXT    NOT NULL,
    family       TEXT    NOT NULL,                   -- v4|v6
    interface    TEXT    NOT NULL DEFAULT '',
    on_interface INTEGER NOT NULL DEFAULT 0,
    is_primary   INTEGER NOT NULL DEFAULT 0,
    label        TEXT    NOT NULL DEFAULT '',
    UNIQUE (node_id, ip)
);

CREATE TABLE domains (
    id             INTEGER PRIMARY KEY,
    name           TEXT    NOT NULL UNIQUE,
    address_id     INTEGER REFERENCES node_addresses (id) ON DELETE SET NULL,
    purpose        TEXT    NOT NULL,                 -- panel|sub|inbound|cdn_origin
    dns_ok         INTEGER NOT NULL DEFAULT 0,
    dns_checked_at INTEGER
);

CREATE TABLE node_inbounds (
    id                INTEGER PRIMARY KEY,
    node_id           INTEGER NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    profile_id        INTEGER NOT NULL REFERENCES profiles (id) ON DELETE RESTRICT,
    tag               TEXT    NOT NULL UNIQUE,
    listen_address_id INTEGER REFERENCES node_addresses (id) ON DELETE SET NULL,
    egress_address_id INTEGER REFERENCES node_addresses (id) ON DELETE SET NULL,
    port_override     INTEGER NOT NULL DEFAULT 0,
    values_json       TEXT    NOT NULL DEFAULT '{}', -- scope=node variables (keys, shortId, domains)
    override_json     TEXT    NOT NULL DEFAULT '{}', -- local differences from the profile
    enabled           INTEGER NOT NULL DEFAULT 1,
    sort              INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);
CREATE INDEX node_inbounds_node ON node_inbounds (node_id);

CREATE TABLE node_install_tokens (
    id         INTEGER PRIMARY KEY,
    node_id    INTEGER NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    token_hash TEXT    NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL
);

CREATE TABLE groups (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    sort        INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

-- docs/INBOUNDS.md §1.5
CREATE TABLE group_access (
    id       INTEGER PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL CHECK (kind IN ('node_inbound', 'profile', 'node')),
    ref_id   INTEGER NOT NULL,
    UNIQUE (group_id, kind, ref_id)
);

CREATE TABLE user_templates (
    id                  INTEGER PRIMARY KEY,
    name                TEXT    NOT NULL UNIQUE,
    is_default          INTEGER NOT NULL DEFAULT 0,
    expire_months       INTEGER NOT NULL DEFAULT 0,
    expire_days         INTEGER NOT NULL DEFAULT 0,  -- both 0 = never expires
    traffic_limit_bytes INTEGER,                     -- NULL = unlimited
    reset_strategy      TEXT    NOT NULL DEFAULT 'no' CHECK (reset_strategy IN ('no', 'day', 'week', 'month')),
    hwid_limit          INTEGER,                     -- NULL = global default
    client_type         TEXT    NOT NULL DEFAULT 'auto',
    group_ids           TEXT    NOT NULL DEFAULT '[]',
    note                TEXT    NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL
);

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY,
    username            TEXT    NOT NULL UNIQUE,
    uuid                TEXT    NOT NULL UNIQUE,
    sub_token           TEXT    NOT NULL UNIQUE,
    disabled            INTEGER NOT NULL DEFAULT 0,  -- set by an admin
    status              TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'limited', 'expired')),
    expire_at           INTEGER,
    traffic_limit_bytes INTEGER,
    traffic_used_bytes  INTEGER NOT NULL DEFAULT 0,
    lifetime_used_bytes INTEGER NOT NULL DEFAULT 0,
    reset_strategy      TEXT    NOT NULL DEFAULT 'no' CHECK (reset_strategy IN ('no', 'day', 'week', 'month')),
    last_reset_at       INTEGER,
    hwid_limit          INTEGER,
    client_type         TEXT    NOT NULL DEFAULT 'auto',
    template_id         INTEGER REFERENCES user_templates (id) ON DELETE SET NULL,
    telegram_id         INTEGER,
    external_id         TEXT UNIQUE,                 -- future billing link
    note                TEXT    NOT NULL DEFAULT '',
    online_at           INTEGER,
    created_by          TEXT    NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE INDEX users_status ON users (status);

CREATE TABLE user_groups (
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);
CREATE INDEX user_groups_group ON user_groups (group_id);

-- +goose Down
DROP TABLE user_groups;
DROP TABLE users;
DROP TABLE user_templates;
DROP TABLE group_access;
DROP TABLE groups;
DROP TABLE node_install_tokens;
DROP TABLE node_inbounds;
DROP TABLE domains;
DROP TABLE node_addresses;
DROP TABLE nodes;
DROP TABLE profiles;
DROP TABLE base_configs;
DROP TABLE events_outbox;
DROP TABLE audit_log;
DROP TABLE settings;
