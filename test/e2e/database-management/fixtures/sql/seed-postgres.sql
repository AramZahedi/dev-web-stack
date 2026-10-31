-- Import fixture for PostgreSQL. Used to build the valid gzip dump and to
-- exercise the import path.
CREATE TABLE IF NOT EXISTS e2e_import_marker (
  id integer PRIMARY KEY,
  label varchar(128) NOT NULL
);
INSERT INTO e2e_import_marker (id, label) VALUES
  (1, 'import-alpha'),
  (2, 'import-beta')
ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label;
