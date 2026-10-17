-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE users (
 id uuid PRIMARY KEY, issuer text NOT NULL, subject_hash text NOT NULL,
 email text NOT NULL, withdrawing_at timestamptz,
 UNIQUE (issuer, subject_hash)
);
CREATE TABLE services (
 id uuid PRIMARY KEY, name text NOT NULL UNIQUE, client_id text NOT NULL UNIQUE,
 capabilities text[] NOT NULL, scopes text[] NOT NULL
);
CREATE TABLE plans (
 id uuid PRIMARY KEY, name text NOT NULL, rank integer NOT NULL UNIQUE,
 free boolean NOT NULL, is_default boolean NOT NULL DEFAULT false,
 postpaid boolean NOT NULL, CHECK (NOT is_default OR free)
);
CREATE UNIQUE INDEX default_free_plan ON plans (is_default) WHERE is_default;
CREATE TABLE meters (service_id uuid NOT NULL REFERENCES services(id),name text NOT NULL,PRIMARY KEY(service_id,name));
CREATE TABLE addons (id uuid PRIMARY KEY,service_id uuid NOT NULL REFERENCES services(id),name text NOT NULL);
CREATE TABLE prices (
 id uuid PRIMARY KEY, plan_id uuid REFERENCES plans(id), service_id uuid REFERENCES services(id),
 meter text NOT NULL, unit_price numeric NOT NULL CHECK(unit_price>=0),
 included numeric NOT NULL CHECK(included>=0), currency text NOT NULL,
 external_price_id text NOT NULL, created_at timestamptz NOT NULL,
 addon_id uuid REFERENCES addons(id), kind integer NOT NULL DEFAULT 1 CHECK(kind IN (1,2)),
 CHECK ((plan_id IS NOT NULL)::integer+(addon_id IS NOT NULL)::integer=1)
);
CREATE TABLE tenants (
 id uuid PRIMARY KEY, name text NOT NULL, personal boolean NOT NULL, owner_id uuid NOT NULL REFERENCES users(id),
 plan_id uuid NOT NULL REFERENCES plans(id), operator_block boolean NOT NULL DEFAULT false,
 renewal_failed boolean NOT NULL DEFAULT false, deleting_at timestamptz, deleted_at timestamptz,
 external_customer_id text NOT NULL DEFAULT '', external_subscription_id text NOT NULL DEFAULT '',
 period_start timestamptz NOT NULL, period_end timestamptz NOT NULL,
 auto_threshold numeric, auto_amount numeric, auto_cap numeric, auto_suspended boolean NOT NULL DEFAULT false,
 CHECK(period_end>period_start),
 CHECK ((auto_threshold IS NULL AND auto_amount IS NULL AND auto_cap IS NULL) OR
 (auto_threshold>=0 AND auto_amount>0 AND auto_cap>=auto_amount))
);
CREATE UNIQUE INDEX personal_owner ON tenants(owner_id) WHERE personal AND deleted_at IS NULL;
CREATE TABLE members (
 tenant_id uuid NOT NULL REFERENCES tenants(id), user_id uuid NOT NULL REFERENCES users(id),
 roles integer[] NOT NULL CHECK (cardinality(roles)>0 AND roles <@ ARRAY[1,2,3,4]),
 PRIMARY KEY(tenant_id,user_id)
);
CREATE TABLE invitations (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id), email text NOT NULL,
 token_hash text NOT NULL UNIQUE, roles integer[] NOT NULL,
 expires_at timestamptz NOT NULL, accepted_at timestamptz, revoked_at timestamptz
);
CREATE TABLE ownership_transfers (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 sender_id uuid NOT NULL REFERENCES users(id), recipient_id uuid NOT NULL REFERENCES users(id),
 expires_at timestamptz NOT NULL, accepted_at timestamptz
);
CREATE TABLE groups (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id), name text NOT NULL,
 UNIQUE(tenant_id,name), UNIQUE(tenant_id,id)
);
CREATE TABLE group_members (
 tenant_id uuid NOT NULL, group_id uuid NOT NULL, user_id uuid NOT NULL,
 PRIMARY KEY(group_id,user_id), FOREIGN KEY(tenant_id,group_id) REFERENCES groups(tenant_id,id),
 FOREIGN KEY(tenant_id,user_id) REFERENCES members(tenant_id,user_id) ON DELETE CASCADE
);
CREATE TABLE group_permissions (
 group_id uuid NOT NULL REFERENCES groups(id), service_id uuid NOT NULL REFERENCES services(id),
 capability text NOT NULL, PRIMARY KEY(group_id,service_id,capability)
);
CREATE TABLE entitlements (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id), price_id uuid NOT NULL REFERENCES prices(id),
 starts_at timestamptz NOT NULL, ends_at timestamptz, CHECK(ends_at IS NULL OR ends_at>starts_at)
);
CREATE TABLE usage_events (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id), service_id uuid NOT NULL REFERENCES services(id),
 event_key text NOT NULL, fingerprint text NOT NULL, meter text NOT NULL,
 quantity numeric NOT NULL CHECK(quantity>=0), occurred_at timestamptz NOT NULL,
 accepted_at timestamptz NOT NULL, price_id uuid NOT NULL REFERENCES prices(id),
 period_start timestamptz NOT NULL, period_end timestamptz NOT NULL,
 correction_of uuid REFERENCES usage_events(id), UNIQUE(service_id,event_key)
);
CREATE UNIQUE INDEX one_correction ON usage_events(correction_of) WHERE correction_of IS NOT NULL;
CREATE TABLE ledger (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 service_id uuid REFERENCES services(id), meter text NOT NULL DEFAULT '',
 usage_id uuid REFERENCES usage_events(id), kind text NOT NULL,
 amount numeric NOT NULL, period_start timestamptz, period_end timestamptz,
 source_key text NOT NULL UNIQUE, created_at timestamptz NOT NULL
);
CREATE FUNCTION immutable_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'accounting records are append-only'; END $$;
CREATE TRIGGER ledger_immutable BEFORE UPDATE OR DELETE ON ledger FOR EACH ROW EXECUTE FUNCTION immutable_record();
CREATE TRIGGER prices_immutable BEFORE UPDATE OR DELETE ON prices FOR EACH ROW EXECUTE FUNCTION immutable_record();
CREATE TABLE jobs (
 id uuid PRIMARY KEY, tenant_id uuid REFERENCES tenants(id), kind text NOT NULL,
 dedup_key text NOT NULL UNIQUE, payload jsonb NOT NULL, state text NOT NULL DEFAULT 'pending'
 CHECK(state IN ('pending','running','reconcile','done','failed')),
 attempts integer NOT NULL DEFAULT 0, available_at timestamptz NOT NULL,
 checkout_url text NOT NULL DEFAULT '', external_id text NOT NULL DEFAULT '', error_category text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX one_auto_topup ON jobs(tenant_id) WHERE kind='auto_topup' AND state IN ('pending','running','reconcile');
CREATE TABLE webhooks (
 event_id text PRIMARY KEY, occurred_at timestamptz NOT NULL, payload jsonb NOT NULL,
 received_at timestamptz NOT NULL, processed_at timestamptz
);
CREATE TABLE provider_objects (
 external_id text PRIMARY KEY, occurred_at timestamptz NOT NULL, state text NOT NULL
);
CREATE TABLE disputes (
 external_id text PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id), active boolean NOT NULL
);
CREATE TABLE audit (
 id uuid PRIMARY KEY, actor_id text NOT NULL, tenant_id uuid, action text NOT NULL,
 reason text NOT NULL, correlation_id uuid NOT NULL, created_at timestamptz NOT NULL
);
CREATE TABLE requests (
 actor_id text NOT NULL, request_key text NOT NULL, fingerprint text NOT NULL,
 response bytea NOT NULL, created_at timestamptz NOT NULL,
 PRIMARY KEY(actor_id,request_key)
);
CREATE INDEX jobs_ready ON jobs(available_at) WHERE state IN ('pending','reconcile');
CREATE INDEX usage_period ON usage_events(tenant_id,service_id,meter,period_start);
CREATE INDEX ledger_balance ON ledger(tenant_id,kind);
CREATE TABLE billing_periods (
 tenant_id uuid NOT NULL REFERENCES tenants(id), starts_at timestamptz NOT NULL,
 ends_at timestamptz NOT NULL, postpaid boolean NOT NULL,
 PRIMARY KEY(tenant_id,starts_at), CHECK(ends_at>starts_at)
);
CREATE TABLE settlements (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 period_start timestamptz NOT NULL, period_end timestamptz NOT NULL,
 service_id uuid NOT NULL REFERENCES services(id), meter text NOT NULL,
 amount numeric NOT NULL, source_key text NOT NULL UNIQUE,
 job_id uuid REFERENCES jobs(id), created_at timestamptz NOT NULL
);
