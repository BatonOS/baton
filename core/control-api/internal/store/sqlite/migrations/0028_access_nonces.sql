-- SPDX-License-Identifier: Apache-2.0







CREATE TABLE IF NOT EXISTS access_nonces (
    tenant_id TEXT NOT NULL,
    purpose   TEXT NOT NULL,
    subject   TEXT NOT NULL,
    nonce     TEXT NOT NULL,
    seen_at   TEXT NOT NULL,
    PRIMARY KEY (tenant_id, purpose, subject, nonce),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
