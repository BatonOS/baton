-- SPDX-License-Identifier: Apache-2.0

























ALTER TABLE identities ADD COLUMN inbox_act_on TEXT NOT NULL DEFAULT '';










ALTER TABLE identities ADD COLUMN inbox_allow_senders TEXT NOT NULL DEFAULT '';
ALTER TABLE identities ADD COLUMN inbox_allow_networks TEXT NOT NULL DEFAULT '';
