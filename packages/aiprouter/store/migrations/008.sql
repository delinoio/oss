-- SPDX-License-Identifier: Apache-2.0
ALTER TABLE {{temp}}.native_affinities ADD COLUMN IF NOT EXISTS successes integer NOT NULL DEFAULT 0 CHECK(successes>=0);
INSERT INTO {{schema}}.schema_versions VALUES(8) ON CONFLICT DO NOTHING;
