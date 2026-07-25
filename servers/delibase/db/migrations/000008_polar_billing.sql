ALTER TABLE organizations
    ADD COLUMN overage_limit_configured boolean NOT NULL DEFAULT false;

ALTER TABLE polar_customers
    ADD COLUMN external_id uuid GENERATED ALWAYS AS (organization_id) STORED,
    ADD CONSTRAINT polar_customers_external_id_unique UNIQUE (external_id),
    ADD CONSTRAINT polar_customers_external_id_matches_organization
        CHECK (external_id = organization_id);

ALTER TABLE subscriptions
    ADD COLUMN provider_event_at timestamptz NOT NULL DEFAULT '-infinity';

CREATE OR REPLACE FUNCTION preserve_terminal_subscription()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status IN ('canceled', 'revoked')
           AND EXISTS (
               SELECT 1
               FROM organizations
               WHERE id = OLD.organization_id
           ) THEN
            RAISE EXCEPTION 'terminal subscription history is immutable'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.status = 'revoked' THEN
        RAISE EXCEPTION 'revoked subscription history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE billing_periods
    ADD COLUMN requested_overage_limit_micros bigint;
UPDATE billing_periods
SET requested_overage_limit_micros = overage_limit_micros;
ALTER TABLE billing_periods
    ALTER COLUMN requested_overage_limit_micros SET DEFAULT 0,
    ALTER COLUMN requested_overage_limit_micros SET NOT NULL,
    ADD CONSTRAINT billing_periods_requested_overage_limit_check
        CHECK (requested_overage_limit_micros >= 0),
    ADD CONSTRAINT billing_periods_effective_overage_floor_check
        CHECK (overage_limit_micros >= requested_overage_limit_micros);

CREATE FUNCTION initialize_requested_overage_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.requested_overage_limit_micros = 0
            AND NEW.overage_limit_micros > 0 THEN
            NEW.requested_overage_limit_micros := NEW.overage_limit_micros;
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER billing_periods_initialize_requested_overage_limit
BEFORE INSERT OR UPDATE OF overage_limit_micros, requested_overage_limit_micros
ON billing_periods
FOR EACH ROW EXECUTE FUNCTION initialize_requested_overage_limit();

ALTER TABLE webhook_inbox DROP CONSTRAINT webhook_inbox_event_type_check;
ALTER TABLE webhook_inbox
    ADD CONSTRAINT webhook_inbox_event_type_check CHECK (event_type IN (
        'order.paid',
        'subscription.created',
        'subscription.updated',
        'subscription.active',
        'subscription.uncanceled',
        'subscription.past_due',
        'subscription.canceled',
        'subscription.revoked',
        'refund.created',
        'refund.updated'
    ));

ALTER TABLE audit_events DROP CONSTRAINT audit_events_type_check;
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_type_check CHECK (event_type IN (
        'authorization.decision',
        'organization.created',
        'organization.updated',
        'organization.deleted',
        'role.updated',
        'invitation.created',
        'invitation.accepted',
        'invitation.revoked',
        'team.created',
        'team.updated',
        'team.deleted',
        'billing_limit.updated',
        'checkout.created',
        'billing_portal_session.created',
        'subscription.updated',
        'refund.recorded',
        'reservation.created',
        'reservation.committed',
        'reservation.released',
        'settlement.recorded',
        'account.deletion_requested',
        'organization.deletion_requested',
        'webhook.received',
        'webhook.processed'
    ));

CREATE TABLE polar_catalog_mappings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    polar_product_id text NOT NULL UNIQUE,
    polar_environment text NOT NULL
        CHECK (polar_environment IN ('production', 'sandbox')),
    currency text NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    recurring_interval text NOT NULL DEFAULT 'month'
        CHECK (recurring_interval = 'month'),
    price_micros bigint NOT NULL DEFAULT 10000000
        CHECK (price_micros = 10000000),
    cycle_grant_micros bigint NOT NULL DEFAULT 10000000
        CHECK (cycle_grant_micros = 10000000),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_product_id) BETWEEN 1 AND 255)
);

CREATE TABLE polar_paid_cycles (
    polar_order_id text PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subscription_id uuid NOT NULL,
    billing_period_id uuid NOT NULL,
    grant_micros bigint NOT NULL CHECK (grant_micros = 10000000),
    reversed_micros bigint NOT NULL DEFAULT 0
        CHECK (reversed_micros BETWEEN 0 AND grant_micros),
    paid_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    UNIQUE (organization_id, polar_order_id),
    FOREIGN KEY (organization_id, subscription_id)
        REFERENCES subscriptions(organization_id, id),
    FOREIGN KEY (organization_id, billing_period_id)
        REFERENCES billing_periods(organization_id, id),
    CHECK (length(polar_order_id) BETWEEN 1 AND 255),
    CHECK (retain_until >= created_at + interval '7 years')
);

CREATE FUNCTION enforce_polar_paid_cycle_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.retain_until > transaction_timestamp() THEN
        RAISE EXCEPTION 'paid cycle retention period has not elapsed'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER polar_paid_cycles_enforce_retention
BEFORE DELETE ON polar_paid_cycles
FOR EACH ROW EXECUTE FUNCTION enforce_polar_paid_cycle_retention();

CREATE TABLE polar_refunds (
    polar_refund_id text PRIMARY KEY,
    polar_order_id text NOT NULL REFERENCES polar_paid_cycles(polar_order_id)
        ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'canceled')),
    requested_micros bigint NOT NULL CHECK (requested_micros >= 0),
    reversed_micros bigint NOT NULL DEFAULT 0 CHECK (reversed_micros >= 0),
    chargeback boolean NOT NULL DEFAULT false,
    provider_event_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    CHECK (length(polar_refund_id) BETWEEN 1 AND 255),
    CHECK (reversed_micros <= requested_micros),
    CHECK (retain_until >= created_at + interval '7 years')
);

CREATE FUNCTION enforce_polar_refund_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.retain_until > transaction_timestamp() THEN
        RAISE EXCEPTION 'refund retention period has not elapsed'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER polar_refunds_enforce_retention
BEFORE DELETE ON polar_refunds
FOR EACH ROW EXECUTE FUNCTION enforce_polar_refund_retention();

CREATE TABLE billing_shortfalls (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    billing_period_id uuid NOT NULL,
    polar_refund_id text NOT NULL REFERENCES polar_refunds(polar_refund_id)
        ON DELETE CASCADE,
    source_reference text NOT NULL UNIQUE,
    amount_micros bigint NOT NULL CHECK (amount_micros > 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    FOREIGN KEY (organization_id, billing_period_id)
        REFERENCES billing_periods(organization_id, id) ON DELETE CASCADE,
    CHECK (is_uuid_v7(id)),
    CHECK (length(source_reference) BETWEEN 1 AND 255),
    CHECK (retain_until >= created_at + interval '7 years')
);

CREATE FUNCTION reject_billing_shortfall_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'billing shortfalls are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER billing_shortfalls_append_only
BEFORE UPDATE OR DELETE ON billing_shortfalls
FOR EACH ROW EXECUTE FUNCTION reject_billing_shortfall_mutation();

CREATE FUNCTION enforce_refund_shortfall_overage_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    period_start timestamptz;
    period_end timestamptz;
    period_limit numeric;
    committed_overage numeric;
    held_overage numeric;
    refund_shortfall numeric;
BEGIN
    IF NEW.held_overage_micros = 0 THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
    FOR UPDATE;

    SELECT
        period.starts_at,
        period.ends_at,
        period.requested_overage_limit_micros
    INTO period_start, period_end, period_limit
    FROM billing_periods AS period
    JOIN subscriptions AS subscription
      ON subscription.organization_id = period.organization_id
     AND subscription.id = period.subscription_id
    WHERE period.organization_id = NEW.organization_id
      AND period.starts_at <= statement_timestamp()
      AND period.ends_at > statement_timestamp()
      AND subscription.status = 'active'
      AND subscription.current_period_starts_at = period.starts_at
      AND subscription.current_period_ends_at = period.ends_at
    FOR SHARE OF period, subscription;

    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    SELECT COALESCE(sum(overage_applied_micros), 0)
    INTO committed_overage
    FROM usage_records
    WHERE organization_id = NEW.organization_id
      AND committed_at >= period_start
      AND committed_at < period_end;

    SELECT COALESCE(sum(held_overage_micros), 0)
    INTO held_overage
    FROM usage_reservations
    WHERE organization_id = NEW.organization_id
      AND status = 'held'
      AND created_at >= period_start
      AND created_at < period_end;

    SELECT COALESCE(sum(amount_micros), 0)
    INTO refund_shortfall
    FROM billing_shortfalls
    WHERE organization_id = NEW.organization_id
      AND billing_period_id = (
          SELECT id
          FROM billing_periods
          WHERE organization_id = NEW.organization_id
            AND starts_at = period_start
      );

    IF committed_overage + held_overage + refund_shortfall
       + NEW.held_overage_micros > period_limit THEN
        RAISE EXCEPTION 'reservation exceeds current overage limit after refund'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_refund_shortfall_capacity
BEFORE INSERT ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION enforce_refund_shortfall_overage_limit();

CREATE FUNCTION enforce_refund_shortfall_commit_capacity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    period_id uuid;
    period_start timestamptz;
    period_end timestamptz;
    period_limit numeric;
    committed_overage numeric;
    held_overage numeric;
    refund_shortfall numeric;
BEGIN
    IF NEW.overage_applied_micros = 0 THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
    FOR UPDATE;

    SELECT period.id, period.starts_at, period.ends_at,
           period.overage_limit_micros
    INTO period_id, period_start, period_end, period_limit
    FROM billing_periods AS period
    JOIN subscriptions AS subscription
      ON subscription.organization_id = period.organization_id
     AND subscription.id = period.subscription_id
    WHERE period.organization_id = NEW.organization_id
      AND period.starts_at <= NEW.committed_at
      AND period.ends_at > NEW.committed_at
      AND subscription.status = 'active'
      AND subscription.current_period_starts_at = period.starts_at
      AND subscription.current_period_ends_at = period.ends_at
    FOR SHARE OF period, subscription;

    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    SELECT COALESCE(sum(overage_applied_micros), 0)
    INTO committed_overage
    FROM usage_records
    WHERE organization_id = NEW.organization_id
      AND committed_at >= period_start
      AND committed_at < period_end;

    SELECT COALESCE(sum(held_overage_micros), 0)
    INTO held_overage
    FROM usage_reservations
    WHERE organization_id = NEW.organization_id
      AND status = 'held'
      AND created_at >= period_start
      AND created_at < period_end;

    SELECT COALESCE(sum(amount_micros), 0)
    INTO refund_shortfall
    FROM billing_shortfalls
    WHERE organization_id = NEW.organization_id
      AND billing_period_id = period_id;

    IF committed_overage + held_overage + refund_shortfall > period_limit THEN
        RAISE EXCEPTION 'commit exceeds current overage limit after refund'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_records_validate_shortfall_capacity
BEFORE INSERT ON usage_records
FOR EACH ROW EXECUTE FUNCTION enforce_refund_shortfall_commit_capacity();
