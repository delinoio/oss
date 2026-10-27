-- SPDX-License-Identifier: Apache-2.0
SET LOCAL search_path TO {{schema}};
CREATE TABLE IF NOT EXISTS admission_instances(id text PRIMARY KEY,state text NOT NULL CHECK(state IN ('active','draining','terminated')),drain_hash bytea,created_at timestamptz NOT NULL DEFAULT clock_timestamp());
INSERT INTO admission_instances(id,state) VALUES('legacy','active') ON CONFLICT DO NOTHING;
ALTER TABLE admissions ADD COLUMN IF NOT EXISTS instance_id text NOT NULL DEFAULT 'legacy' REFERENCES admission_instances(id);
INSERT INTO schema_versions VALUES(5) ON CONFLICT DO NOTHING;
