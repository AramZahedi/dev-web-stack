-- MySQL 8.4 / MariaDB 11.8 core seed.
-- Idempotent: safe to re-run against the disposable harness databases.
CREATE DATABASE IF NOT EXISTS e2e_app CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_empty CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_reserved CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_case CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_long_63 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_unicode CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_fk CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_binary CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_routines CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE e2e_app;
CREATE TABLE IF NOT EXISTS e2e_marker (
  id INT NOT NULL PRIMARY KEY,
  label VARCHAR(128) NOT NULL,
  note TEXT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO e2e_marker (id, label, note) VALUES
  (1, 'alpha', 'first marker row'),
  (2, 'beta', 'second marker row'),
  (3, 'café ☕ 🚀', 'unicode marker row')
ON DUPLICATE KEY UPDATE label = VALUES(label), note = VALUES(note);

CREATE TABLE IF NOT EXISTS e2e_counter (
  name VARCHAR(64) NOT NULL PRIMARY KEY,
  value BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO e2e_counter (name, value) VALUES ('hits', 7)
ON DUPLICATE KEY UPDATE value = VALUES(value);

USE e2e_reserved;
CREATE TABLE IF NOT EXISTS `select` (
  `order` INT NOT NULL PRIMARY KEY,
  `group` VARCHAR(64) NOT NULL,
  `where` VARCHAR(64) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO `select` (`order`, `group`, `where`) VALUES (1, 'reserved', 'quoted')
ON DUPLICATE KEY UPDATE `group` = VALUES(`group`), `where` = VALUES(`where`);

USE e2e_case;
CREATE TABLE IF NOT EXISTS CaseProbe (
  ID INT NOT NULL PRIMARY KEY,
  Label VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO CaseProbe (ID, Label) VALUES (1, 'case-sensitive')
ON DUPLICATE KEY UPDATE Label = VALUES(Label);

USE e2e_long_63;
CREATE TABLE IF NOT EXISTS e2e_long_table (
  id INT NOT NULL PRIMARY KEY,
  value VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO e2e_long_table (id, value) VALUES (1, 'long-name-ok')
ON DUPLICATE KEY UPDATE value = VALUES(value);
