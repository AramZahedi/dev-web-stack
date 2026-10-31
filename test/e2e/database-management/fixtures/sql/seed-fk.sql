-- Foreign keys, a trigger, and a view. MySQL/MariaDB variant.
USE e2e_fk;
CREATE TABLE IF NOT EXISTS parent (
  id INT NOT NULL PRIMARY KEY,
  name VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS child (
  id INT NOT NULL PRIMARY KEY,
  parent_id INT NOT NULL,
  label VARCHAR(64) NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES parent (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO parent (id, name) VALUES (1, 'root-parent')
ON DUPLICATE KEY UPDATE name = VALUES(name);
INSERT INTO child (id, parent_id, label) VALUES (1, 1, 'child-row')
ON DUPLICATE KEY UPDATE label = VALUES(label);
DROP TRIGGER IF EXISTS child_touch;
CREATE TRIGGER child_touch BEFORE UPDATE ON child
FOR EACH ROW SET NEW.updated_at = CURRENT_TIMESTAMP;
CREATE OR REPLACE VIEW child_view AS
  SELECT c.id, c.label, p.name AS parent_name
  FROM child c JOIN parent p ON p.id = c.parent_id;
