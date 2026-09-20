DELETE FROM invite_tokens WHERE user_id IS NULL;
ALTER TABLE invite_tokens ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE invite_tokens DROP COLUMN IF EXISTS invited_email;
ALTER TABLE invite_tokens DROP COLUMN IF EXISTS display_name;
