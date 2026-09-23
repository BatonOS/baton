-- SPDX-License-Identifier: Apache-2.0













ALTER TABLE messages ADD COLUMN via TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN via_isolated INTEGER NOT NULL DEFAULT 0;
