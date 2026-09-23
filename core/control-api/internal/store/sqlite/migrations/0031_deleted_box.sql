


















ALTER TABLE messages ADD COLUMN deleted_at TEXT NOT NULL DEFAULT '';




CREATE INDEX IF NOT EXISTS idx_messages_box
    ON messages (tenant_id, deleted_at, state, created_at);
