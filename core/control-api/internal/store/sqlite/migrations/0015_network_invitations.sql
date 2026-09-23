-- SPDX-License-Identifier: Apache-2.0



























ALTER TABLE enrollment_tokens ADD COLUMN invitee TEXT NOT NULL DEFAULT '';












ALTER TABLE enrollment_tokens ADD COLUMN from_network TEXT NOT NULL DEFAULT '';
ALTER TABLE enrollment_tokens ADD COLUMN from_network_key TEXT NOT NULL DEFAULT '';








ALTER TABLE enrollment_tokens ADD COLUMN approved_at TEXT NOT NULL DEFAULT '';
ALTER TABLE enrollment_tokens ADD COLUMN approval_sig TEXT NOT NULL DEFAULT '';










CREATE TABLE IF NOT EXISTS released_members (
    tenant_id    TEXT NOT NULL,
    agent_name   TEXT NOT NULL,
    to_network   TEXT NOT NULL,
    released_at  TEXT NOT NULL,

    PRIMARY KEY (tenant_id, agent_name, to_network),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
