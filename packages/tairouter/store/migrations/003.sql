-- SPDX-License-Identifier: Apache-2.0
SET LOCAL search_path TO {{schema}};
CREATE TABLE IF NOT EXISTS account_soft_denials(org_id text NOT NULL,user_id text NOT NULL,kind text NOT NULL DEFAULT 'provider',id text NOT NULL,version text NOT NULL,model text NOT NULL,until_at timestamptz NOT NULL,PRIMARY KEY(org_id,user_id,id,model),FOREIGN KEY(org_id,user_id,kind,id) REFERENCES private_data ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS mistral_holds(org_id text NOT NULL,user_id text NOT NULL,identity text NOT NULL,hold_until timestamptz NOT NULL,active boolean NOT NULL DEFAULT true,probe_hash bytea,probe_ticket text,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(org_id,user_id,identity),FOREIGN KEY(org_id,user_id) REFERENCES memberships ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS mistral_waiters(id text PRIMARY KEY,org_id text NOT NULL,user_id text NOT NULL,identity text NOT NULL,admission_id text NOT NULL,key_id text NOT NULL,generation bigint NOT NULL,provider_id text NOT NULL,version text NOT NULL,FOREIGN KEY(org_id,user_id,identity) REFERENCES mistral_holds ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS operation_receipts(admission_id text REFERENCES admissions ON DELETE CASCADE,operation_id text NOT NULL,usage_id text NOT NULL UNIQUE,PRIMARY KEY(admission_id,operation_id));
INSERT INTO schema_versions VALUES(3) ON CONFLICT DO NOTHING;
ALTER TABLE mistral_holds ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 0;
ALTER TABLE mistral_holds ADD COLUMN IF NOT EXISTS probe_revision bigint;
ALTER TABLE artifacts DROP CONSTRAINT IF EXISTS artifacts_size_check;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='artifacts'::regclass AND conname='artifact_media_size') THEN
  ALTER TABLE artifacts ADD CONSTRAINT artifact_media_size CHECK(size>=1 AND ((content_type='video/mp4' AND size<=209715200) OR (content_type IN ('image/png','image/jpeg','image/webp','image/gif','image/avif') AND size<=52428800)));
 END IF;
END $$;
