CREATE TABLE polar_customers (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    polar_customer_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_customer_id) BETWEEN 1 AND 255)
);

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    polar_subscription_id text NOT NULL UNIQUE,
    status text NOT NULL
        CHECK (status IN ('pending', 'active', 'past_due', 'canceled', 'revoked')),
    current_period_starts_at timestamptz,
    current_period_ends_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id),
    UNIQUE (organization_id, id),
    CHECK (is_uuid_v7(id)),
    CHECK (
        current_period_starts_at IS NULL
        OR current_period_ends_at IS NULL
        OR current_period_ends_at > current_period_starts_at
    )
);

CREATE TABLE billing_periods (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subscription_id uuid,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    overage_limit_micros bigint NOT NULL DEFAULT 0 CHECK (overage_limit_micros >= 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id, starts_at),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, subscription_id)
        REFERENCES subscriptions(organization_id, id)
        ON DELETE SET NULL (subscription_id),
    CHECK (is_uuid_v7(id)),
    CHECK (ends_at > starts_at),
    EXCLUDE USING gist (
        organization_id WITH =,
        tstzrange(starts_at, ends_at, '[)') WITH &&
    )
);

CREATE TABLE ledger_entries (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    billing_period_id uuid,
    entry_type text NOT NULL
        CHECK (entry_type IN (
            'credit_grant', 'credit_reversal', 'credit_hold',
            'credit_commit', 'credit_release', 'overage_hold',
            'overage_commit', 'overage_release', 'credit_forfeiture'
        )),
    amount_micros bigint NOT NULL CHECK (amount_micros <> 0),
    balance_after_micros bigint NOT NULL,
    reservation_id uuid,
    usage_record_id uuid,
    team_id_snapshot uuid,
    team_name_snapshot text,
    source_reference text NOT NULL,
    actor_reference text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id, entry_type, source_reference),
    CHECK (is_uuid_v7(id)),
    CHECK (billing_period_id IS NULL OR is_uuid_v7(billing_period_id)),
    CHECK (reservation_id IS NULL OR is_uuid_v7(reservation_id)),
    CHECK (usage_record_id IS NULL OR is_uuid_v7(usage_record_id)),
    CHECK (team_id_snapshot IS NULL OR is_uuid_v7(team_id_snapshot)),
    CHECK (
        (team_id_snapshot IS NULL AND team_name_snapshot IS NULL)
        OR
        (
            team_id_snapshot IS NOT NULL
            AND team_name_snapshot IS NOT NULL
            AND length(team_name_snapshot) BETWEEN 1 AND 120
        )
    ),
    CHECK (usage_record_id IS NULL OR reservation_id IS NOT NULL),
    CHECK (
        reservation_id IS NULL
        OR (team_id_snapshot IS NOT NULL AND team_name_snapshot IS NOT NULL)
    )
);

CREATE INDEX ledger_entries_organization_idx
    ON ledger_entries(organization_id, created_at, id);
CREATE INDEX ledger_entries_reservation_idx
    ON ledger_entries(reservation_id) WHERE reservation_id IS NOT NULL;
CREATE INDEX ledger_entries_usage_record_idx
    ON ledger_entries(usage_record_id) WHERE usage_record_id IS NOT NULL;

CREATE FUNCTION reject_ledger_entry_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'ledger entries are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER ledger_entries_append_only
BEFORE UPDATE OR DELETE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION reject_ledger_entry_mutation();

CREATE TABLE usage_reservations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    team_id uuid NOT NULL,
    team_name_snapshot text NOT NULL,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE RESTRICT,
    price_version_id uuid NOT NULL,
    account_id uuid NOT NULL,
    service_identity_id uuid NOT NULL REFERENCES service_identities(id) ON DELETE RESTRICT,
    maximum_units bigint NOT NULL CHECK (maximum_units > 0),
    usd_micros_per_unit bigint NOT NULL CHECK (usd_micros_per_unit >= 0),
    maximum_cost_micros bigint NOT NULL CHECK (maximum_cost_micros >= 0),
    held_credit_micros bigint NOT NULL CHECK (held_credit_micros >= 0),
    held_overage_micros bigint NOT NULL CHECK (held_overage_micros >= 0),
    client_reference text NOT NULL,
    status text NOT NULL DEFAULT 'held'
        CHECK (status IN ('held', 'committed', 'released', 'expired')),
    active_team_id uuid GENERATED ALWAYS AS (
        CASE WHEN status = 'held' THEN team_id ELSE NULL END
    ) STORED,
    active_account_id uuid GENERATED ALWAYS AS (
        CASE WHEN status = 'held' THEN account_id ELSE NULL END
    ) STORED,
    active_service_identity_id uuid GENERATED ALWAYS AS (
        CASE WHEN status = 'held' THEN service_identity_id ELSE NULL END
    ) STORED,
    active_meter_id uuid GENERATED ALWAYS AS (
        CASE WHEN status = 'held' THEN meter_id ELSE NULL END
    ) STORED,
    expires_at timestamptz NOT NULL,
    finalized_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (
        id,
        organization_id,
        team_id,
        meter_id,
        account_id,
        service_identity_id
    ),
    FOREIGN KEY (organization_id, active_team_id)
        REFERENCES teams(organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (active_account_id)
        REFERENCES accounts(id) ON DELETE RESTRICT,
    FOREIGN KEY (active_service_identity_id, active_meter_id)
        REFERENCES service_meter_allowlists(service_identity_id, meter_id)
        ON DELETE RESTRICT,
    FOREIGN KEY (meter_id, price_version_id)
        REFERENCES catalog_price_versions(meter_id, id) ON DELETE RESTRICT,
    CHECK (is_uuid_v7(id)),
    CHECK (
        maximum_cost_micros::numeric
        = maximum_units::numeric * usd_micros_per_unit::numeric
    ),
    CHECK (held_credit_micros + held_overage_micros = maximum_cost_micros),
    CHECK (
        (status = 'held' AND finalized_at IS NULL)
        OR
        (status <> 'held' AND finalized_at IS NOT NULL)
    )
);

CREATE FUNCTION validate_usage_reservation_references()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM 1
    FROM teams
    WHERE organization_id = NEW.organization_id
      AND id = NEW.team_id
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation team does not belong to organization'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF NEW.status = 'held' THEN
        PERFORM 1
        FROM service_meter_allowlists
        WHERE service_identity_id = NEW.service_identity_id
          AND meter_id = NEW.meter_id
          AND enabled
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'reservation service is not allowed for meter'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_validate_references
BEFORE INSERT OR UPDATE OF
    organization_id, team_id, service_identity_id, meter_id, status
ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION validate_usage_reservation_references();

CREATE INDEX usage_reservations_active_org_idx
    ON usage_reservations(organization_id, expires_at) WHERE status = 'held';
CREATE INDEX usage_reservations_active_team_idx
    ON usage_reservations(team_id, expires_at) WHERE status = 'held';

CREATE TABLE usage_records (
    id uuid PRIMARY KEY,
    reservation_id uuid NOT NULL UNIQUE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    team_id uuid NOT NULL,
    team_name_snapshot text NOT NULL,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL,
    service_identity_id uuid NOT NULL REFERENCES service_identities(id) ON DELETE RESTRICT,
    committed_units bigint NOT NULL CHECK (committed_units >= 0),
    total_cost_micros bigint NOT NULL CHECK (total_cost_micros >= 0),
    credit_applied_micros bigint NOT NULL CHECK (credit_applied_micros >= 0),
    overage_applied_micros bigint NOT NULL CHECK (overage_applied_micros >= 0),
    committed_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    FOREIGN KEY (
        reservation_id,
        organization_id,
        team_id,
        meter_id,
        account_id,
        service_identity_id
    ) REFERENCES usage_reservations(
        id,
        organization_id,
        team_id,
        meter_id,
        account_id,
        service_identity_id
    ) ON DELETE RESTRICT,
    CHECK (is_uuid_v7(id)),
    CHECK (credit_applied_micros + overage_applied_micros = total_cost_micros)
);

CREATE INDEX usage_records_organization_idx
    ON usage_records(organization_id, committed_at, id);

CREATE FUNCTION commit_usage_record_reservation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    reservation usage_reservations%ROWTYPE;
BEGIN
    SELECT *
    INTO reservation
    FROM usage_reservations
    WHERE id = NEW.reservation_id
      AND organization_id = NEW.organization_id
      AND team_id = NEW.team_id
      AND meter_id = NEW.meter_id
      AND account_id = NEW.account_id
      AND service_identity_id = NEW.service_identity_id
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'usage record does not match reservation'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF reservation.status <> 'held' THEN
        RAISE EXCEPTION 'reservation is already finalized'
            USING ERRCODE = 'check_violation';
    END IF;
    IF reservation.expires_at <= transaction_timestamp() THEN
        RAISE EXCEPTION 'reservation has expired'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.committed_units > reservation.maximum_units THEN
        RAISE EXCEPTION 'committed usage exceeds reservation maximum'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.total_cost_micros::numeric
       <> NEW.committed_units::numeric * reservation.usd_micros_per_unit::numeric THEN
        RAISE EXCEPTION 'committed usage cost does not match reservation price'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE usage_reservations
    SET status = 'committed',
        finalized_at = transaction_timestamp()
    WHERE id = reservation.id;

    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_records_commit_reservation
BEFORE INSERT
ON usage_records
FOR EACH ROW EXECUTE FUNCTION commit_usage_record_reservation();

CREATE FUNCTION reject_usage_record_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'usage records are immutable'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER usage_records_immutable
BEFORE UPDATE OR DELETE ON usage_records
FOR EACH ROW EXECUTE FUNCTION reject_usage_record_mutation();

CREATE FUNCTION validate_ledger_entry_links()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    linked_reservation_id uuid;
    linked_team_id uuid;
    linked_team_name text;
BEGIN
    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'ledger organization does not exist'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.billing_period_id IS NOT NULL THEN
        PERFORM 1
        FROM billing_periods
        WHERE id = NEW.billing_period_id
          AND organization_id = NEW.organization_id
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'ledger billing period does not belong to organization'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
    END IF;

    IF NEW.reservation_id IS NOT NULL THEN
        SELECT team_id, team_name_snapshot
        INTO linked_team_id, linked_team_name
        FROM usage_reservations
        WHERE id = NEW.reservation_id
          AND organization_id = NEW.organization_id
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'ledger reservation does not belong to organization'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        IF NEW.team_id_snapshot IS DISTINCT FROM linked_team_id
           OR NEW.team_name_snapshot IS DISTINCT FROM linked_team_name THEN
            RAISE EXCEPTION 'ledger team snapshot does not match reservation'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.usage_record_id IS NOT NULL THEN
        SELECT reservation_id, team_id, team_name_snapshot
        INTO linked_reservation_id, linked_team_id, linked_team_name
        FROM usage_records
        WHERE id = NEW.usage_record_id
          AND organization_id = NEW.organization_id
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'ledger usage record does not belong to organization'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        IF NEW.reservation_id IS DISTINCT FROM linked_reservation_id
           OR NEW.team_id_snapshot IS DISTINCT FROM linked_team_id
           OR NEW.team_name_snapshot IS DISTINCT FROM linked_team_name THEN
            RAISE EXCEPTION 'ledger links do not match usage record'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER ledger_entries_validate_links
BEFORE INSERT ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION validate_ledger_entry_links();
