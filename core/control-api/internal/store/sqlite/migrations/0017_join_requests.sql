-- SPDX-License-Identifier: Apache-2.0













CREATE TABLE IF NOT EXISTS join_requests (
  request_id      TEXT PRIMARY KEY,
  tenant_id       TEXT NOT NULL,


  agent           TEXT NOT NULL,




  node_public_key TEXT NOT NULL,
  fingerprint     TEXT NOT NULL,
  from_ip         TEXT NOT NULL DEFAULT '',

  state           TEXT NOT NULL,
  reason          TEXT NOT NULL DEFAULT '',


  token_hash      TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  expires_at      TEXT NOT NULL,
  decided_at      TEXT NOT NULL DEFAULT '',
  decided_by      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS join_requests_agent_state ON join_requests (tenant_id, agent, state);





ALTER TABLE enrollment_tokens ADD COLUMN bound_key_fingerprint TEXT NOT NULL DEFAULT '';
