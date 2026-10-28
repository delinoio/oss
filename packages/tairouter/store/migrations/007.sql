-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE IF NOT EXISTS {{temp}}.native_quotas (
 org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL DEFAULT 'provider',id text NOT NULL,
 generation bigint NOT NULL,version text NOT NULL,value bytea NOT NULL,expires_at timestamptz NOT NULL,
 PRIMARY KEY(org_id,user_id,id),FOREIGN KEY(org_id,user_id,kind,id) REFERENCES {{schema}}.private_data ON DELETE CASCADE
);
CREATE SEQUENCE IF NOT EXISTS {{schema}}.native_affinity_epoch;
CREATE TABLE IF NOT EXISTS {{temp}}.native_affinities (
 org_id text NOT NULL,user_id text NOT NULL,key_id text NOT NULL REFERENCES {{schema}}.api_keys ON DELETE CASCADE,
 generation bigint NOT NULL,group_id text NOT NULL,group_version text NOT NULL,session_hash text NOT NULL,
 account_id text NOT NULL DEFAULT '',credential_version text NOT NULL DEFAULT '',epoch bigint NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),expires_at timestamptz NOT NULL,
 PRIMARY KEY(org_id,user_id,key_id,group_id,group_version,session_hash),
 FOREIGN KEY(org_id,user_id) REFERENCES {{schema}}.memberships ON DELETE CASCADE
);
INSERT INTO {{schema}}.schema_versions VALUES(7) ON CONFLICT DO NOTHING;
