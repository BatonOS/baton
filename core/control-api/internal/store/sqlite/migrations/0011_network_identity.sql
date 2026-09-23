-- SPDX-License-Identifier: Apache-2.0


































CREATE TABLE IF NOT EXISTS networks (
    network_id      TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    display_name    TEXT NOT NULL,







    public_key_pem  TEXT NOT NULL,
    private_key_pem TEXT NOT NULL,
    fingerprint     TEXT NOT NULL,

    created_at      TEXT NOT NULL,








    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);








CREATE TABLE IF NOT EXISTS network_addresses (
    network_id  TEXT NOT NULL,
    address     TEXT NOT NULL,
    source      TEXT NOT NULL,
    created_at  TEXT NOT NULL,

    PRIMARY KEY (network_id, address),
    FOREIGN KEY (network_id) REFERENCES networks(network_id) ON DELETE CASCADE
);



CREATE TABLE IF NOT EXISTS network_endpoints (
    network_id  TEXT NOT NULL,
    address     TEXT NOT NULL,
    port        INTEGER NOT NULL,
    protocol    TEXT NOT NULL,
    priority    INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL,

    PRIMARY KEY (network_id, address, port),
    FOREIGN KEY (network_id) REFERENCES networks(network_id) ON DELETE CASCADE
);








CREATE TABLE IF NOT EXISTS trusted_peers (
    peer_network_id      TEXT NOT NULL,
    tenant_id            TEXT NOT NULL,
    identity_fingerprint TEXT NOT NULL,
    public_key_pem       TEXT NOT NULL,




    trust_state          TEXT NOT NULL,
    trust_origin         TEXT NOT NULL,
    last_seen_at         TEXT NOT NULL,
    created_at           TEXT NOT NULL,

    PRIMARY KEY (peer_network_id, tenant_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS trusted_peer_addresses (
    peer_network_id TEXT NOT NULL,
    address         TEXT NOT NULL,
    kind            TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,

    PRIMARY KEY (peer_network_id, address, kind)
);















