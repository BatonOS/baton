



































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
    runtime_name           TEXT NOT NULL DEFAULT '',
    runtime_type           TEXT NOT NULL DEFAULT '',
    runtime_image          TEXT NOT NULL DEFAULT '',
    runtime_enterable      INTEGER NOT NULL DEFAULT 0,
    runtime_state          TEXT NOT NULL DEFAULT '',
    runtime_restart_count  INTEGER NOT NULL DEFAULT 0,
    runtime_started_at     TEXT,
    runtime_last_exit_code INTEGER,
    runtime_oom_killed     INTEGER NOT NULL DEFAULT 0,
    runtime_last_error     TEXT NOT NULL DEFAULT '',
    inflight_calls         INTEGER NOT NULL DEFAULT 0,
    uptime_sec             INTEGER NOT NULL DEFAULT 0, runtime_probe TEXT NOT NULL DEFAULT '', runtime_memory TEXT NOT NULL DEFAULT '', runtime_template TEXT NOT NULL DEFAULT '', host_address TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', suspended_at TEXT NOT NULL DEFAULT '', suspend_reason TEXT NOT NULL DEFAULT '', suspended_by TEXT NOT NULL DEFAULT '', runtime_skills_mount_path TEXT NOT NULL DEFAULT '', remote_shell INTEGER, runtime_status TEXT NOT NULL DEFAULT 'unknown',























    owner_identity_id   TEXT REFERENCES identities(identity_id) ON DELETE RESTRICT,
    UNIQUE (tenant_id, display_name),










    CHECK (status = 'revoked' OR owner_identity_id IS NOT NULL)
);

INSERT INTO nodes_new (node_id,tenant_id,display_name,roles,master_state,deployment,status,trust,platform,arch,agent_version,labels_json,enrolled_at,last_seen_at,last_heartbeat_seq,revoked_at,revoke_reason,created_at,updated_at,runtime_name,runtime_type,runtime_image,runtime_enterable,runtime_state,runtime_restart_count,runtime_started_at,runtime_last_exit_code,runtime_oom_killed,runtime_last_error,inflight_calls,uptime_sec,runtime_probe,runtime_memory,runtime_template,host_address,label,suspended_at,suspend_reason,suspended_by,runtime_skills_mount_path,remote_shell,runtime_status, owner_identity_id)
SELECT n.node_id,
       n.tenant_id,
       n.display_name,
       n.roles,
       n.master_state,
       n.deployment,
       n.status,
       n.trust,
       n.platform,
       n.arch,
       n.agent_version,
       n.labels_json,
       n.enrolled_at,
       n.last_seen_at,
       n.last_heartbeat_seq,
       n.revoked_at,
       n.revoke_reason,
       n.created_at,
       n.updated_at,
       n.runtime_name,
       n.runtime_type,
       n.runtime_image,
       n.runtime_enterable,
       n.runtime_state,
       n.runtime_restart_count,
       n.runtime_started_at,
       n.runtime_last_exit_code,
       n.runtime_oom_killed,
       n.runtime_last_error,
       n.inflight_calls,
       n.uptime_sec,
       n.runtime_probe,
       n.runtime_memory,
       n.runtime_template,
       n.host_address,
       n.label,
       n.suspended_at,
       n.suspend_reason,
       n.suspended_by,
       n.runtime_skills_mount_path,
       n.remote_shell,
       n.runtime_status,




       COALESCE(




         (SELECT i.identity_id FROM identities i
           WHERE i.node_id = n.node_id AND i.name = n.display_name),
         (SELECT i.identity_id FROM identities i
           WHERE i.node_id = n.node_id ORDER BY i.created_at, i.identity_id LIMIT 1))
  FROM nodes n;

DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;

CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
CREATE INDEX IF NOT EXISTS idx_nodes_seen   ON nodes(last_seen_at);






















CREATE TRIGGER IF NOT EXISTS identities_active_must_be_owned_insert
BEFORE INSERT ON identities
FOR EACH ROW WHEN NEW.node_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM nodes n WHERE n.node_id = NEW.node_id AND n.owner_identity_id = NEW.identity_id)
BEGIN
    SELECT RAISE(ABORT, 'an identity may only be active on a node it owns');
END;

CREATE TRIGGER IF NOT EXISTS identities_active_must_be_owned_update
BEFORE UPDATE OF node_id ON identities
FOR EACH ROW WHEN NEW.node_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM nodes n WHERE n.node_id = NEW.node_id AND n.owner_identity_id = NEW.identity_id)
BEGIN
    SELECT RAISE(ABORT, 'an identity may only be active on a node it owns');
END;
