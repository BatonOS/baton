-- SPDX-License-Identifier: Apache-2.0






























CREATE TABLE messages_new (
    message_id   TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,



    source_agent        TEXT NOT NULL,
    source_network      TEXT NOT NULL,
    destination_agent   TEXT NOT NULL,
    destination_network TEXT NOT NULL,

    created_at   TEXT NOT NULL,
    state        TEXT NOT NULL,
    delivered_at TEXT NOT NULL DEFAULT '',
    acked_at     TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT 'text/plain',
    payload_size INTEGER NOT NULL DEFAULT 0,
    payload      BLOB
);








INSERT INTO messages_new
  (message_id, tenant_id, source_agent, source_network, destination_agent,
   destination_network, created_at, state, delivered_at, acked_at, expires_at,
   attempts, content_type, payload_size, payload)
SELECT
  m.message_id, m.tenant_id,
  m.sender,
  COALESCE((SELECT n.network_id FROM networks n WHERE n.tenant_id = m.tenant_id), ''),
  m.recipient,
  COALESCE((SELECT n.network_id FROM networks n WHERE n.tenant_id = m.tenant_id), ''),
  m.created_at, m.state, m.delivered_at, m.acked_at, m.expires_at,
  m.attempts, m.content_type, m.payload_size, m.payload
FROM messages m;

DROP TABLE messages;
ALTER TABLE messages_new RENAME TO messages;





CREATE INDEX IF NOT EXISTS idx_messages_destination
    ON messages (destination_network, destination_agent, state, created_at);
