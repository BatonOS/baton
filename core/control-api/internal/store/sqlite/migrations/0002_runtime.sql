-- SPDX-License-Identifier: Apache-2.0










ALTER TABLE nodes ADD COLUMN owner TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN runtime_name TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN runtime_type TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN runtime_image TEXT NOT NULL DEFAULT '';



ALTER TABLE nodes ADD COLUMN runtime_enterable INTEGER NOT NULL DEFAULT 0;













ALTER TABLE nodes ADD COLUMN runtime_state TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN runtime_activity TEXT NOT NULL DEFAULT 'unreported';
ALTER TABLE nodes ADD COLUMN runtime_restart_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN runtime_started_at TEXT;
ALTER TABLE nodes ADD COLUMN runtime_last_exit_code INTEGER;
ALTER TABLE nodes ADD COLUMN runtime_oom_killed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN runtime_last_error TEXT NOT NULL DEFAULT '';






ALTER TABLE nodes ADD COLUMN inflight_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN uptime_sec INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_nodes_runtime_state ON nodes(runtime_state);
