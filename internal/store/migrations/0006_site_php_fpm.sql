-- 0006_site_php_fpm persists the per-site php-fpm pool budget so performance
-- applies affect the live master rather than only a generated fragment.
ALTER TABLE sites ADD COLUMN php_fpm_json TEXT NOT NULL DEFAULT '{}';
