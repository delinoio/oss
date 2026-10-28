-- SPDX-License-Identifier: Apache-2.0
CREATE INDEX IF NOT EXISTS admissions_active_scope ON {{schema}}.admissions(org_id,user_id) WHERE NOT released;
INSERT INTO {{schema}}.schema_versions VALUES(12) ON CONFLICT DO NOTHING;
