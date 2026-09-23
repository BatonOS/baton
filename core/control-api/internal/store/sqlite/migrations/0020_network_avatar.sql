-- SPDX-License-Identifier: Apache-2.0









CREATE TABLE IF NOT EXISTS network_avatars (
    network_id   TEXT PRIMARY KEY,
    content_type TEXT NOT NULL,
    data         BLOB NOT NULL,
    updated_at   TEXT NOT NULL,
    FOREIGN KEY (network_id) REFERENCES networks(network_id)
);
