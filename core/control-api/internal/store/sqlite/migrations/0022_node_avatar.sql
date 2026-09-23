-- SPDX-License-Identifier: Apache-2.0







CREATE TABLE IF NOT EXISTS node_avatars (
    node_id      TEXT PRIMARY KEY,
    content_type TEXT NOT NULL,
    full         BLOB NOT NULL,
    thumb        BLOB NOT NULL,
    updated_at   TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(node_id)
);
