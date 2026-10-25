-- 0004_services tracks the state and active status of infrastructure services.
CREATE TABLE IF NOT EXISTS services (
    id              TEXT PRIMARY KEY,
    state           TEXT NOT NULL DEFAULT 'not_installed',
    image           TEXT NOT NULL DEFAULT '',
    active          INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    last_operation  TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL DEFAULT 0
);
