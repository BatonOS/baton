















CREATE TABLE IF NOT EXISTS skill_codes (
    node_id        TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
    skill_id       TEXT NOT NULL,
    code_id        TEXT NOT NULL DEFAULT '',
    issued_at      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (node_id, skill_id)
);

CREATE INDEX IF NOT EXISTS idx_skill_codes_skill ON skill_codes(skill_id);
