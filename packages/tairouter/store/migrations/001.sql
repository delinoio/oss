-- SPDX-License-Identifier: Apache-2.0
CREATE SCHEMA IF NOT EXISTS {{schema}};
CREATE SCHEMA IF NOT EXISTS {{temp}};
SET LOCAL search_path TO {{schema}};
CREATE TABLE IF NOT EXISTS schema_versions(version integer PRIMARY KEY);
CREATE TABLE IF NOT EXISTS users(id text PRIMARY KEY, issuer text NOT NULL, subject text NOT NULL, email text NOT NULL, UNIQUE(issuer,subject));
CREATE TABLE IF NOT EXISTS organizations(id text PRIMARY KEY, name text NOT NULL);
CREATE SEQUENCE IF NOT EXISTS membership_generation;
CREATE TABLE IF NOT EXISTS memberships(org_id text REFERENCES organizations ON DELETE CASCADE,user_id text REFERENCES users ON DELETE CASCADE,role text NOT NULL CHECK(role IN ('owner','administrator','member')),generation bigint NOT NULL DEFAULT nextval('membership_generation'),PRIMARY KEY(org_id,user_id));
CREATE UNIQUE INDEX IF NOT EXISTS one_owner ON memberships(org_id) WHERE role='owner';
CREATE TABLE IF NOT EXISTS sessions(hash bytea PRIMARY KEY,user_id text REFERENCES users ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now(),last_seen timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS transfers(org_id text PRIMARY KEY REFERENCES organizations ON DELETE CASCADE,from_user text NOT NULL,to_user text NOT NULL,expires_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS api_keys(id text PRIMARY KEY,org_id text REFERENCES organizations ON DELETE CASCADE,user_id text REFERENCES users ON DELETE CASCADE,generation bigint NOT NULL,name text NOT NULL,hash bytea UNIQUE NOT NULL,expires_at timestamptz,revoked boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS private_data(org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL,id text NOT NULL,value bytea NOT NULL,PRIMARY KEY(org_id,user_id,kind,id),FOREIGN KEY(org_id,user_id) REFERENCES memberships ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS admissions(id text PRIMARY KEY,org_id text REFERENCES organizations ON DELETE CASCADE,user_id text NOT NULL,key_id text NOT NULL,generation bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),lease_until timestamptz NOT NULL DEFAULT now()+interval '2 minutes',released boolean NOT NULL DEFAULT false);
CREATE INDEX IF NOT EXISTS admissions_scope ON admissions(org_id,user_id,created_at);
CREATE TABLE IF NOT EXISTS settings(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),rpm integer NOT NULL DEFAULT 120 CHECK(rpm BETWEEN 1 AND 6000),concurrency integer NOT NULL DEFAULT 10 CHECK(concurrency BETWEEN 1 AND 100),usage_days integer NOT NULL DEFAULT 30 CHECK(usage_days BETWEEN 1 AND 90),aggregate_days integer NOT NULL DEFAULT 365 CHECK(aggregate_days BETWEEN 30 AND 730),audit_days integer NOT NULL DEFAULT 90 CHECK(audit_days BETWEEN 30 AND 365),destinations jsonb NOT NULL DEFAULT '[]',prices jsonb NOT NULL DEFAULT '{}');
INSERT INTO settings(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS audits(id text PRIMARY KEY,org_id text REFERENCES organizations ON DELETE CASCADE,actor_id text REFERENCES users ON DELETE SET NULL,action text NOT NULL,target_id text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS usage(id text PRIMARY KEY,org_id text REFERENCES organizations ON DELETE CASCADE,user_id text REFERENCES users ON DELETE SET NULL,model text NOT NULL,provider text NOT NULL,error_code text NOT NULL,input_tokens bigint,output_tokens bigint,cost numeric,price jsonb NOT NULL,provenance text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS daily_usage(id text PRIMARY KEY,org_id text REFERENCES organizations ON DELETE CASCADE,user_id text REFERENCES users ON DELETE SET NULL,day date NOT NULL,model text NOT NULL,requests bigint NOT NULL,input_tokens bigint NOT NULL,output_tokens bigint NOT NULL,cost numeric NOT NULL,missing_usage bigint NOT NULL,missing_cost bigint NOT NULL);
CREATE TABLE IF NOT EXISTS invitations(id text PRIMARY KEY,org_id text REFERENCES organizations ON DELETE CASCADE,email text NOT NULL,hash bytea UNIQUE NOT NULL,status text NOT NULL DEFAULT 'pending',created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '7 days',attempts integer NOT NULL DEFAULT 0,next_attempt timestamptz NOT NULL DEFAULT now(),claim text,claim_until timestamptz);
CREATE TABLE IF NOT EXISTS mail_attempts(org_id text REFERENCES organizations ON DELETE CASCADE,attempted_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS artifacts(id text NOT NULL,org_id text NOT NULL,user_id text NOT NULL,object_key text UNIQUE NOT NULL,size bigint NOT NULL CHECK(size BETWEEN 1 AND 52428800),created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours',PRIMARY KEY(org_id,user_id,id),FOREIGN KEY(org_id,user_id) REFERENCES memberships ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS object_deletes(object_key text PRIMARY KEY);
CREATE OR REPLACE FUNCTION queue_object_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO {{schema}}.object_deletes(object_key) VALUES(OLD.object_key) ON CONFLICT DO NOTHING; RETURN OLD; END $$;
DROP TRIGGER IF EXISTS artifact_delete ON artifacts;
CREATE TRIGGER artifact_delete BEFORE DELETE ON artifacts FOR EACH ROW EXECUTE FUNCTION queue_object_delete();
CREATE TABLE IF NOT EXISTS {{temp}}.states(org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL,id text NOT NULL,value bytea NOT NULL,size bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,PRIMARY KEY(org_id,user_id,kind,id),FOREIGN KEY(org_id,user_id) REFERENCES {{schema}}.memberships ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS {{temp}}.devices(id text PRIMARY KEY,code text UNIQUE NOT NULL,secret_hash bytea NOT NULL,name text NOT NULL,server text NOT NULL,expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes',last_poll timestamptz,org_id text,user_id text,generation bigint,key_id text REFERENCES {{schema}}.api_keys ON DELETE CASCADE,value bytea,status text NOT NULL DEFAULT 'pending');
CREATE TABLE IF NOT EXISTS {{temp}}.mail_tokens(invitation_id text PRIMARY KEY REFERENCES {{schema}}.invitations ON DELETE CASCADE,value bytea NOT NULL);
INSERT INTO schema_versions VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS {{temp}}.login_states(hash bytea PRIMARY KEY,binding bytea NOT NULL,nonce text NOT NULL,value bytea NOT NULL,expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes');
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS handoff_deadline timestamptz;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS handed_off boolean NOT NULL DEFAULT true;
ALTER TABLE usage ADD COLUMN IF NOT EXISTS expires_at timestamptz NOT NULL DEFAULT now()+interval '30 days';
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS expires_at timestamptz NOT NULL DEFAULT now()+interval '365 days';
ALTER TABLE audits ADD COLUMN IF NOT EXISTS expires_at timestamptz NOT NULL DEFAULT now()+interval '90 days';
-- Migration replay also rebuilds backup-excluded temporary state. Once version 2
-- expands usage dimensions, never reintroduce the superseded narrow constraint.
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM schema_versions WHERE version=2) THEN
  CREATE UNIQUE INDEX IF NOT EXISTS daily_usage_scope ON daily_usage(org_id,user_id,day,model);
 END IF;
END $$;
ALTER TABLE admissions ADD COLUMN IF NOT EXISTS usage_recorded boolean NOT NULL DEFAULT false;
ALTER TABLE artifacts ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT 'pending';
ALTER TABLE artifacts ADD COLUMN IF NOT EXISTS content_type text NOT NULL DEFAULT 'image/png';
ALTER TABLE artifacts ADD COLUMN IF NOT EXISTS upload_deadline timestamptz NOT NULL DEFAULT now()+interval '5 minutes';
ALTER TABLE object_deletes ADD COLUMN IF NOT EXISTS id text;
ALTER TABLE object_deletes ADD COLUMN IF NOT EXISTS not_before timestamptz NOT NULL DEFAULT now();
CREATE TABLE IF NOT EXISTS artifact_ids(id text PRIMARY KEY,object_key text UNIQUE NOT NULL);
CREATE OR REPLACE FUNCTION queue_object_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO {{schema}}.object_deletes(object_key,id,not_before) VALUES(OLD.object_key,OLD.id,CASE WHEN OLD.state='pending' THEN OLD.upload_deadline ELSE clock_timestamp() END) ON CONFLICT(object_key) DO UPDATE SET not_before=GREATEST(object_deletes.not_before,excluded.not_before); RETURN OLD; END $$;
ALTER TABLE private_data ADD COLUMN IF NOT EXISTS version text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS org_settings(org_id text PRIMARY KEY REFERENCES organizations ON DELETE CASCADE,value jsonb NOT NULL DEFAULT '{}');
ALTER TABLE transfers ADD COLUMN IF NOT EXISTS id text NOT NULL DEFAULT '';
