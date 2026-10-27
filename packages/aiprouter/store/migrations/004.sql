-- SPDX-License-Identifier: Apache-2.0
SET LOCAL search_path TO {{schema}};
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS prefix text NOT NULL DEFAULT '' CHECK(length(prefix)<=12);
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS last_used_at timestamptz;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS device boolean NOT NULL DEFAULT false;
UPDATE api_keys SET device=true WHERE handoff_deadline IS NOT NULL;
INSERT INTO schema_versions VALUES(4) ON CONFLICT DO NOTHING;
