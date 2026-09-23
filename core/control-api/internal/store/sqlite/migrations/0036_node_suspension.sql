
















ALTER TABLE nodes ADD COLUMN suspended_at TEXT NOT NULL DEFAULT '';




ALTER TABLE nodes ADD COLUMN suspend_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN suspended_by TEXT NOT NULL DEFAULT '';
