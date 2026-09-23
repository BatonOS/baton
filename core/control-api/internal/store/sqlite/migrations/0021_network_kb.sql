-- SPDX-License-Identifier: Apache-2.0










CREATE TABLE IF NOT EXISTS network_kb (
    tenant_id    TEXT NOT NULL,
    slug         TEXT NOT NULL,
    title        TEXT NOT NULL,
    tags         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    body         TEXT NOT NULL,
    author       TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (tenant_id, slug),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
