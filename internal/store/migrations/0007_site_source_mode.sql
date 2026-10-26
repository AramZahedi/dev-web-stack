-- 0007_site_source_mode records where a Windows site's project files live.
-- Linux keeps the existing local mode. Existing rows default to local so an
-- upgrade never reinterprets a path.
ALTER TABLE sites ADD COLUMN source_mode TEXT NOT NULL DEFAULT 'local';
