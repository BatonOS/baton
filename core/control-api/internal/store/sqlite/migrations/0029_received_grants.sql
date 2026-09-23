-- SPDX-License-Identifier: Apache-2.0










CREATE TABLE IF NOT EXISTS received_grants (
    tenant_id          TEXT NOT NULL,
    grant_id           TEXT NOT NULL,
    issuer_network_id  TEXT NOT NULL,
    issuer_key         TEXT NOT NULL,
    subject_network_id TEXT NOT NULL,
    scope              TEXT NOT NULL,
    issued_at          TEXT NOT NULL,
    expiry_at          TEXT NOT NULL,
    endpoint           TEXT NOT NULL DEFAULT '',
    ca_pem             TEXT NOT NULL DEFAULT '',
    ca_fingerprint     TEXT NOT NULL DEFAULT '',
    payload            TEXT NOT NULL,
    signature          TEXT NOT NULL,
    imported_at        TEXT NOT NULL,
    PRIMARY KEY (tenant_id, grant_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
