-- 0003_php_build pins each PHP site to an exact build revision and records the
-- generated site configuration. Existing rows keep empty build metadata and
-- the epoch applied_at default until a PHP configuration is applied.

ALTER TABLE sites ADD COLUMN php_build TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN php_ini_raw TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN php_config_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE site_php_config ADD COLUMN applied_at INTEGER NOT NULL DEFAULT 0;
