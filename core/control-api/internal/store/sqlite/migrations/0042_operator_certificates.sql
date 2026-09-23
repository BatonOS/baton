




















CREATE TABLE IF NOT EXISTS operator_certificates (
    serial              TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL,





    operator_name       TEXT NOT NULL,

    fingerprint_sha256  TEXT NOT NULL UNIQUE,
    subject_cn          TEXT NOT NULL,
    san_uri             TEXT NOT NULL,
    not_before          TEXT NOT NULL,
    not_after           TEXT NOT NULL,
    issued_at           TEXT NOT NULL,





    issued_by           TEXT NOT NULL DEFAULT '',
    from_ip             TEXT NOT NULL DEFAULT '',

    revoked_at          TEXT,
    revoke_reason       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_opcerts_name    ON operator_certificates(operator_name);
CREATE INDEX IF NOT EXISTS idx_opcerts_revoked ON operator_certificates(revoked_at);
