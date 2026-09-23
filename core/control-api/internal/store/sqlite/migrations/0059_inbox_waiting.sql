-- SPDX-License-Identifier: Apache-2.0




















ALTER TABLE nodes ADD COLUMN inbox_waiting INTEGER;








ALTER TABLE nodes ADD COLUMN inbox_oldest_waiting_at TEXT NOT NULL DEFAULT '';
