






















CREATE TABLE IF NOT EXISTS transactions (
  transaction_id        TEXT PRIMARY KEY,
  tenant_id             TEXT NOT NULL DEFAULT 'default',
  proposal_json         TEXT NOT NULL,
  proposal_digest       TEXT NOT NULL,
  actor                 TEXT NOT NULL,
  via_json              TEXT NOT NULL DEFAULT '{}',
  action_name           TEXT NOT NULL,
  action_version        TEXT NOT NULL DEFAULT '',
  state                 TEXT NOT NULL
                          CHECK (state IN ('proposed','pending_approval','approved','executing',
                                           'completed','failed','rejected','expired','modified','withdrawn')),
  plan_digest           TEXT,
  plan_detail_json      TEXT,
  approval_required     INTEGER NOT NULL DEFAULT 0,
  approval_basis        TEXT NOT NULL DEFAULT ''
                          CHECK (approval_basis IN ('','policy','floor','undeclared-consequences')),
  approval_rule_ref     TEXT NOT NULL DEFAULT '',
  approval_request_id   TEXT NOT NULL DEFAULT '',
  approval_expires_at   TEXT,
  approval_decision_json TEXT,
  outcome_ref           TEXT NOT NULL DEFAULT '',
  receipt_ref           TEXT NOT NULL DEFAULT '',
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_transactions_state ON transactions(state);



CREATE TABLE IF NOT EXISTS transaction_history (
  transaction_id TEXT NOT NULL REFERENCES transactions(transaction_id),
  seq            INTEGER NOT NULL,
  state          TEXT NOT NULL
                   CHECK (state IN ('proposed','pending_approval','approved','executing',
                                    'completed','failed','rejected','expired','modified','withdrawn')),
  at             TEXT NOT NULL,
  by             TEXT NOT NULL CHECK (by IN ('core','authority','actor')),
  PRIMARY KEY (transaction_id, seq)
);




CREATE TABLE IF NOT EXISTS transaction_steps (
  transaction_id TEXT NOT NULL REFERENCES transactions(transaction_id),
  idx            INTEGER NOT NULL,
  name           TEXT NOT NULL,
  executor       TEXT NOT NULL,
  summary        TEXT NOT NULL DEFAULT '',
  operation_id   TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL
                   CHECK (status IN ('not_started','running','succeeded','failed','unknown','skipped')),
  started_at     TEXT,
  ended_at       TEXT,
  PRIMARY KEY (transaction_id, idx)
);
