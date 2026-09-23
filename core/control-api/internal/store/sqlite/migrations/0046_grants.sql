-- SPDX-License-Identifier: Apache-2.0
















CREATE TABLE IF NOT EXISTS grants (
    tenant_id    TEXT NOT NULL,
    grant_id     TEXT NOT NULL,
    grantor      TEXT NOT NULL,
    grantee      TEXT NOT NULL,
    action       TEXT NOT NULL,


    object       TEXT NOT NULL DEFAULT '',
    scope        TEXT NOT NULL,
    effect       TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
    constraints  TEXT NOT NULL DEFAULT '',
    valid_from   TEXT NOT NULL,

    valid_until  TEXT NOT NULL DEFAULT '',



    proof        BLOB NOT NULL,
    created_at   TEXT NOT NULL,

    PRIMARY KEY (tenant_id, grant_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX IF NOT EXISTS idx_grants_grantee ON grants (tenant_id, grantee);
CREATE INDEX IF NOT EXISTS idx_grants_action  ON grants (tenant_id, action);






CREATE TABLE IF NOT EXISTS grant_revocations (
    tenant_id  TEXT NOT NULL,
    grant_id   TEXT NOT NULL,
    revoked_by TEXT NOT NULL,
    revoked_at TEXT NOT NULL,

    PRIMARY KEY (tenant_id, grant_id),
    FOREIGN KEY (tenant_id, grant_id) REFERENCES grants(tenant_id, grant_id)
);
