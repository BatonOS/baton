


















































CREATE TABLE IF NOT EXISTS capability_revocations (
    capability_id TEXT PRIMARY KEY REFERENCES capabilities(id) ON DELETE CASCADE,










    decided_at    TEXT NOT NULL,
    decided_by    TEXT NOT NULL DEFAULT ''
);





INSERT OR IGNORE INTO capability_revocations (capability_id, decided_at, decided_by)
SELECT id, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), ''
FROM capabilities
WHERE disabled = 1;








ALTER TABLE capabilities DROP COLUMN disabled;
