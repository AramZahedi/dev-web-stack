-- Database management: service-wide users, site links, grants, backups, jobs,
-- and durable audit events. Passwords live in the privileged platform's
-- secret tree, never in this database.

CREATE TABLE IF NOT EXISTS database_users (
    id                       TEXT PRIMARY KEY,
    service_id               TEXT NOT NULL,
    engine                   TEXT NOT NULL,
    username                 TEXT NOT NULL,
    host                     TEXT NOT NULL DEFAULT '',
    managed_by_devstack      INTEGER NOT NULL DEFAULT 0,
    password_managed         INTEGER NOT NULL DEFAULT 0,
    enabled                  INTEGER NOT NULL DEFAULT 1,
    access_level             TEXT NOT NULL DEFAULT '',
    created_at               INTEGER NOT NULL DEFAULT 0,
    last_password_rotated_at INTEGER NOT NULL DEFAULT 0,
    last_seen_at             INTEGER NOT NULL DEFAULT 0,
    UNIQUE(service_id, username, host)
);

CREATE INDEX IF NOT EXISTS database_users_service_idx
    ON database_users(service_id);

CREATE TABLE IF NOT EXISTS database_links (
    id            TEXT PRIMARY KEY,
    site_id       TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    service_id    TEXT NOT NULL,
    database_name TEXT NOT NULL,
    user_id       TEXT NOT NULL REFERENCES database_users(id) ON DELETE RESTRICT,
    is_primary    INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL DEFAULT 0,
    UNIQUE(site_id, service_id, database_name)
);

CREATE UNIQUE INDEX IF NOT EXISTS database_links_primary_idx
    ON database_links(site_id) WHERE is_primary = 1;
CREATE INDEX IF NOT EXISTS database_links_service_idx
    ON database_links(service_id);
CREATE INDEX IF NOT EXISTS database_links_user_idx
    ON database_links(user_id);

CREATE TABLE IF NOT EXISTS database_user_grants (
    user_id       TEXT NOT NULL REFERENCES database_users(id) ON DELETE CASCADE,
    database_name TEXT NOT NULL,
    preset        TEXT NOT NULL,
    custom_json   TEXT NOT NULL DEFAULT '[]',
    updated_at    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(user_id, database_name)
);

CREATE TABLE IF NOT EXISTS database_backups (
    id            TEXT PRIMARY KEY,
    service_id    TEXT NOT NULL,
    engine        TEXT NOT NULL,
    database_name TEXT NOT NULL,
    path          TEXT NOT NULL,
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    sha256        TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    status        TEXT NOT NULL,
    created_at    INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS database_backups_service_idx
    ON database_backups(service_id, database_name);

CREATE TABLE IF NOT EXISTS database_jobs (
    id            TEXT PRIMARY KEY,
    service_id    TEXT NOT NULL,
    database_name TEXT NOT NULL,
    kind          TEXT NOT NULL,
    status        TEXT NOT NULL,
    progress      INTEGER NOT NULL DEFAULT 0,
    message       TEXT NOT NULL DEFAULT '',
    backup_id     TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS database_jobs_created_at_idx
    ON database_jobs(created_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    actor       TEXT NOT NULL DEFAULT '',
    service_id  TEXT NOT NULL DEFAULT '',
    site_id     TEXT NOT NULL DEFAULT '',
    object_type TEXT NOT NULL DEFAULT '',
    object_name TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL DEFAULT '',
    result      TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '{}',
    created_at  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS audit_events_created_at_idx
    ON audit_events(created_at);
