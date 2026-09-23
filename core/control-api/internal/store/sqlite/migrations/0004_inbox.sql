-- SPDX-License-Identifier: Apache-2.0













CREATE TABLE IF NOT EXISTS identities (
    identity_id TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,


    name        TEXT NOT NULL,







    node_id     TEXT REFERENCES nodes(node_id) ON DELETE SET NULL,
    created_at  TEXT NOT NULL,
    bound_at    TEXT NOT NULL DEFAULT '',
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_identities_node ON identities(node_id);











CREATE TABLE IF NOT EXISTS messages (
    message_id   TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,




    sender       TEXT NOT NULL,
    recipient    TEXT NOT NULL,
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



CREATE INDEX IF NOT EXISTS idx_messages_recipient_state
    ON messages(tenant_id, recipient, state, created_at);



CREATE INDEX IF NOT EXISTS idx_messages_expiry ON messages(expires_at);
