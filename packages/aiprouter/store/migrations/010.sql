-- SPDX-License-Identifier: Apache-2.0
ALTER TABLE {{schema}}.native_health_permits ADD COLUMN IF NOT EXISTS bindings jsonb NOT NULL DEFAULT '[]';
CREATE TABLE IF NOT EXISTS {{temp}}.vertex_projects(org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL DEFAULT 'provider',id text NOT NULL,version text NOT NULL,generation bigint NOT NULL,value bytea,expires_at timestamptz,claim_hash bytea,claim_until timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(org_id,user_id,id,version),FOREIGN KEY(org_id,user_id,kind,id) REFERENCES {{schema}}.private_data ON DELETE CASCADE);
INSERT INTO {{schema}}.schema_versions VALUES(10) ON CONFLICT DO NOTHING;
