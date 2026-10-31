-- Portable-enough import fixture for MySQL/MariaDB. Used to build the valid
-- gzip dump and to exercise the import path.
CREATE TABLE IF NOT EXISTS e2e_import_marker (
  id INT NOT NULL PRIMARY KEY,
  label VARCHAR(128) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO e2e_import_marker (id, label) VALUES
  (1, 'import-alpha'),
  (2, 'import-beta')
ON DUPLICATE KEY UPDATE label = VALUES(label);
