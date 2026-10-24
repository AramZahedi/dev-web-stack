-- 0001_init creates the initial devstack schema. Tables for PHP configuration
-- are created now so Plan 2 can add PHP support without a migration.

CREATE TABLE IF NOT EXISTS sites (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    domain              TEXT NOT NULL UNIQUE,
    www_alias           INTEGER NOT NULL DEFAULT 0,
    backend             TEXT NOT NULL DEFAULT '',
    host_path           TEXT NOT NULL DEFAULT '',
    backend_path        TEXT NOT NULL DEFAULT '',
    kind                TEXT NOT NULL DEFAULT '',
    php_version         TEXT NOT NULL DEFAULT '',
    php_extensions_json TEXT NOT NULL DEFAULT '{}',
    php_ini_json        TEXT NOT NULL DEFAULT '{}',
    node_version        TEXT NOT NULL DEFAULT '',
    command             TEXT NOT NULL DEFAULT '',
    env_json            TEXT NOT NULL DEFAULT '{}',
    document_root       TEXT NOT NULL DEFAULT '',
    front_controller    INTEGER NOT NULL DEFAULT 0,
    spa_fallback        INTEGER NOT NULL DEFAULT 0,
    tls                 INTEGER NOT NULL DEFAULT 0,
    force_https         INTEGER NOT NULL DEFAULT 0,
    desired_state       TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT '',
    http_port           INTEGER NOT NULL DEFAULT 0,
    fastcgi_port        INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL DEFAULT 0,
    updated_at          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS runtimes (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT '',
    version      TEXT NOT NULL DEFAULT '',
    backend      TEXT NOT NULL DEFAULT '',
    path         TEXT NOT NULL DEFAULT '',
    checksum     TEXT NOT NULL DEFAULT '',
    installed_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS certs (
    site_id      TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    serial       TEXT NOT NULL DEFAULT '',
    domains_json TEXT NOT NULL DEFAULT '[]',
    not_after    INTEGER NOT NULL DEFAULT 0,
    key_path     TEXT NOT NULL DEFAULT '',
    cert_path    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id    TEXT NOT NULL DEFAULT '',
    type       TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS events_created_at_idx ON events(created_at);

-- Plan 2: PHP build defaults and per-site applied configuration.
CREATE TABLE IF NOT EXISTS php_defaults (
    php_version     TEXT PRIMARY KEY,
    ini_json        TEXT NOT NULL DEFAULT '{}',
    extensions_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS site_php_config (
    site_id                 TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    ini_overrides_json      TEXT NOT NULL DEFAULT '{}',
    extension_overrides_json TEXT NOT NULL DEFAULT '{}',
    config_hash             TEXT NOT NULL DEFAULT ''
);
