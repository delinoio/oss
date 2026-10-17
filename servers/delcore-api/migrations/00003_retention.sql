-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE usage_replay (
 service_id uuid NOT NULL REFERENCES services(id), event_key text NOT NULL,
 event_id uuid NOT NULL, fingerprint text NOT NULL,
 PRIMARY KEY(service_id,event_key)
);
-- Normal application statements remain unable to rewrite the journal. The
-- maintenance transaction enables deletion only for fully deleted tenants.
CREATE OR REPLACE FUNCTION immutable_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='ledger' AND TG_OP='DELETE'
 AND current_setting('delcore.retention',true)='on'
 AND EXISTS(SELECT 1 FROM tenants WHERE id=OLD.tenant_id AND deleted_at IS NOT NULL)
 THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'accounting records are append-only';
END $$;
