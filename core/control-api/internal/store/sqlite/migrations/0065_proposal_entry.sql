








CREATE TABLE IF NOT EXISTS interactions (
  interaction_id TEXT PRIMARY KEY,
  tenant_id      TEXT NOT NULL DEFAULT 'default',
  actor          TEXT NOT NULL,
  created_at     TEXT NOT NULL
);

ALTER TABLE transactions ADD COLUMN interaction_id TEXT REFERENCES interactions(interaction_id);
ALTER TABLE transactions ADD COLUMN idempotency_key TEXT;
ALTER TABLE transactions ADD COLUMN target_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE transactions ADD COLUMN target_id TEXT NOT NULL DEFAULT '';



ALTER TABLE transactions ADD COLUMN error_json TEXT;








CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_actor_key
  ON transactions(actor, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_transactions_interaction
  ON transactions(interaction_id, action_name, target_kind, target_id);
