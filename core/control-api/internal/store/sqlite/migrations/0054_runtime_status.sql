






















ALTER TABLE nodes RENAME COLUMN runtime_activity TO runtime_status;
UPDATE nodes SET runtime_status = 'unknown';
