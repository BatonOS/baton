





















ALTER TABLE enrollment_tokens ADD COLUMN purpose TEXT NOT NULL DEFAULT '';

UPDATE enrollment_tokens SET purpose = 'node' WHERE purpose = '';

CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_purpose ON enrollment_tokens(purpose);
