













ALTER TABLE networks ADD COLUMN admission TEXT NOT NULL DEFAULT 'open';




ALTER TABLE networks ADD COLUMN admission_changed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE networks ADD COLUMN admission_changed_by TEXT NOT NULL DEFAULT '';
