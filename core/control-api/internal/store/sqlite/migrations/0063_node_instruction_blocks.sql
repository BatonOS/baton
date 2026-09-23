-- SPDX-License-Identifier: Apache-2.0























CREATE TABLE IF NOT EXISTS node_instruction_blocks (
  node_id    TEXT NOT NULL,
  plugin_id  TEXT NOT NULL,
  path       TEXT NOT NULL,
  sha256     TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  PRIMARY KEY (node_id, plugin_id, path, sha256)
);
