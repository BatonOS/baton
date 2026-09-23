-- SPDX-License-Identifier: Apache-2.0










CREATE TABLE IF NOT EXISTS resources (
    resource_id          TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL,
    network_id           TEXT NOT NULL DEFAULT '',
    type                 TEXT NOT NULL,
    name                 TEXT NOT NULL,
    version              TEXT NOT NULL DEFAULT '',
    folder               TEXT NOT NULL DEFAULT '',
    visibility           TEXT NOT NULL DEFAULT 'network',
    publisher            TEXT NOT NULL DEFAULT '',
    hash                 TEXT NOT NULL DEFAULT '',
    source               TEXT NOT NULL DEFAULT '',
    signature            TEXT NOT NULL DEFAULT '',
    provenance           TEXT NOT NULL DEFAULT '',
    license              TEXT NOT NULL DEFAULT '',
    compatibility        TEXT NOT NULL DEFAULT '',
    permissions_required TEXT NOT NULL DEFAULT '',
    dependencies         TEXT NOT NULL DEFAULT '',
    detail               TEXT NOT NULL DEFAULT '{}',
    body                 TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    UNIQUE (tenant_id, type, name),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX IF NOT EXISTS idx_resources_type ON resources (tenant_id, type);


INSERT INTO resources (resource_id, tenant_id, type, name, folder, visibility, publisher, detail, body, created_at, updated_at)
    SELECT lower(hex(randomblob(16))), tenant_id, 'knowledge', slug, 'Knowledge/', 'network', author,
           json_object('content_type', content_type, 'tags', tags, 'size_bytes', length(body)),
           body, updated_at, updated_at
      FROM network_kb;


INSERT INTO resources (resource_id, tenant_id, type, name, folder, visibility, source, detail, body, created_at, updated_at)
    SELECT lower(hex(randomblob(16))), tenant_id, 'template', name, 'Templates/', 'network', image,
           json_object('image', image),
           yaml, updated_at, updated_at
      FROM network_templates;




INSERT INTO resources (resource_id, tenant_id, type, name, version, folder, visibility, hash, source, detail, created_at, updated_at)
    SELECT lower(hex(randomblob(16))), tenant_id, 'skill', name, version, 'Skills/', 'network', sha256, source_url,
           json_object('sha256', sha256, 'size_bytes', size_bytes, 'skill_id', skill_id, 'cached', json('true')),
           created_at, created_at
      FROM skills WHERE network_scope = 1;

DROP TABLE network_kb;
DROP TABLE network_templates;
ALTER TABLE skills DROP COLUMN network_scope;
