
































CREATE TABLE nodes_new (
    node_id             TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    display_name        TEXT NOT NULL,


    roles               TEXT NOT NULL DEFAULT '[]',

    master_state        TEXT NOT NULL DEFAULT ''
                            CHECK (master_state IN ('', 'active', 'standby')),

    deployment          TEXT NOT NULL DEFAULT 'local'
                            CHECK (deployment IN ('local','self_hosted','baton_cloud')),
    status              TEXT NOT NULL CHECK (status IN ('pending','active','offline','revoked')),
    trust               TEXT NOT NULL DEFAULT 'unverified'
                            CHECK (trust IN ('official','self_built','unverified')),
    platform            TEXT NOT NULL DEFAULT '',
    arch                TEXT NOT NULL DEFAULT '',
    agent_version       TEXT NOT NULL DEFAULT '',
    labels_json         TEXT NOT NULL DEFAULT '{}',
    enrolled_at         TEXT NOT NULL,
    last_seen_at        TEXT,
    last_heartbeat_seq  INTEGER NOT NULL DEFAULT 0,
    revoked_at          TEXT,
    revoke_reason       TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    owner                  TEXT NOT NULL DEFAULT '',
    runtime_name           TEXT NOT NULL DEFAULT '',
    runtime_type           TEXT NOT NULL DEFAULT '',
    runtime_image          TEXT NOT NULL DEFAULT '',
    runtime_enterable      INTEGER NOT NULL DEFAULT 0,
    runtime_state          TEXT NOT NULL DEFAULT '',
    runtime_activity       TEXT NOT NULL DEFAULT 'unreported',
    runtime_restart_count  INTEGER NOT NULL DEFAULT 0,
    runtime_started_at     TEXT,
    runtime_last_exit_code INTEGER,
    runtime_oom_killed     INTEGER NOT NULL DEFAULT 0,
    runtime_last_error     TEXT NOT NULL DEFAULT '',
    inflight_calls         INTEGER NOT NULL DEFAULT 0,
    uptime_sec             INTEGER NOT NULL DEFAULT 0,
    UNIQUE (tenant_id, display_name)
);


INSERT INTO nodes_new
SELECT
    node_id, tenant_id, display_name,
    CASE role WHEN 'standby' THEN '["master"]' ELSE '["' || role || '"]' END,
    CASE role WHEN 'master' THEN 'active' WHEN 'standby' THEN 'standby' ELSE '' END,
    'local',
    status, trust, platform, arch, agent_version, labels_json,
    enrolled_at, last_seen_at, last_heartbeat_seq, revoked_at, revoke_reason,
    created_at, updated_at,
    owner, runtime_name, runtime_type, runtime_image, runtime_enterable,
    runtime_state, runtime_activity, runtime_restart_count, runtime_started_at,
    runtime_last_exit_code, runtime_oom_killed, runtime_last_error,
    inflight_calls, uptime_sec
FROM nodes;

DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;

CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
CREATE INDEX IF NOT EXISTS idx_nodes_seen   ON nodes(last_seen_at);



CREATE TABLE tokens_new (
    token_id     TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    token_hash   TEXT NOT NULL UNIQUE,
    roles        TEXT NOT NULL DEFAULT '[]',
    name_pattern TEXT NOT NULL DEFAULT '',
    max_uses     INTEGER NOT NULL CHECK (max_uses > 0),
    used_count   INTEGER NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    expires_at   TEXT NOT NULL,
    revoked_at   TEXT,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    CHECK (used_count <= max_uses)
);

INSERT INTO tokens_new
SELECT
    token_id, tenant_id, token_hash,
    CASE role WHEN 'standby' THEN '["master"]' ELSE '["' || role || '"]' END,
    name_pattern, max_uses, used_count, expires_at, revoked_at, created_by, created_at
FROM enrollment_tokens;

DROP TABLE enrollment_tokens;
ALTER TABLE tokens_new RENAME TO enrollment_tokens;

CREATE INDEX IF NOT EXISTS idx_tokens_expiry ON enrollment_tokens(expires_at);



