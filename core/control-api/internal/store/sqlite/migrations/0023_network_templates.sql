-- SPDX-License-Identifier: Apache-2.0








CREATE TABLE IF NOT EXISTS network_templates (
    tenant_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    yaml       TEXT NOT NULL,
    image      TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (tenant_id, name),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
