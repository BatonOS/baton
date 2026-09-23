-- SPDX-License-Identifier: Apache-2.0







CREATE TABLE IF NOT EXISTS access_grants (
    tenant_id          TEXT NOT NULL,
    grant_id           TEXT NOT NULL,
    subject_network_id TEXT NOT NULL,
    subject_key        TEXT NOT NULL,
    scope              TEXT NOT NULL,
    issued_at          TEXT NOT NULL,
    expiry_at          TEXT NOT NULL,
    revoked_at         TEXT,
    PRIMARY KEY (tenant_id, grant_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
