-- 0005_site_contracts adds front-controller configuration, Apache override
-- settings, SPA fallback, and optional build profile to sites.
ALTER TABLE sites ADD COLUMN front_controller_file TEXT NOT NULL DEFAULT 'index.php';
ALTER TABLE sites ADD COLUMN front_controller_mode TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE sites ADD COLUMN apache_allow_override TEXT NOT NULL DEFAULT 'all';
ALTER TABLE sites ADD COLUMN spa_fallback_file TEXT NOT NULL DEFAULT 'index.html';
ALTER TABLE sites ADD COLUMN build_json TEXT NOT NULL DEFAULT '{}';
