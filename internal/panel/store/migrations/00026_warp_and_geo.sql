-- Per-node Cloudflare WARP settings and panel-managed GeoIP/GeoSite (.dat) files.

-- +goose Up
CREATE TABLE node_warp (
    node_id          INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    enabled          INTEGER NOT NULL DEFAULT 0,
    rule_mode        TEXT    NOT NULL DEFAULT 'rules', -- 'rules' | 'all'
    cred_mode        TEXT    NOT NULL DEFAULT 'auto',  -- 'auto' | 'manual'
    device_id        TEXT    NOT NULL DEFAULT '',
    access_token     TEXT    NOT NULL DEFAULT '',
    license_key      TEXT    NOT NULL DEFAULT '',
    account_type     TEXT    NOT NULL DEFAULT '',      -- 'free' | 'limited' | 'unlimited' | 'custom'
    secret_key       TEXT    NOT NULL DEFAULT '',
    public_key       TEXT    NOT NULL DEFAULT '',
    peer_public_key  TEXT    NOT NULL DEFAULT '',
    preshared_key    TEXT    NOT NULL DEFAULT '',
    addr_v4          TEXT    NOT NULL DEFAULT '',
    addr_v6          TEXT    NOT NULL DEFAULT '',
    reserved_json    TEXT    NOT NULL DEFAULT '[]',
    endpoint         TEXT    NOT NULL DEFAULT 'engage.cloudflareclient.com:2408',
    mtu              INTEGER NOT NULL DEFAULT 1280,
    keepalive        INTEGER NOT NULL DEFAULT 25,
    domain_strategy  TEXT    NOT NULL DEFAULT 'ForceIPv4v6',
    rules_json       TEXT    NOT NULL DEFAULT '[]',
    last_check_at    INTEGER NOT NULL DEFAULT 0,
    last_check_ok    INTEGER NOT NULL DEFAULT 0,
    last_check_ip    TEXT    NOT NULL DEFAULT '',
    last_check_colo  TEXT    NOT NULL DEFAULT '',
    last_check_ms    INTEGER NOT NULL DEFAULT 0,
    last_check_err   TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE geo_files (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL UNIQUE,
    title              TEXT    NOT NULL DEFAULT '',
    kind               TEXT    NOT NULL CHECK (kind IN ('geosite', 'geoip')),
    is_system_override INTEGER NOT NULL DEFAULT 0,
    source_url         TEXT    NOT NULL DEFAULT '',
    auto_update        INTEGER NOT NULL DEFAULT 1,
    sha256             TEXT    NOT NULL DEFAULT '',
    size_bytes         INTEGER NOT NULL DEFAULT 0,
    categories_count   INTEGER NOT NULL DEFAULT 0,
    entries_count      INTEGER NOT NULL DEFAULT 0,
    fetched_at         INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT    NOT NULL DEFAULT '',
    enabled            INTEGER NOT NULL DEFAULT 1,
    -- Reserved for future client geo asset distribution.
    client_expose      INTEGER NOT NULL DEFAULT 0,
    client_alias       TEXT    NOT NULL DEFAULT '',
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
);

-- +goose Down
DROP TABLE geo_files;
DROP TABLE node_warp;
