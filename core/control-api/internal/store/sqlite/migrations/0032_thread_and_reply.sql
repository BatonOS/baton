

















ALTER TABLE messages ADD COLUMN thread_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN reply_to  TEXT NOT NULL DEFAULT '';

UPDATE messages SET thread_id = message_id WHERE thread_id = '';



CREATE INDEX IF NOT EXISTS idx_messages_thread
    ON messages (tenant_id, thread_id, created_at);
