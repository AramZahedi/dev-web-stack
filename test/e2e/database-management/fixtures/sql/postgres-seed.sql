-- PostgreSQL 18 seed. seed.sh creates the databases first, then runs this
-- file through psql with ON_ERROR_STOP=1. \connect switches databases.
\connect e2e_app
CREATE TABLE IF NOT EXISTS e2e_marker (
  id integer PRIMARY KEY,
  label varchar(128) NOT NULL,
  note text,
  created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO e2e_marker (id, label, note) VALUES
  (1, 'alpha', 'first marker row'),
  (2, 'beta', 'second marker row'),
  (3, 'café ☕ 🚀', 'unicode marker row')
ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label, note = EXCLUDED.note;
CREATE TABLE IF NOT EXISTS e2e_counter (
  name varchar(64) PRIMARY KEY,
  value bigint NOT NULL DEFAULT 0
);
INSERT INTO e2e_counter (name, value) VALUES ('hits', 7)
ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value;

\connect e2e_unicode
CREATE TABLE IF NOT EXISTS messages (
  id integer PRIMARY KEY,
  body varchar(255) NOT NULL,
  emoji varchar(32) NOT NULL
);
INSERT INTO messages (id, body, emoji) VALUES
  (1, 'café naïve résumé', '☕'),
  (2, '日本語のテキスト', '🚀'),
  (3, 'Здравствуйте', '🌍')
ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body, emoji = EXCLUDED.emoji;

\connect e2e_reserved
CREATE TABLE IF NOT EXISTS "select" (
  "order" integer PRIMARY KEY,
  "group" varchar(64) NOT NULL,
  "where" varchar(64)
);
INSERT INTO "select" ("order", "group", "where") VALUES (1, 'reserved', 'quoted')
ON CONFLICT ("order") DO UPDATE SET "group" = EXCLUDED."group", "where" = EXCLUDED."where";

\connect e2e_case
CREATE TABLE IF NOT EXISTS "CaseProbe" (
  "ID" integer PRIMARY KEY,
  "Label" varchar(64) NOT NULL
);
INSERT INTO "CaseProbe" ("ID", "Label") VALUES (1, 'case-sensitive')
ON CONFLICT ("ID") DO UPDATE SET "Label" = EXCLUDED."Label";

\connect e2e_long_63
CREATE TABLE IF NOT EXISTS e2e_long_table (
  id integer PRIMARY KEY,
  value varchar(64) NOT NULL
);
INSERT INTO e2e_long_table (id, value) VALUES (1, 'long-name-ok')
ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value;

\connect e2e_fk
CREATE TABLE IF NOT EXISTS parent (
  id integer PRIMARY KEY,
  name varchar(64) NOT NULL
);
CREATE TABLE IF NOT EXISTS child (
  id integer PRIMARY KEY,
  parent_id integer NOT NULL REFERENCES parent (id) ON DELETE CASCADE,
  label varchar(64) NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO parent (id, name) VALUES (1, 'root-parent')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;
INSERT INTO child (id, parent_id, label) VALUES (1, 1, 'child-row')
ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label;
CREATE OR REPLACE VIEW child_view AS
  SELECT c.id, c.label, p.name AS parent_name
  FROM child c JOIN parent p ON p.id = c.parent_id;
CREATE OR REPLACE FUNCTION e2e_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS child_touch ON child;
CREATE TRIGGER child_touch BEFORE UPDATE ON child
FOR EACH ROW EXECUTE FUNCTION e2e_touch();

\connect e2e_binary
CREATE TABLE IF NOT EXISTS blobs (
  id integer PRIMARY KEY,
  name varchar(64) NOT NULL,
  payload bytea NOT NULL
);
INSERT INTO blobs (id, name, payload) VALUES
  (1, 'bytes-0-255', decode(repeat('000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1ff0f1f2f3f4f5f6f7f8f9fafbfcfdfeff', 1), 'hex'))
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, payload = EXCLUDED.payload;

\connect e2e_routines
CREATE TABLE IF NOT EXISTS routine_input (
  id integer PRIMARY KEY,
  value integer NOT NULL
);
INSERT INTO routine_input (id, value) VALUES (1, 41)
ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value;
CREATE OR REPLACE FUNCTION e2e_fn(x integer) RETURNS integer
LANGUAGE sql IMMUTABLE AS $$ SELECT x + 1 $$;
CREATE OR REPLACE PROCEDURE e2e_proc()
LANGUAGE sql AS $$ SELECT value + 1 FROM routine_input WHERE id = 1 $$;
