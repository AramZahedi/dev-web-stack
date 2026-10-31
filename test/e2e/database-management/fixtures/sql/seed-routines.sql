-- Stored procedure and function. MySQL/MariaDB variant.
SET GLOBAL log_bin_trust_function_creators = 1;
USE e2e_routines;
CREATE TABLE IF NOT EXISTS routine_input (
  id INT NOT NULL PRIMARY KEY,
  value INT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO routine_input (id, value) VALUES (1, 41)
ON DUPLICATE KEY UPDATE value = VALUES(value);
DROP PROCEDURE IF EXISTS e2e_proc;
CREATE PROCEDURE e2e_proc()
  SELECT value + 1 AS answer FROM routine_input WHERE id = 1;
DROP FUNCTION IF EXISTS e2e_fn;
CREATE FUNCTION e2e_fn(x INT) RETURNS INT DETERMINISTIC
  RETURN x + 1;
