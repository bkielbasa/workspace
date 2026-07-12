ALTER TABLE messages
ADD COLUMN IF NOT EXISTS thread_id UUID;

CREATE INDEX IF NOT EXISTS idx_messages_thread
ON messages(thread_id);
