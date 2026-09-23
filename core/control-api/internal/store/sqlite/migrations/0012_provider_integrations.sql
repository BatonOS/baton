-- SPDX-License-Identifier: Apache-2.0
















CREATE TABLE IF NOT EXISTS provider_integrations (
    integration_id  TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,



    provider        TEXT NOT NULL,





    public_key_pem  TEXT NOT NULL,


    fingerprint     TEXT NOT NULL,





    scopes          TEXT NOT NULL,





    target_network  TEXT NOT NULL,





    enabled         INTEGER NOT NULL DEFAULT 0,

    created_at      TEXT NOT NULL,
    confirmed_at    TEXT NOT NULL DEFAULT '',






    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);




CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_fingerprint
    ON provider_integrations (tenant_id, fingerprint);
