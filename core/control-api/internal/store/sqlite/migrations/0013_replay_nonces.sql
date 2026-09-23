-- SPDX-License-Identifier: Apache-2.0















CREATE TABLE IF NOT EXISTS replay_nonces (
    nonce      TEXT NOT NULL,



    scope      TEXT NOT NULL,
    expires_at TEXT NOT NULL,

    PRIMARY KEY (nonce, scope)
);




CREATE INDEX IF NOT EXISTS idx_replay_nonces_expiry ON replay_nonces (expires_at);
