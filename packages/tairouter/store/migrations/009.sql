-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE IF NOT EXISTS {{schema}}.native_health (
 org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL DEFAULT 'provider',account_id text NOT NULL,credential_version text NOT NULL,
 lane text NOT NULL CHECK(lane IN ('transient','quota','family')),model text NOT NULL,generation bigint NOT NULL,
 state jsonb NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(org_id,user_id,account_id,credential_version,lane,model),
 FOREIGN KEY(org_id,user_id,kind,account_id) REFERENCES {{schema}}.private_data ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS {{schema}}.native_health_permits (
 id text PRIMARY KEY,org_id text NOT NULL,user_id text NOT NULL,key_id text NOT NULL,admission_id text NOT NULL REFERENCES {{schema}}.admissions ON DELETE CASCADE,generation bigint NOT NULL,
 account_id text NOT NULL,credential_version text NOT NULL,lane text NOT NULL,model text NOT NULL,
 revision bigint NOT NULL,probe_epoch bigint NOT NULL,probe boolean NOT NULL,status text NOT NULL CHECK(status IN ('new','reserved','dispatched','finished','released')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(org_id,user_id,account_id,credential_version,lane,model) REFERENCES {{schema}}.native_health ON DELETE CASCADE
);
ALTER TABLE {{temp}}.native_affinities ADD COLUMN IF NOT EXISTS hold_since timestamptz;
ALTER TABLE {{temp}}.native_affinities ADD COLUMN IF NOT EXISTS detour_account_id text NOT NULL DEFAULT '';
INSERT INTO {{schema}}.schema_versions VALUES(9) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS {{schema}}.native_recovery_buckets(org_id text NOT NULL,user_id text NOT NULL,pool_key text NOT NULL,bucket_start timestamptz NOT NULL,initials bigint NOT NULL DEFAULT 0,recoveries bigint NOT NULL DEFAULT 0,PRIMARY KEY(org_id,user_id,pool_key,bucket_start),FOREIGN KEY(org_id,user_id) REFERENCES {{schema}}.memberships ON DELETE CASCADE);
ALTER TABLE {{schema}}.native_health_permits ADD COLUMN IF NOT EXISTS recovery_pool text NOT NULL DEFAULT '';
ALTER TABLE {{schema}}.native_health_permits ADD COLUMN IF NOT EXISTS recovery_class text NOT NULL DEFAULT '';
ALTER TABLE {{temp}}.native_quotas ADD COLUMN IF NOT EXISTS recorded_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE TABLE IF NOT EXISTS {{schema}}.quota_collection_claims(org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL DEFAULT 'provider',id text NOT NULL,version text NOT NULL,purpose text NOT NULL,generation bigint NOT NULL,claim_hash bytea,claim_until timestamptz NOT NULL,next_at timestamptz NOT NULL,PRIMARY KEY(org_id,user_id,id,version,purpose),FOREIGN KEY(org_id,user_id,kind,id) REFERENCES {{schema}}.private_data ON DELETE CASCADE);

ALTER TABLE {{schema}}.native_health DROP CONSTRAINT IF EXISTS native_health_lane_check;
ALTER TABLE {{schema}}.native_health ADD CONSTRAINT native_health_lane_check CHECK(lane IN ('transient','quota','family'));
