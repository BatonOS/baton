


















ALTER TABLE resources ADD COLUMN review         TEXT NOT NULL DEFAULT '';
ALTER TABLE resources ADD COLUMN reviewed_by    TEXT NOT NULL DEFAULT '';
ALTER TABLE resources ADD COLUMN reviewed_at    TEXT NOT NULL DEFAULT '';
ALTER TABLE resources ADD COLUMN review_reason  TEXT NOT NULL DEFAULT '';


CREATE INDEX IF NOT EXISTS idx_resources_review ON resources(tenant_id, review);
