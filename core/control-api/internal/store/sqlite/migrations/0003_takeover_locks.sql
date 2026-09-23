-- SPDX-License-Identifier: Apache-2.0












CREATE TABLE IF NOT EXISTS takeover_locks (
    node_id     TEXT PRIMARY KEY REFERENCES nodes(node_id),


    holder      TEXT NOT NULL,


    session_id  TEXT NOT NULL,
    acquired_at TEXT NOT NULL,
    expires_at  TEXT NOT NULL,



    preempted_from TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_takeover_locks_expiry ON takeover_locks(expires_at);
