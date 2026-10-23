-- 0002_cert_owner decouples certificate metadata from an existing sites row.
-- Certificates are issued before the site row is committed (so a failed
-- issuance leaves no site behind) and the dashboard certificate has no site at
-- all: its owner is __dashboard__. DeleteSite already deletes the certificate
-- row explicitly, so removing the cascade does not change site deletion.

CREATE TABLE certs_new (
    site_id      TEXT PRIMARY KEY,
    serial       TEXT NOT NULL DEFAULT '',
    domains_json TEXT NOT NULL DEFAULT '[]',
    not_after    INTEGER NOT NULL DEFAULT 0,
    key_path     TEXT NOT NULL DEFAULT '',
    cert_path    TEXT NOT NULL DEFAULT ''
);

INSERT INTO certs_new (site_id, serial, domains_json, not_after, key_path, cert_path)
    SELECT site_id, serial, domains_json, not_after, key_path, cert_path FROM certs;

DROP TABLE certs;

ALTER TABLE certs_new RENAME TO certs;
