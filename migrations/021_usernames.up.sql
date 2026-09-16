-- Login usernames as an alternative to email addresses. Usernames are
-- login-only aliases: mail, DAV and share paths keep using the email.
ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT;

UPDATE users SET username = lower(split_part(email, '@', 1)) WHERE username IS NULL;

UPDATE users AS u SET username = u.username || n.n::text
FROM (SELECT id, row_number() OVER (PARTITION BY username ORDER BY created_at) AS n FROM users) AS n
WHERE u.id = n.id AND n.n > 1;

ALTER TABLE users ALTER COLUMN username SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS users_username_idx ON users(username);
