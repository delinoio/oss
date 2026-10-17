-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE provider_transactions (
 external_id text PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 kind text NOT NULL, amount numeric NOT NULL, currency text NOT NULL,
 created_at timestamptz NOT NULL
);
ALTER TABLE disputes ADD COLUMN transaction_id text NOT NULL DEFAULT '';
ALTER TABLE settlements ADD COLUMN billed_minor numeric NOT NULL DEFAULT 0;
CREATE TABLE billing_notices (
 tenant_id uuid NOT NULL REFERENCES tenants(id), reason text NOT NULL,
 active boolean NOT NULL, generation integer NOT NULL DEFAULT 0,
 PRIMARY KEY(tenant_id,reason)
);
CREATE TABLE adjustment_effects (
 external_id text PRIMARY KEY, transaction_id text NOT NULL,
 amount numeric NOT NULL, occurred_at timestamptz NOT NULL
);
ALTER TABLE tenants ALTER COLUMN owner_id DROP NOT NULL;
ALTER TABLE requests ADD COLUMN tenant_id uuid REFERENCES tenants(id);
