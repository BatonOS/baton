

















UPDATE messages SET state = 'unread';

ALTER TABLE messages DROP COLUMN acked_at;
