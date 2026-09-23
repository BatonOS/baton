-- SPDX-License-Identifier: Apache-2.0






ALTER TABLE resources ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
