-- SPDX-License-Identifier: Apache-2.0









CREATE TABLE IF NOT EXISTS skills (
    skill_id    TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,

    name        TEXT NOT NULL,
    version     TEXT NOT NULL,








    sha256      TEXT NOT NULL,







    source_url  TEXT NOT NULL,
    size_bytes  INTEGER NOT NULL DEFAULT 0,

    created_at  TEXT NOT NULL,





    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);




CREATE UNIQUE INDEX IF NOT EXISTS idx_skills_digest
    ON skills (tenant_id, sha256);

CREATE TABLE IF NOT EXISTS skill_installs (
    install_id  TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    skill_id    TEXT NOT NULL,




    target_kind TEXT NOT NULL,



    target      TEXT NOT NULL,

    created_at  TEXT NOT NULL,

    FOREIGN KEY (tenant_id) REFERENCES tenants(id),
    FOREIGN KEY (skill_id) REFERENCES skills(skill_id) ON DELETE CASCADE
);




CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_install_target
    ON skill_installs (tenant_id, skill_id, target_kind, target);

CREATE INDEX IF NOT EXISTS idx_skill_install_lookup
    ON skill_installs (tenant_id, target_kind, target);
