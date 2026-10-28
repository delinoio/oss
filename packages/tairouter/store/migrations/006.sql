-- SPDX-License-Identifier: Apache-2.0
ALTER TABLE {{temp}}.provider_states ADD COLUMN IF NOT EXISTS account_id text NOT NULL DEFAULT '';
ALTER TABLE {{temp}}.provider_states ADD COLUMN IF NOT EXISTS credential_version text NOT NULL DEFAULT '';
INSERT INTO {{schema}}.schema_versions VALUES(6) ON CONFLICT DO NOTHING;
