-- SPDX-License-Identifier: Apache-2.0


















PRAGMA foreign_keys = ON;


CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);




CREATE TABLE IF NOT EXISTS cluster_state (
    k          TEXT PRIMARY KEY,
    v          TEXT NOT NULL,
    updated_at TEXT NOT NULL
);


CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

INSERT OR IGNORE INTO tenants (id, name, created_at)
VALUES ('default', 'default', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));









CREATE TABLE IF NOT EXISTS nodes (
    node_id             TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    display_name        TEXT NOT NULL,
    role                TEXT NOT NULL CHECK (role IN ('master','standby','agent')),
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
    UNIQUE (tenant_id, display_name)
);

CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
CREATE INDEX IF NOT EXISTS idx_nodes_role   ON nodes(role);
CREATE INDEX IF NOT EXISTS idx_nodes_seen   ON nodes(last_seen_at);








CREATE TABLE IF NOT EXISTS enrollment_tokens (
    token_id     TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    token_hash   TEXT NOT NULL UNIQUE,
    role         TEXT NOT NULL CHECK (role IN ('master','standby','agent')),
    name_pattern TEXT NOT NULL DEFAULT '',
    max_uses     INTEGER NOT NULL CHECK (max_uses > 0),
    used_count   INTEGER NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    expires_at   TEXT NOT NULL,
    revoked_at   TEXT,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    CHECK (used_count <= max_uses)
);

CREATE INDEX IF NOT EXISTS idx_tokens_expiry ON enrollment_tokens(expires_at);





CREATE TABLE IF NOT EXISTS certificates (
    serial              TEXT PRIMARY KEY,
    node_id             TEXT NOT NULL REFERENCES nodes(node_id),
    fingerprint_sha256  TEXT NOT NULL UNIQUE,
    subject_cn          TEXT NOT NULL,
    san_uri             TEXT NOT NULL,
    not_before          TEXT NOT NULL,
    not_after           TEXT NOT NULL,
    issued_at           TEXT NOT NULL,
    revoked_at          TEXT,
    revoke_reason       TEXT NOT NULL DEFAULT '',
    superseded_by       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_certs_node    ON certificates(node_id);
CREATE INDEX IF NOT EXISTS idx_certs_revoked ON certificates(revoked_at);
CREATE INDEX IF NOT EXISTS idx_certs_expiry  ON certificates(not_after);




CREATE TABLE IF NOT EXISTS capabilities (
    id                 TEXT PRIMARY KEY,
    node_id            TEXT NOT NULL REFERENCES nodes(node_id),
    name               TEXT NOT NULL,
    version            TEXT NOT NULL,
    risk               TEXT NOT NULL DEFAULT 'confirm'
                           CHECK (risk IN ('low','confirm','blocked')),
    input_schema_json  TEXT NOT NULL DEFAULT '{}',
    output_schema_json TEXT NOT NULL DEFAULT '{}',
    allow_from_json    TEXT NOT NULL DEFAULT '[]',
    health             TEXT NOT NULL DEFAULT 'unknown',
    disabled           INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
    registered_at      TEXT NOT NULL,
    last_call_at       TEXT,
    UNIQUE (node_id, name, version)
);

CREATE INDEX IF NOT EXISTS idx_caps_node ON capabilities(node_id);
CREATE INDEX IF NOT EXISTS idx_caps_name ON capabilities(name);






CREATE TABLE IF NOT EXISTS calls (
    call_id            TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    node_id            TEXT NOT NULL REFERENCES nodes(node_id),
    capability_name    TEXT NOT NULL,
    capability_version TEXT NOT NULL DEFAULT '',
    idempotency_key    TEXT NOT NULL,
    requester          TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL
                           CHECK (status IN ('queued','dispatched','succeeded','failed','expired')),
    input_json         TEXT NOT NULL DEFAULT '{}',
    output_json        TEXT,
    error_code         TEXT NOT NULL DEFAULT '',
    error_message      TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    dispatched_at      TEXT,
    completed_at       TEXT,
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    request_id         TEXT NOT NULL DEFAULT '',
    trace_id           TEXT NOT NULL DEFAULT '',
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_calls_status  ON calls(status);
CREATE INDEX IF NOT EXISTS idx_calls_node    ON calls(node_id);
CREATE INDEX IF NOT EXISTS idx_calls_created ON calls(created_at);













CREATE TABLE IF NOT EXISTS events (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id     TEXT NOT NULL UNIQUE,
    ts           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL DEFAULT 'default',
    category     TEXT NOT NULL CHECK (category IN ('audit','system')),
    event        TEXT NOT NULL,
    actor        TEXT NOT NULL DEFAULT '',
    actor_type   TEXT NOT NULL DEFAULT '',
    acting_for   TEXT NOT NULL DEFAULT '',
    action       TEXT NOT NULL DEFAULT '',
    target       TEXT NOT NULL DEFAULT '',
    result       TEXT NOT NULL DEFAULT '',
    source_ip    TEXT NOT NULL DEFAULT '',
    node_id      TEXT NOT NULL DEFAULT '',
    request_id   TEXT NOT NULL DEFAULT '',
    trace_id     TEXT NOT NULL DEFAULT '',
    leader_epoch INTEGER NOT NULL DEFAULT 1,
    detail_json  TEXT NOT NULL DEFAULT '{}',
    prev_hash    TEXT NOT NULL DEFAULT '',
    hash         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_ts    ON events(ts);
CREATE INDEX IF NOT EXISTS idx_events_node  ON events(node_id);
CREATE INDEX IF NOT EXISTS idx_events_event ON events(event);
CREATE INDEX IF NOT EXISTS idx_events_cat   ON events(category);





CREATE TABLE IF NOT EXISTS replication_snapshots (
    id         TEXT PRIMARY KEY,
    taken_at   TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    sha256     TEXT NOT NULL,
    applied_at TEXT
);

INSERT OR IGNORE INTO schema_migrations (version, applied_at)
VALUES (1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
