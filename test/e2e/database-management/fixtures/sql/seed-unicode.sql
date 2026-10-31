-- Unicode and emoji data. MySQL/MariaDB variant.
USE e2e_unicode;
CREATE TABLE IF NOT EXISTS messages (
  id INT NOT NULL PRIMARY KEY,
  body VARCHAR(255) NOT NULL,
  emoji VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO messages (id, body, emoji) VALUES
  (1, 'café naïve résumé', '☕'),
  (2, '日本語のテキスト', '🚀'),
  (3, 'Здравствуйте', '🌍')
ON DUPLICATE KEY UPDATE body = VALUES(body), emoji = VALUES(emoji);
