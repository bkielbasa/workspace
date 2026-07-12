ALTER TABLE messages
ADD COLUMN IF NOT EXISTS uid BIGSERIAL;

CREATE INDEX IF NOT EXISTS idx_messages_mailbox_uid
ON messages (mailbox_id, uid);
