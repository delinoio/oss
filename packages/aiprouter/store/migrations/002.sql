-- SPDX-License-Identifier: Apache-2.0
SET LOCAL search_path TO {{schema}};
ALTER TABLE settings ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 1;
CREATE TABLE IF NOT EXISTS {{temp}}.provider_flows (
 org_id text NOT NULL,user_id text NOT NULL,id text NOT NULL,generation bigint NOT NULL,
 value bytea NOT NULL,expires_at timestamptz NOT NULL,claim_hash bytea,
 PRIMARY KEY(org_id,user_id,id),FOREIGN KEY(org_id,user_id) REFERENCES {{schema}}.memberships ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS private_refresh (
 org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL,id text NOT NULL,
 version text NOT NULL,claim_hash bytea NOT NULL,state text NOT NULL CHECK(state IN ('claimed','uncertain')),
 PRIMARY KEY(org_id,user_id,kind,id),FOREIGN KEY(org_id,user_id,kind,id) REFERENCES private_data ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS selection_state (
 org_id text NOT NULL,user_id text NOT NULL,group_id text NOT NULL,candidate_id text NOT NULL,
 last_selected bigint NOT NULL DEFAULT 0,current_weight bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(org_id,user_id,group_id,candidate_id),FOREIGN KEY(org_id,user_id) REFERENCES memberships ON DELETE CASCADE
);
CREATE SEQUENCE IF NOT EXISTS selection_sequence;

ALTER TABLE {{temp}}.provider_flows ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'pending';
ALTER TABLE {{temp}}.provider_flows ADD COLUMN IF NOT EXISTS target_kind text NOT NULL DEFAULT '';
ALTER TABLE {{temp}}.provider_flows ADD COLUMN IF NOT EXISTS target_id text NOT NULL DEFAULT '';
ALTER TABLE {{temp}}.provider_flows ADD COLUMN IF NOT EXISTS expected_version text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS account_cooldowns(org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL DEFAULT 'provider',id text NOT NULL,version text NOT NULL,model text NOT NULL,until_at timestamptz NOT NULL,PRIMARY KEY(org_id,user_id,id,model),FOREIGN KEY(org_id,user_id,kind,id) REFERENCES private_data ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS selection_affinity(org_id text NOT NULL,user_id text NOT NULL,group_id text NOT NULL,affinity_hash bytea NOT NULL,candidate_id text NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(org_id,user_id,group_id,affinity_hash),FOREIGN KEY(org_id,user_id) REFERENCES memberships ON DELETE CASCADE);
ALTER TABLE selection_state ADD COLUMN IF NOT EXISTS credential_version text NOT NULL DEFAULT '';
ALTER TABLE usage ADD COLUMN IF NOT EXISTS outcome text NOT NULL DEFAULT 'unknown';
ALTER TABLE usage ADD COLUMN IF NOT EXISTS price_known boolean NOT NULL DEFAULT false;
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT '';
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS outcome text NOT NULL DEFAULT 'unknown';
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS missing_price bigint NOT NULL DEFAULT 0;
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS failed_requests bigint NOT NULL DEFAULT 0;
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS known_input bigint NOT NULL DEFAULT 0;
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS known_output bigint NOT NULL DEFAULT 0;
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS known_cost bigint NOT NULL DEFAULT 0;
DROP INDEX IF EXISTS daily_usage_scope;
CREATE UNIQUE INDEX IF NOT EXISTS daily_usage_dimensions ON daily_usage(org_id,user_id,day,model,provider,outcome);
ALTER TABLE audits ADD COLUMN IF NOT EXISTS outcome text NOT NULL DEFAULT 'unknown';
ALTER TABLE settings ADD COLUMN IF NOT EXISTS usage_history_floor timestamptz NOT NULL DEFAULT '-infinity';
CREATE TABLE IF NOT EXISTS operational_health(job text PRIMARY KEY,last_success timestamptz,last_failure timestamptz,consecutive_failures bigint NOT NULL DEFAULT 0);
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS delivery_state text NOT NULL DEFAULT 'queued';
CREATE TABLE IF NOT EXISTS {{temp}}.provider_states(org_id text NOT NULL,user_id text NOT NULL,key_id text NOT NULL,namespace text NOT NULL,id text NOT NULL,generation bigint NOT NULL,value bytea NOT NULL,version text NOT NULL,size bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),expires_at timestamptz NOT NULL,PRIMARY KEY(org_id,user_id,key_id,namespace,id),FOREIGN KEY(org_id,user_id) REFERENCES {{schema}}.memberships ON DELETE CASCADE);

ALTER TABLE {{temp}}.provider_states ADD COLUMN IF NOT EXISTS quota_entries integer NOT NULL DEFAULT 256;
ALTER TABLE {{temp}}.provider_states ADD COLUMN IF NOT EXISTS quota_bytes bigint NOT NULL DEFAULT 67108864;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM schema_versions WHERE version=2) THEN
  UPDATE daily_usage SET missing_price=requests,known_input=GREATEST(0,requests-missing_usage),known_output=GREATEST(0,requests-missing_usage),known_cost=GREATEST(0,requests-missing_cost);
  UPDATE usage SET outcome=CASE WHEN error_code='canceled' THEN 'canceled' WHEN error_code='upstream_error' THEN 'upstream_error' WHEN error_code='gateway_error' THEN 'gateway_error' WHEN error_code<>'' THEN 'failed' ELSE 'unknown' END;
 END IF;
END $$;
INSERT INTO schema_versions VALUES(2) ON CONFLICT DO NOTHING;
