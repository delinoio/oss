-- Usage mutations keep the operational references needed while a reservation
-- is held and retain immutable, privacy-safe snapshots after finalization.
ALTER TABLE usage_reservations
    ADD COLUMN user_actor_reference_snapshot text NOT NULL
        DEFAULT 'actor:v1:00000000000000000000000000000000',
    ADD COLUMN service_name_snapshot text NOT NULL DEFAULT 'Unknown service',
    ADD COLUMN meter_name_snapshot text NOT NULL DEFAULT 'Unknown meter',
    ADD COLUMN polar_event_name_snapshot text NOT NULL DEFAULT 'unknown',
    ADD COLUMN price_effective_from_snapshot timestamptz NOT NULL DEFAULT '-infinity',
    ADD COLUMN price_effective_until_snapshot timestamptz,
    ADD COLUMN overage_billing_period_id uuid,
    ADD CONSTRAINT usage_reservations_user_actor_snapshot_check CHECK (
        user_actor_reference_snapshot ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    ADD CONSTRAINT usage_reservations_service_name_snapshot_check CHECK (
        length(service_name_snapshot) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT usage_reservations_meter_name_snapshot_check CHECK (
        length(meter_name_snapshot) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT usage_reservations_polar_event_snapshot_check CHECK (
        length(polar_event_name_snapshot) BETWEEN 1 AND 255
    ),
    ADD CONSTRAINT usage_reservations_price_window_snapshot_check CHECK (
        price_effective_until_snapshot IS NULL
        OR price_effective_until_snapshot > price_effective_from_snapshot
    );

-- Snapshot backfill is the only update that may bypass the existing
-- held-to-finalized transition guard. The trigger is re-enabled in this same
-- migration transaction before runtime writes resume.
ALTER TABLE usage_reservations
    DISABLE TRIGGER usage_reservations_enforce_transition;

-- Earlier migrations accepted arbitrary nonempty client references. Preserve a
-- stable per-reservation reference for legacy values that the service no
-- longer accepts before installing the stricter storage invariant.
UPDATE usage_reservations
SET client_reference = 'legacy:' || id::text
WHERE client_reference !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$';

UPDATE usage_reservations AS reservation
SET service_name_snapshot = service.name,
    meter_name_snapshot = meter.name,
    polar_event_name_snapshot = COALESCE((
        SELECT mapping.polar_meter_id
        FROM polar_meter_mappings AS mapping
        WHERE mapping.meter_id = reservation.meter_id
    ), 'unknown'),
    price_effective_from_snapshot = price.effective_from,
    price_effective_until_snapshot = price.effective_until,
    overage_billing_period_id = CASE
        WHEN reservation.held_overage_micros > 0 THEN (
            SELECT candidate.id
            FROM billing_periods AS candidate
            WHERE candidate.organization_id = reservation.organization_id
              AND candidate.starts_at <= reservation.created_at
              AND candidate.ends_at > reservation.created_at
            ORDER BY candidate.starts_at DESC
            LIMIT 1
        )
        ELSE NULL
    END
FROM service_identities AS service,
     catalog_meters AS meter,
     catalog_price_versions AS price
WHERE service.id = reservation.service_identity_id
  AND meter.id = reservation.meter_id
  AND price.id = reservation.price_version_id
  AND price.meter_id = reservation.meter_id;

ALTER TABLE usage_reservations
    ENABLE TRIGGER usage_reservations_enforce_transition;

ALTER TABLE usage_reservations
    ALTER COLUMN user_actor_reference_snapshot DROP DEFAULT;

ALTER TABLE usage_reservations
    ADD CONSTRAINT usage_reservations_client_reference_check CHECK (
        client_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$'
    ),
    ADD CONSTRAINT usage_reservations_overage_period_check CHECK (
        (held_overage_micros = 0 AND overage_billing_period_id IS NULL)
        OR
        (held_overage_micros > 0 AND overage_billing_period_id IS NOT NULL)
    );

ALTER TABLE usage_records
    ADD COLUMN price_version_id uuid,
    ADD COLUMN usd_micros_per_unit bigint,
    ADD COLUMN client_reference text,
    ADD COLUMN user_actor_reference_snapshot text,
    ADD COLUMN service_name_snapshot text,
    ADD COLUMN meter_name_snapshot text,
    ADD COLUMN polar_event_name_snapshot text,
    ADD COLUMN price_effective_from_snapshot timestamptz,
    ADD COLUMN price_effective_until_snapshot timestamptz,
    ADD COLUMN billing_period_id uuid,
    ADD COLUMN billing_period_starts_at_snapshot timestamptz,
    ADD COLUMN billing_period_ends_at_snapshot timestamptz;

ALTER TABLE usage_records
    DISABLE TRIGGER usage_records_immutable;

UPDATE usage_records AS usage
SET price_version_id = reservation.price_version_id,
    usd_micros_per_unit = reservation.usd_micros_per_unit,
    client_reference = reservation.client_reference,
    user_actor_reference_snapshot = reservation.user_actor_reference_snapshot,
    service_name_snapshot = reservation.service_name_snapshot,
    meter_name_snapshot = reservation.meter_name_snapshot,
    polar_event_name_snapshot = reservation.polar_event_name_snapshot,
    price_effective_from_snapshot = reservation.price_effective_from_snapshot,
    price_effective_until_snapshot = reservation.price_effective_until_snapshot,
    billing_period_id = (
        SELECT candidate.id
        FROM billing_periods AS candidate
        WHERE candidate.organization_id = usage.organization_id
          AND candidate.starts_at <= usage.committed_at
          AND candidate.ends_at > usage.committed_at
        ORDER BY candidate.starts_at DESC
        LIMIT 1
    ),
    billing_period_starts_at_snapshot = (
        SELECT candidate.starts_at
        FROM billing_periods AS candidate
        WHERE candidate.organization_id = usage.organization_id
          AND candidate.starts_at <= usage.committed_at
          AND candidate.ends_at > usage.committed_at
        ORDER BY candidate.starts_at DESC
        LIMIT 1
    ),
    billing_period_ends_at_snapshot = (
        SELECT candidate.ends_at
        FROM billing_periods AS candidate
        WHERE candidate.organization_id = usage.organization_id
          AND candidate.starts_at <= usage.committed_at
          AND candidate.ends_at > usage.committed_at
        ORDER BY candidate.starts_at DESC
        LIMIT 1
    )
FROM usage_reservations AS reservation
WHERE reservation.id = usage.reservation_id;

ALTER TABLE usage_records
    ENABLE TRIGGER usage_records_immutable;

ALTER TABLE usage_records
    ALTER COLUMN price_version_id SET NOT NULL,
    ALTER COLUMN usd_micros_per_unit SET NOT NULL,
    ALTER COLUMN client_reference SET NOT NULL,
    ALTER COLUMN user_actor_reference_snapshot SET NOT NULL,
    ALTER COLUMN service_name_snapshot SET NOT NULL,
    ALTER COLUMN meter_name_snapshot SET NOT NULL,
    ALTER COLUMN polar_event_name_snapshot SET NOT NULL,
    ALTER COLUMN price_effective_from_snapshot SET NOT NULL,
    ADD CONSTRAINT usage_records_price_version_snapshot_check CHECK (
        is_uuid_v7(price_version_id)
    ),
    ADD CONSTRAINT usage_records_unit_price_snapshot_check CHECK (
        usd_micros_per_unit >= 0
    ),
    ADD CONSTRAINT usage_records_client_reference_snapshot_check CHECK (
        client_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$'
    ),
    ADD CONSTRAINT usage_records_user_actor_snapshot_check CHECK (
        user_actor_reference_snapshot ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    ADD CONSTRAINT usage_records_service_name_snapshot_check CHECK (
        length(service_name_snapshot) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT usage_records_meter_name_snapshot_check CHECK (
        length(meter_name_snapshot) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT usage_records_polar_event_snapshot_check CHECK (
        length(polar_event_name_snapshot) BETWEEN 1 AND 255
    ),
    ADD CONSTRAINT usage_records_price_window_snapshot_check CHECK (
        price_effective_until_snapshot IS NULL
        OR price_effective_until_snapshot > price_effective_from_snapshot
    ),
    ADD CONSTRAINT usage_records_billing_period_snapshot_check CHECK (
        (
            billing_period_id IS NULL
            AND billing_period_starts_at_snapshot IS NULL
            AND billing_period_ends_at_snapshot IS NULL
        )
        OR
        (
            billing_period_id IS NOT NULL
            AND billing_period_starts_at_snapshot IS NOT NULL
            AND billing_period_ends_at_snapshot
                > billing_period_starts_at_snapshot
            AND committed_at >= billing_period_starts_at_snapshot
            AND committed_at < billing_period_ends_at_snapshot
        )
    );

-- Credit remains usable after a subscription's last billing period ends.
-- Only provider-billed overage settlement requires a containing period.
DO $$
DECLARE
    settlement_period_constraint name;
BEGIN
    SELECT candidate.conname
    INTO settlement_period_constraint
    FROM pg_constraint AS candidate
    WHERE candidate.conrelid = 'ledger_entries'::regclass
      AND candidate.contype = 'c'
      AND pg_get_constraintdef(candidate.oid) LIKE '%credit_commit%'
      AND pg_get_constraintdef(candidate.oid) LIKE '%overage_commit%'
      AND pg_get_constraintdef(candidate.oid) LIKE '%billing_period_id IS NOT NULL%'
    LIMIT 1;

    IF settlement_period_constraint IS NULL THEN
        RAISE EXCEPTION
            'ledger usage-settlement billing-period constraint is unavailable';
    END IF;

    EXECUTE format(
        'ALTER TABLE ledger_entries DROP CONSTRAINT %I',
        settlement_period_constraint
    );
END;
$$;

ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_overage_commit_period_check CHECK (
        entry_type <> 'overage_commit' OR billing_period_id IS NOT NULL
    );

-- Existing chargeable usage predates the deferred outbox invariant below.
-- Queue each missing event with the same immutable payload used by runtime
-- commits, then reject any pre-existing event that cannot satisfy that
-- invariant instead of silently retaining unreportable usage.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM usage_records AS usage
        JOIN usage_reservations AS reservation
          ON reservation.id = usage.reservation_id
        LEFT JOIN polar_meter_mappings AS mapping
          ON mapping.meter_id = reservation.meter_id
        WHERE usage.overage_applied_micros > 0
          AND mapping.meter_id IS NULL
    ) THEN
        RAISE EXCEPTION
            'legacy overage usage requires its retained Polar meter mapping'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

INSERT INTO integration_outbox (
    id,
    integration,
    operation,
    aggregate_type,
    aggregate_id,
    payload,
    created_at,
    idempotency_key,
    actor_reference
)
SELECT
    usage.id,
    'polar',
    'report_usage',
    'usage_record',
    usage.id,
    jsonb_build_object(
        'event_name', usage.polar_event_name_snapshot,
        'organization_id', usage.organization_id::text,
        'usage_record_id', usage.id::text,
        'units', usage.overage_applied_micros,
        'committed_at', usage.committed_at
    ),
    usage.committed_at,
    'usage-record:' || usage.id::text,
    usage.user_actor_reference_snapshot
FROM usage_records AS usage
WHERE usage.overage_applied_micros > 0
  AND NOT EXISTS (
      SELECT 1
      FROM integration_outbox AS outbox
      WHERE outbox.integration = 'polar'
        AND outbox.operation = 'report_usage'
        AND outbox.aggregate_type = 'usage_record'
        AND outbox.aggregate_id = usage.id
  );

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM usage_records AS usage
        WHERE usage.overage_applied_micros > 0
          AND (
              SELECT count(*)
              FROM integration_outbox AS outbox
              WHERE outbox.integration = 'polar'
                AND outbox.operation = 'report_usage'
                AND outbox.aggregate_type = 'usage_record'
                AND outbox.aggregate_id = usage.id
                AND outbox.payload -> 'organization_id'
                    = to_jsonb(usage.organization_id::text)
                AND outbox.payload -> 'usage_record_id'
                    = to_jsonb(usage.id::text)
                AND outbox.payload -> 'event_name'
                    = to_jsonb(usage.polar_event_name_snapshot)
                AND outbox.payload -> 'units'
                    = to_jsonb(usage.overage_applied_micros)
                AND CASE
                    WHEN jsonb_typeof(outbox.payload -> 'committed_at') = 'string'
                     AND outbox.payload ->> 'committed_at'
                         ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$'
                    THEN (outbox.payload ->> 'committed_at')::timestamptz
                         = usage.committed_at
                    ELSE false
                END
          ) <> 1
    ) THEN
        RAISE EXCEPTION
            'legacy overage usage requires exactly one matching Polar outbox event'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

CREATE UNIQUE INDEX usage_reservations_service_client_reference_idx
    ON usage_reservations(service_identity_id, client_reference);

CREATE UNIQUE INDEX integration_outbox_one_polar_usage_event_idx
    ON integration_outbox(integration, operation, aggregate_id)
    WHERE integration = 'polar' AND operation = 'report_usage';

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
        'reservation.expired',
        'settlement.recorded',
        'account.deletion_requested',
        'organization.deletion_requested',
        'webhook.received',
        'webhook.processed'
    ));

CREATE FUNCTION capture_usage_reservation_snapshots()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    selected_period_id uuid;
BEGIN
    NEW.created_at := statement_timestamp();

    SELECT service.name, meter.name, mapping.polar_meter_id,
           price.effective_from, price.effective_until
    INTO NEW.service_name_snapshot, NEW.meter_name_snapshot,
         NEW.polar_event_name_snapshot, NEW.price_effective_from_snapshot,
         NEW.price_effective_until_snapshot
    FROM service_identities AS service
    JOIN catalog_meters AS meter ON meter.id = NEW.meter_id
    JOIN polar_meter_mappings AS mapping ON mapping.meter_id = meter.id
    JOIN catalog_price_versions AS price
      ON price.meter_id = meter.id AND price.id = NEW.price_version_id
    WHERE service.id = NEW.service_identity_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation snapshots cannot be resolved'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.held_overage_micros > 0 THEN
        SELECT period.id
        INTO selected_period_id
        FROM billing_periods AS period
        JOIN subscriptions AS subscription
          ON subscription.organization_id = period.organization_id
         AND subscription.id = period.subscription_id
        WHERE period.organization_id = NEW.organization_id
          AND period.starts_at <= NEW.created_at
          AND period.ends_at > NEW.created_at
          AND subscription.status = 'active'
          AND subscription.current_period_starts_at = period.starts_at
          AND subscription.current_period_ends_at = period.ends_at
        FOR SHARE OF period, subscription;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'reservation has no current billing period'
                USING ERRCODE = 'check_violation';
        END IF;
        NEW.overage_billing_period_id := selected_period_id;
    ELSE
        NEW.overage_billing_period_id := NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_capture_snapshots
BEFORE INSERT ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION capture_usage_reservation_snapshots();

CREATE FUNCTION preserve_usage_reservation_snapshots()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.user_actor_reference_snapshot IS DISTINCT FROM OLD.user_actor_reference_snapshot
       OR NEW.service_name_snapshot IS DISTINCT FROM OLD.service_name_snapshot
       OR NEW.meter_name_snapshot IS DISTINCT FROM OLD.meter_name_snapshot
       OR NEW.polar_event_name_snapshot IS DISTINCT FROM OLD.polar_event_name_snapshot
       OR NEW.price_effective_from_snapshot IS DISTINCT FROM OLD.price_effective_from_snapshot
       OR NEW.price_effective_until_snapshot IS DISTINCT FROM OLD.price_effective_until_snapshot
       OR NEW.overage_billing_period_id IS DISTINCT FROM OLD.overage_billing_period_id THEN
        RAISE EXCEPTION 'usage reservation snapshots are immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_preserve_snapshots
BEFORE UPDATE ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION preserve_usage_reservation_snapshots();

CREATE FUNCTION validate_new_usage_reservation_capacity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    requested_limit numeric;
    committed_overage numeric;
    held_overage numeric;
    refund_shortfall numeric;
BEGIN
    IF NEW.held_overage_micros = 0 THEN
        RETURN NEW;
    END IF;

    SELECT requested_overage_limit_micros
    INTO requested_limit
    FROM billing_periods
    WHERE organization_id = NEW.organization_id
      AND id = NEW.overage_billing_period_id;

    SELECT COALESCE(sum(overage_applied_micros), 0)
    INTO committed_overage
    FROM usage_records
    WHERE organization_id = NEW.organization_id
      AND billing_period_id = NEW.overage_billing_period_id;

    SELECT COALESCE(sum(held_overage_micros), 0)
    INTO held_overage
    FROM usage_reservations
    WHERE organization_id = NEW.organization_id
      AND status = 'held'
      AND overage_billing_period_id = NEW.overage_billing_period_id;

    SELECT COALESCE(sum(amount_micros), 0)
    INTO refund_shortfall
    FROM billing_shortfalls
    WHERE organization_id = NEW.organization_id
      AND billing_period_id = NEW.overage_billing_period_id;

    IF committed_overage + held_overage + refund_shortfall > requested_limit THEN
        RAISE EXCEPTION 'reservation exceeds requested current overage limit'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_validate_current_capacity
AFTER INSERT ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION validate_new_usage_reservation_capacity();

CREATE FUNCTION capture_usage_record_snapshots()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    reservation usage_reservations%ROWTYPE;
    period billing_periods%ROWTYPE;
BEGIN
    SELECT *
    INTO reservation
    FROM usage_reservations
    WHERE id = NEW.reservation_id
      AND organization_id = NEW.organization_id
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usage record reservation snapshot is unavailable'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    NEW.price_version_id := reservation.price_version_id;
    NEW.usd_micros_per_unit := reservation.usd_micros_per_unit;
    NEW.client_reference := reservation.client_reference;
    NEW.user_actor_reference_snapshot := reservation.user_actor_reference_snapshot;
    NEW.service_name_snapshot := reservation.service_name_snapshot;
    NEW.meter_name_snapshot := reservation.meter_name_snapshot;
    NEW.polar_event_name_snapshot := reservation.polar_event_name_snapshot;
    NEW.price_effective_from_snapshot := reservation.price_effective_from_snapshot;
    NEW.price_effective_until_snapshot := reservation.price_effective_until_snapshot;
    NEW.committed_at := statement_timestamp();

    SELECT *
    INTO period
    FROM billing_periods AS candidate
    WHERE candidate.organization_id = NEW.organization_id
      AND candidate.starts_at <= NEW.committed_at
      AND candidate.ends_at > NEW.committed_at
      AND (
          reservation.overage_billing_period_id IS NULL
          OR candidate.id = reservation.overage_billing_period_id
      )
    ORDER BY candidate.starts_at DESC
    LIMIT 1
    FOR KEY SHARE;

    IF FOUND THEN
        NEW.billing_period_id := period.id;
        NEW.billing_period_starts_at_snapshot := period.starts_at;
        NEW.billing_period_ends_at_snapshot := period.ends_at;
    ELSE
        NEW.billing_period_id := NULL;
        NEW.billing_period_starts_at_snapshot := NULL;
        NEW.billing_period_ends_at_snapshot := NULL;
        IF NEW.overage_applied_micros > 0 THEN
            RAISE EXCEPTION 'usage record has no containing billing period'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_records_capture_snapshots
BEFORE INSERT ON usage_records
FOR EACH ROW EXECUTE FUNCTION capture_usage_record_snapshots();

CREATE FUNCTION validate_usage_record_current_capacity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    effective_limit numeric;
    committed_overage numeric;
    held_overage numeric;
    refund_shortfall numeric;
BEGIN
    IF NEW.overage_applied_micros = 0 THEN
        RETURN NEW;
    END IF;

    SELECT overage_limit_micros
    INTO effective_limit
    FROM billing_periods
    WHERE organization_id = NEW.organization_id
      AND id = NEW.billing_period_id;
    SELECT COALESCE(sum(overage_applied_micros), 0)
    INTO committed_overage
    FROM usage_records
    WHERE organization_id = NEW.organization_id
      AND billing_period_id = NEW.billing_period_id;
    SELECT COALESCE(sum(held_overage_micros), 0)
    INTO held_overage
    FROM usage_reservations
    WHERE organization_id = NEW.organization_id
      AND status = 'held'
      AND overage_billing_period_id = NEW.billing_period_id;
    SELECT COALESCE(sum(amount_micros), 0)
    INTO refund_shortfall
    FROM billing_shortfalls
    WHERE organization_id = NEW.organization_id
      AND billing_period_id = NEW.billing_period_id;

    -- The newly inserted usage is present in committed_overage while its
    -- reservation is still held. Subtract the transferring amount once so the
    -- same capacity is not double-counted during commit.
    IF committed_overage + held_overage
       - NEW.overage_applied_micros + refund_shortfall > effective_limit THEN
        RAISE EXCEPTION 'reservation overage capacity is no longer available'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_records_validate_current_capacity
AFTER INSERT ON usage_records
FOR EACH ROW EXECUTE FUNCTION validate_usage_record_current_capacity();

CREATE FUNCTION require_usage_record_polar_outbox()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.overage_applied_micros > 0
       AND NOT EXISTS (
           SELECT 1
           FROM integration_outbox AS outbox
           WHERE outbox.integration = 'polar'
             AND outbox.operation = 'report_usage'
             AND outbox.aggregate_type = 'usage_record'
             AND outbox.aggregate_id = NEW.id
             AND outbox.payload -> 'organization_id'
                 = to_jsonb(NEW.organization_id::text)
             AND outbox.payload -> 'usage_record_id'
                 = to_jsonb(NEW.id::text)
             AND outbox.payload -> 'event_name'
                 = to_jsonb(NEW.polar_event_name_snapshot)
             AND outbox.payload -> 'units'
                 = to_jsonb(NEW.overage_applied_micros)
             AND jsonb_typeof(outbox.payload -> 'committed_at') = 'string'
             AND outbox.payload ->> 'committed_at'
                 ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$'
             AND (outbox.payload ->> 'committed_at')::timestamptz
                 = NEW.committed_at
       ) THEN
        RAISE EXCEPTION
            'overage usage record requires a matching Polar outbox event'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER usage_records_require_polar_outbox
AFTER INSERT ON usage_records
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_usage_record_polar_outbox();

CREATE FUNCTION validate_polar_usage_outbox_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.integration = 'polar'
       AND NEW.operation = 'report_usage'
       AND (
           NEW.aggregate_type <> 'usage_record'
           OR NOT EXISTS (
               SELECT 1
               FROM usage_records AS record
               WHERE record.id = NEW.aggregate_id
                 AND record.overage_applied_micros > 0
                 AND NEW.payload -> 'organization_id'
                     = to_jsonb(record.organization_id::text)
                 AND NEW.payload -> 'usage_record_id'
                     = to_jsonb(record.id::text)
                 AND NEW.payload -> 'event_name'
                     = to_jsonb(record.polar_event_name_snapshot)
                 AND NEW.payload -> 'units'
                     = to_jsonb(record.overage_applied_micros)
                 AND jsonb_typeof(NEW.payload -> 'committed_at') = 'string'
                 AND NEW.payload ->> 'committed_at'
                     ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$'
                 AND (NEW.payload ->> 'committed_at')::timestamptz
                     = record.committed_at
           )
       ) THEN
        RAISE EXCEPTION
            'Polar usage outbox requires a matching overage usage record'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER integration_outbox_validate_polar_usage_record
AFTER INSERT ON integration_outbox
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_polar_usage_outbox_record();
