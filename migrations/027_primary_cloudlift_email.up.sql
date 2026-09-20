-- 1. Decouple invite_tokens from pre-created disabled users
ALTER TABLE invite_tokens ADD COLUMN IF NOT EXISTS invited_email TEXT;
ALTER TABLE invite_tokens ADD COLUMN IF NOT EXISTS display_name TEXT;

-- 2. Backfill existing pending invite records from users if any exist (FIRST, using their original external emails)
UPDATE invite_tokens i
SET invited_email = u.email,
    display_name = u.display_name
FROM users u
WHERE i.user_id = u.id AND i.invited_email IS NULL;

-- 3. Make user_id nullable for pending invites
ALTER TABLE invite_tokens ALTER COLUMN user_id DROP NOT NULL;

-- 4. Decouple pending unredeemed invites and purge their pre-created disabled placeholder accounts
CREATE TEMP TABLE _pending_invite_users AS
SELECT user_id FROM invite_tokens WHERE used_at IS NULL AND user_id IS NOT NULL;

UPDATE invite_tokens SET user_id = NULL WHERE used_at IS NULL;

DELETE FROM users
WHERE id IN (SELECT user_id FROM _pending_invite_users)
  AND enabled = FALSE;

DROP TABLE IF EXISTS _pending_invite_users;

-- 5. Ensure all remaining users have primary email formatted as <username>@cloudlift.run
UPDATE users
SET email = lower(username) || '@cloudlift.run'
WHERE email NOT LIKE '%@cloudlift.run';
