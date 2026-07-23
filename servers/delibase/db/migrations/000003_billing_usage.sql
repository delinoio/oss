CREATE TABLE polar_customers (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    polar_customer_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_customer_id) BETWEEN 1 AND 255)
);

CREATE FUNCTION preserve_polar_customer_identifier()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.polar_customer_id IS DISTINCT FROM OLD.polar_customer_id THEN
        RAISE EXCEPTION 'Polar customer identifier is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER polar_customers_preserve_identifier
BEFORE UPDATE OF polar_customer_id ON polar_customers
FOR EACH ROW EXECUTE FUNCTION preserve_polar_customer_identifier();

CREATE FUNCTION require_organization_polar_customer()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM organizations
        WHERE id = NEW.id
    ) AND NOT EXISTS (
        SELECT 1
        FROM polar_customers
        WHERE organization_id = NEW.id
    ) THEN
        RAISE EXCEPTION 'organization must have a Polar customer'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER organizations_require_polar_customer
AFTER INSERT ON organizations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_organization_polar_customer();

CREATE FUNCTION preserve_organization_polar_customer()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM organizations
        WHERE id = OLD.organization_id
    ) AND NOT EXISTS (
        SELECT 1
        FROM polar_customers
        WHERE organization_id = OLD.organization_id
    ) THEN
        RAISE EXCEPTION 'organization must retain its Polar customer'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER polar_customers_preserve_organization_customer
AFTER DELETE OR UPDATE OF organization_id ON polar_customers
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION preserve_organization_polar_customer();

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
    UNIQUE (organization_id, id),
    CHECK (is_uuid_v7(id)),
    CHECK (
        current_period_starts_at IS NULL
        OR current_period_ends_at IS NULL
        OR current_period_ends_at > current_period_starts_at
    )
);

CREATE UNIQUE INDEX subscriptions_one_active_per_organization_idx
    ON subscriptions(organization_id)
    WHERE status = 'active';

CREATE FUNCTION preserve_polar_subscription_identifier()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.polar_subscription_id IS DISTINCT FROM OLD.polar_subscription_id THEN
        RAISE EXCEPTION 'Polar subscription identifier is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER subscriptions_preserve_polar_identifier
BEFORE UPDATE OF polar_subscription_id ON subscriptions
FOR EACH ROW EXECUTE FUNCTION preserve_polar_subscription_identifier();

CREATE FUNCTION preserve_terminal_subscription()
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

    IF OLD.status IN ('canceled', 'revoked') THEN
        RAISE EXCEPTION 'terminal subscription history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER subscriptions_preserve_terminal_history
BEFORE UPDATE OR DELETE ON subscriptions
FOR EACH ROW EXECUTE FUNCTION preserve_terminal_subscription();

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
    billing_period_starts_at_snapshot timestamptz,
    billing_period_ends_at_snapshot timestamptz,
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
    CHECK (
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
        )
    ),
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
        (
            entry_type IN ('credit_grant', 'credit_release', 'overage_release')
            AND amount_micros > 0
        )
        OR
        (
            entry_type IN (
                'credit_reversal', 'credit_hold', 'credit_commit',
                'overage_hold', 'overage_commit', 'credit_forfeiture'
            )
            AND amount_micros < 0
        )
    ),
    CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    CHECK (
        reservation_id IS NULL
        OR (team_id_snapshot IS NOT NULL AND team_name_snapshot IS NOT NULL)
    ),
    CHECK (
        entry_type NOT IN (
            'credit_hold',
            'credit_release',
            'overage_hold',
            'overage_release'
        )
        OR reservation_id IS NOT NULL
    ),
    CHECK (
        entry_type NOT IN ('credit_commit', 'overage_commit')
        OR usage_record_id IS NOT NULL
    ),
    CHECK (
        entry_type NOT IN ('credit_commit', 'overage_commit')
        OR billing_period_id IS NOT NULL
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
    organization_id uuid NOT NULL,
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
    active_organization_id uuid GENERATED ALWAYS AS (
        CASE WHEN status = 'held' THEN organization_id ELSE NULL END
    ) STORED,
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
    FOREIGN KEY (active_organization_id)
        REFERENCES organizations(id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, active_team_id)
        REFERENCES teams(organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (active_organization_id, active_account_id)
        REFERENCES organization_memberships(organization_id, account_id)
        ON DELETE RESTRICT,
    FOREIGN KEY (active_service_identity_id, active_meter_id)
        REFERENCES service_meter_allowlists(service_identity_id, meter_id)
        ON DELETE RESTRICT,
    FOREIGN KEY (active_meter_id)
        REFERENCES polar_meter_mappings(meter_id) ON DELETE RESTRICT,
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
DECLARE
    locked_team_name text;
    locked_organization_role text;
    locked_access_team_id uuid;
    locked_price_micros bigint;
    price_effective_from timestamptz;
    price_effective_until timestamptz;
    reservation_ttl_seconds bigint;
    settled_credit_micros numeric;
    active_credit_micros numeric;
    available_credit_micros numeric;
    expected_credit_micros numeric;
    expected_overage_micros numeric;
    period_starts_at timestamptz;
    period_ends_at timestamptz;
    period_overage_limit_micros numeric;
    committed_overage_micros numeric;
    active_overage_micros numeric;
BEGIN
    IF NEW.status <> 'held' THEN
        RAISE EXCEPTION 'usage reservations must be inserted as held'
            USING ERRCODE = 'check_violation';
    END IF;
    NEW.created_at := transaction_timestamp();

    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
      AND deleted_at IS NULL
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation organization does not exist'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    SELECT name
    INTO locked_team_name
    FROM teams
    WHERE organization_id = NEW.organization_id
      AND id = NEW.team_id
    FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation team does not belong to organization'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF NEW.team_name_snapshot <> locked_team_name THEN
        RAISE EXCEPTION 'reservation team snapshot does not match team'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT
        price.usd_micros_per_unit,
        price.effective_from,
        price.effective_until,
        meter.reservation_ttl_seconds
    INTO
        locked_price_micros,
        price_effective_from,
        price_effective_until,
        reservation_ttl_seconds
    FROM catalog_price_versions AS price
    JOIN catalog_meters AS meter ON meter.id = price.meter_id
    WHERE price.meter_id = NEW.meter_id
      AND price.id = NEW.price_version_id
    FOR SHARE OF price, meter;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation price does not belong to meter'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF NEW.usd_micros_per_unit <> locked_price_micros THEN
        RAISE EXCEPTION 'reservation price snapshot does not match catalog'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.created_at < price_effective_from
       OR (
           price_effective_until IS NOT NULL
           AND NEW.created_at >= price_effective_until
       ) THEN
        RAISE EXCEPTION 'reservation price is not effective at creation'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.expires_at
       <> NEW.created_at + reservation_ttl_seconds * interval '1 second' THEN
        RAISE EXCEPTION 'reservation expiry does not match meter TTL'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.status = 'held' THEN
        SELECT membership.role
        INTO locked_organization_role
        FROM organization_memberships AS membership
        JOIN accounts AS account ON account.id = membership.account_id
        WHERE membership.organization_id = NEW.organization_id
          AND membership.account_id = NEW.account_id
          AND account.status = 'active'
        FOR SHARE OF membership, account;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'reservation account cannot access team'
                USING ERRCODE = 'check_violation';
        END IF;

        IF locked_organization_role NOT IN ('owner', 'admin') THEN
            WITH RECURSIVE team_and_ancestors AS (
                SELECT team.id, team.parent_team_id
                FROM teams AS team
                WHERE team.organization_id = NEW.organization_id
                  AND team.id = NEW.team_id

                UNION ALL

                SELECT parent.id, parent.parent_team_id
                FROM teams AS parent
                JOIN team_and_ancestors AS child
                  ON child.parent_team_id = parent.id
                WHERE parent.organization_id = NEW.organization_id
            )
            SELECT team_membership.team_id
            INTO locked_access_team_id
            FROM team_memberships AS team_membership
            JOIN team_and_ancestors AS allowed_team
              ON allowed_team.id = team_membership.team_id
            WHERE team_membership.organization_id = NEW.organization_id
              AND team_membership.account_id = NEW.account_id
            ORDER BY team_membership.team_id
            LIMIT 1
            FOR SHARE OF team_membership;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'reservation account cannot access team'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        PERFORM 1
        FROM service_meter_allowlists AS allowlist
        JOIN service_identities AS service
          ON service.id = allowlist.service_identity_id
        JOIN catalog_meters AS meter ON meter.id = allowlist.meter_id
        JOIN catalog_apps AS app ON app.id = meter.app_id
        WHERE allowlist.service_identity_id = NEW.service_identity_id
          AND allowlist.meter_id = NEW.meter_id
          AND allowlist.enabled
          AND service.enabled
          AND meter.enabled
          AND app.enabled
        FOR SHARE OF allowlist, service, meter, app;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'reservation service is not allowed for meter'
                USING ERRCODE = 'foreign_key_violation';
        END IF;

        SELECT COALESCE(sum(amount_micros), 0)
        INTO settled_credit_micros
        FROM ledger_entries
        WHERE organization_id = NEW.organization_id
          AND entry_type IN (
              'credit_grant',
              'credit_reversal',
              'credit_commit',
              'credit_forfeiture'
          );
        SELECT COALESCE(sum(held_credit_micros), 0)
        INTO active_credit_micros
        FROM usage_reservations
        WHERE organization_id = NEW.organization_id
          AND status = 'held';

        available_credit_micros := GREATEST(
            settled_credit_micros - active_credit_micros,
            0
        );
        expected_credit_micros := LEAST(
            NEW.maximum_cost_micros::numeric,
            available_credit_micros
        );
        expected_overage_micros :=
            NEW.maximum_cost_micros::numeric - expected_credit_micros;
        IF NEW.held_credit_micros::numeric <> expected_credit_micros
           OR NEW.held_overage_micros::numeric <> expected_overage_micros THEN
            RAISE EXCEPTION 'reservation hold split does not match available credit'
                USING ERRCODE = 'check_violation';
        END IF;

        IF expected_overage_micros > 0 THEN
            SELECT period.starts_at, period.ends_at, period.overage_limit_micros
            INTO period_starts_at, period_ends_at, period_overage_limit_micros
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
            IF NEW.expires_at > period_ends_at THEN
                RAISE EXCEPTION 'overage reservation cannot outlive billing period'
                    USING ERRCODE = 'check_violation';
            END IF;

            SELECT COALESCE(sum(overage_applied_micros), 0)
            INTO committed_overage_micros
            FROM usage_records
            WHERE organization_id = NEW.organization_id
              AND committed_at >= period_starts_at
              AND committed_at < period_ends_at;
            SELECT COALESCE(sum(held_overage_micros), 0)
            INTO active_overage_micros
            FROM usage_reservations
            WHERE organization_id = NEW.organization_id
              AND status = 'held'
              AND created_at >= period_starts_at
              AND created_at < period_ends_at;

            IF committed_overage_micros
               + active_overage_micros
               + expected_overage_micros
               > period_overage_limit_micros THEN
                RAISE EXCEPTION 'reservation exceeds current overage limit'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_validate_references
BEFORE INSERT
ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION validate_usage_reservation_references();

CREATE FUNCTION enforce_usage_reservation_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.finalized_at := statement_timestamp();
    IF OLD.status <> 'held'
       OR NEW.status NOT IN ('committed', 'released', 'expired')
       OR (
           NEW.status = 'expired'
           AND OLD.expires_at > statement_timestamp()
       )
       OR (
           NEW.status = 'committed'
           AND NOT EXISTS (
               SELECT 1
               FROM usage_records AS usage
               WHERE usage.reservation_id = OLD.id
                 AND COALESCE((
                     SELECT -sum(entry.amount_micros)
                     FROM ledger_entries AS entry
                     WHERE entry.usage_record_id = usage.id
                       AND entry.entry_type = 'credit_commit'
                 ), 0) = usage.credit_applied_micros::numeric
                 AND COALESCE((
                     SELECT -sum(entry.amount_micros)
                     FROM ledger_entries AS entry
                     WHERE entry.usage_record_id = usage.id
                       AND entry.entry_type = 'overage_commit'
                 ), 0) = usage.overage_applied_micros::numeric
                 AND COALESCE((
                     SELECT sum(entry.amount_micros)
                     FROM ledger_entries AS entry
                     WHERE entry.reservation_id = OLD.id
                       AND entry.entry_type = 'credit_release'
                 ), 0) = (
                     OLD.held_credit_micros - usage.credit_applied_micros
                 )::numeric
                 AND COALESCE((
                     SELECT sum(entry.amount_micros)
                     FROM ledger_entries AS entry
                     WHERE entry.reservation_id = OLD.id
                       AND entry.entry_type = 'overage_release'
                 ), 0) = (
                     OLD.held_overage_micros - usage.overage_applied_micros
                 )::numeric
           )
       )
       OR (
           NEW.status IN ('released', 'expired')
           AND (
               COALESCE(-(
                   SELECT sum(entry.amount_micros)
                   FROM ledger_entries AS entry
                   WHERE entry.reservation_id = OLD.id
                     AND entry.entry_type = 'credit_hold'
               ), 0) <> OLD.held_credit_micros::numeric
               OR COALESCE(-(
                   SELECT sum(entry.amount_micros)
                   FROM ledger_entries AS entry
                   WHERE entry.reservation_id = OLD.id
                     AND entry.entry_type = 'overage_hold'
               ), 0) <> OLD.held_overage_micros::numeric
               OR COALESCE((
                   SELECT sum(entry.amount_micros)
                   FROM ledger_entries AS entry
                   WHERE entry.reservation_id = OLD.id
                     AND entry.entry_type = 'credit_release'
               ), 0) <> OLD.held_credit_micros::numeric
               OR COALESCE((
                   SELECT sum(entry.amount_micros)
                   FROM ledger_entries AS entry
                   WHERE entry.reservation_id = OLD.id
                     AND entry.entry_type = 'overage_release'
               ), 0) <> OLD.held_overage_micros::numeric
           )
       )
       OR NEW.id IS DISTINCT FROM OLD.id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.team_name_snapshot IS DISTINCT FROM OLD.team_name_snapshot
       OR NEW.meter_id IS DISTINCT FROM OLD.meter_id
       OR NEW.price_version_id IS DISTINCT FROM OLD.price_version_id
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.service_identity_id IS DISTINCT FROM OLD.service_identity_id
       OR NEW.maximum_units IS DISTINCT FROM OLD.maximum_units
       OR NEW.usd_micros_per_unit IS DISTINCT FROM OLD.usd_micros_per_unit
       OR NEW.maximum_cost_micros IS DISTINCT FROM OLD.maximum_cost_micros
       OR NEW.held_credit_micros IS DISTINCT FROM OLD.held_credit_micros
       OR NEW.held_overage_micros IS DISTINCT FROM OLD.held_overage_micros
       OR NEW.client_reference IS DISTINCT FROM OLD.client_reference
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'usage reservation transition is invalid'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_enforce_transition
BEFORE UPDATE ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION enforce_usage_reservation_transition();

CREATE FUNCTION reject_usage_reservation_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'usage reservations are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER usage_reservations_reject_delete
BEFORE DELETE ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION reject_usage_reservation_delete();

CREATE INDEX usage_reservations_active_org_idx
    ON usage_reservations(organization_id, expires_at) WHERE status = 'held';
CREATE INDEX usage_reservations_active_team_idx
    ON usage_reservations(team_id, expires_at) WHERE status = 'held';

CREATE TABLE usage_records (
    id uuid PRIMARY KEY,
    reservation_id uuid NOT NULL UNIQUE,
    organization_id uuid NOT NULL,
    team_id uuid NOT NULL,
    team_name_snapshot text NOT NULL,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL,
    service_identity_id uuid NOT NULL REFERENCES service_identities(id) ON DELETE RESTRICT,
    committed_units bigint NOT NULL CHECK (committed_units >= 0),
    total_cost_micros bigint NOT NULL CHECK (total_cost_micros >= 0),
    credit_applied_micros bigint NOT NULL CHECK (credit_applied_micros >= 0),
    overage_applied_micros bigint NOT NULL CHECK (overage_applied_micros >= 0),
    committed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
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

CREATE FUNCTION validate_usage_record_reservation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    reservation usage_reservations%ROWTYPE;
    locked_organization_role text;
    locked_access_team_id uuid;
    expected_credit_micros bigint;
    expected_overage_micros bigint;
    settled_credit_micros numeric;
    active_credit_micros numeric;
    period_starts_at timestamptz;
    period_ends_at timestamptz;
    period_overage_limit_micros numeric;
    committed_overage_micros numeric;
    active_overage_micros numeric;
BEGIN
    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
      AND deleted_at IS NULL
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usage record organization is unavailable'
            USING ERRCODE = 'check_violation';
    END IF;

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
    SELECT membership.role
    INTO locked_organization_role
    FROM organization_memberships AS membership
    JOIN accounts AS account ON account.id = membership.account_id
    WHERE membership.organization_id = NEW.organization_id
      AND membership.account_id = NEW.account_id
      AND account.status = 'active'
    FOR SHARE OF membership, account;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usage record account is unavailable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF locked_organization_role NOT IN ('owner', 'admin') THEN
        WITH RECURSIVE team_and_ancestors AS (
            SELECT team.id, team.parent_team_id
            FROM teams AS team
            WHERE team.organization_id = NEW.organization_id
              AND team.id = NEW.team_id

            UNION ALL

            SELECT parent.id, parent.parent_team_id
            FROM teams AS parent
            JOIN team_and_ancestors AS child
              ON child.parent_team_id = parent.id
            WHERE parent.organization_id = NEW.organization_id
        )
        SELECT team_membership.team_id
        INTO locked_access_team_id
        FROM team_memberships AS team_membership
        JOIN team_and_ancestors AS allowed_team
          ON allowed_team.id = team_membership.team_id
        WHERE team_membership.organization_id = NEW.organization_id
          AND team_membership.account_id = NEW.account_id
        ORDER BY team_membership.team_id
        LIMIT 1
        FOR SHARE OF team_membership;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'usage record account cannot access team'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    IF reservation.status <> 'held' THEN
        RAISE EXCEPTION 'reservation is already finalized'
            USING ERRCODE = 'check_violation';
    END IF;
    IF reservation.expires_at <= statement_timestamp() THEN
        RAISE EXCEPTION 'reservation has expired'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.committed_units > reservation.maximum_units THEN
        RAISE EXCEPTION 'committed usage exceeds reservation maximum'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.team_name_snapshot <> reservation.team_name_snapshot THEN
        RAISE EXCEPTION 'usage team snapshot does not match reservation'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.total_cost_micros::numeric
       <> NEW.committed_units::numeric * reservation.usd_micros_per_unit::numeric THEN
        RAISE EXCEPTION 'committed usage cost does not match reservation price'
            USING ERRCODE = 'check_violation';
    END IF;
    expected_credit_micros := LEAST(
        NEW.total_cost_micros,
        reservation.held_credit_micros
    );
    expected_overage_micros := NEW.total_cost_micros - expected_credit_micros;
    IF NEW.credit_applied_micros <> expected_credit_micros
       OR NEW.overage_applied_micros <> expected_overage_micros THEN
        RAISE EXCEPTION 'committed usage split does not match reservation holds'
            USING ERRCODE = 'check_violation';
    END IF;

    NEW.committed_at := statement_timestamp();

    SELECT COALESCE(sum(amount_micros), 0)
    INTO settled_credit_micros
    FROM ledger_entries
    WHERE organization_id = NEW.organization_id
      AND entry_type IN (
          'credit_grant',
          'credit_reversal',
          'credit_commit',
          'credit_forfeiture'
      );
    SELECT COALESCE(sum(held_credit_micros), 0)
    INTO active_credit_micros
    FROM usage_reservations
    WHERE organization_id = NEW.organization_id
      AND status = 'held';
    IF settled_credit_micros < active_credit_micros THEN
        RAISE EXCEPTION 'reservation credit capacity is no longer available'
            USING ERRCODE = 'check_violation';
    END IF;

    IF reservation.held_overage_micros > 0 THEN
        SELECT period.starts_at, period.ends_at, period.overage_limit_micros
        INTO period_starts_at, period_ends_at, period_overage_limit_micros
        FROM billing_periods AS period
        JOIN subscriptions AS subscription
          ON subscription.organization_id = period.organization_id
         AND subscription.id = period.subscription_id
        WHERE period.organization_id = NEW.organization_id
          AND period.starts_at <= NEW.committed_at
          AND period.ends_at > NEW.committed_at
          AND period.starts_at <= reservation.created_at
          AND period.ends_at >= reservation.expires_at
          AND subscription.status = 'active'
          AND subscription.current_period_starts_at = period.starts_at
          AND subscription.current_period_ends_at = period.ends_at
        FOR SHARE OF period, subscription;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'reservation has no current billing capacity'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT COALESCE(sum(overage_applied_micros), 0)
        INTO committed_overage_micros
        FROM usage_records
        WHERE organization_id = NEW.organization_id
          AND committed_at >= period_starts_at
          AND committed_at < period_ends_at;
        SELECT COALESCE(sum(held_overage_micros), 0)
        INTO active_overage_micros
        FROM usage_reservations
        WHERE organization_id = NEW.organization_id
          AND status = 'held'
          AND created_at >= period_starts_at
          AND created_at < period_ends_at;

        IF committed_overage_micros + active_overage_micros
           > period_overage_limit_micros THEN
            RAISE EXCEPTION 'reservation overage capacity is no longer available'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_records_validate_reservation
BEFORE INSERT
ON usage_records
FOR EACH ROW EXECUTE FUNCTION validate_usage_record_reservation();

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
    linked_reservation_status text;
    linked_team_id uuid;
    linked_team_name text;
    linked_held_credit_micros bigint;
    linked_held_overage_micros bigint;
    linked_usage_committed_at timestamptz;
    linked_credit_applied_micros bigint;
    linked_overage_applied_micros bigint;
    linked_period_starts_at timestamptz;
    linked_period_ends_at timestamptz;
    current_balance_micros numeric;
BEGIN
    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'ledger organization does not exist'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    SELECT COALESCE(sum(amount_micros), 0)
    INTO current_balance_micros
    FROM ledger_entries
    WHERE organization_id = NEW.organization_id;
    IF NEW.balance_after_micros::numeric
       <> current_balance_micros + NEW.amount_micros::numeric THEN
        RAISE EXCEPTION 'ledger balance does not match prior balance and amount'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.billing_period_id IS NOT NULL THEN
        SELECT starts_at, ends_at
        INTO linked_period_starts_at, linked_period_ends_at
        FROM billing_periods
        WHERE id = NEW.billing_period_id
          AND organization_id = NEW.organization_id
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'ledger billing period does not belong to organization'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        NEW.billing_period_starts_at_snapshot := linked_period_starts_at;
        NEW.billing_period_ends_at_snapshot := linked_period_ends_at;
    ELSE
        NEW.billing_period_starts_at_snapshot := NULL;
        NEW.billing_period_ends_at_snapshot := NULL;
    END IF;

    IF NEW.reservation_id IS NOT NULL THEN
        SELECT
            team_id,
            team_name_snapshot,
            status,
            held_credit_micros,
            held_overage_micros
        INTO
            linked_team_id,
            linked_team_name,
            linked_reservation_status,
            linked_held_credit_micros,
            linked_held_overage_micros
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
        IF NEW.entry_type IN (
            'credit_hold',
            'credit_release',
            'overage_hold',
            'overage_release'
        ) AND linked_reservation_status <> 'held' THEN
            RAISE EXCEPTION 'finalized reservation holds cannot change'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.entry_type = 'credit_hold'
           AND COALESCE((
               SELECT -sum(amount_micros)
               FROM ledger_entries
               WHERE reservation_id = NEW.reservation_id
                 AND entry_type = 'credit_hold'
           ), 0) - NEW.amount_micros::numeric
               > linked_held_credit_micros::numeric THEN
            RAISE EXCEPTION 'credit hold exceeds reservation'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.entry_type = 'credit_release'
           AND COALESCE((
               SELECT sum(amount_micros)
               FROM ledger_entries
               WHERE reservation_id = NEW.reservation_id
                 AND entry_type = 'credit_release'
           ), 0) + NEW.amount_micros::numeric
               > linked_held_credit_micros::numeric THEN
            RAISE EXCEPTION 'credit release exceeds reservation hold'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.entry_type = 'overage_hold'
           AND COALESCE((
               SELECT -sum(amount_micros)
               FROM ledger_entries
               WHERE reservation_id = NEW.reservation_id
                 AND entry_type = 'overage_hold'
           ), 0) - NEW.amount_micros::numeric
               > linked_held_overage_micros::numeric THEN
            RAISE EXCEPTION 'overage hold exceeds reservation'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.entry_type = 'overage_release'
           AND COALESCE((
               SELECT sum(amount_micros)
               FROM ledger_entries
               WHERE reservation_id = NEW.reservation_id
                 AND entry_type = 'overage_release'
           ), 0) + NEW.amount_micros::numeric
               > linked_held_overage_micros::numeric THEN
            RAISE EXCEPTION 'overage release exceeds reservation hold'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.usage_record_id IS NOT NULL THEN
        SELECT
            reservation_id,
            team_id,
            team_name_snapshot,
            committed_at,
            credit_applied_micros,
            overage_applied_micros
        INTO
            linked_reservation_id,
            linked_team_id,
            linked_team_name,
            linked_usage_committed_at,
            linked_credit_applied_micros,
            linked_overage_applied_micros
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
        IF NEW.entry_type IN ('credit_commit', 'overage_commit')
           AND (
               linked_usage_committed_at < linked_period_starts_at
               OR linked_usage_committed_at >= linked_period_ends_at
           ) THEN
            RAISE EXCEPTION 'usage settlement does not belong to billing period'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.entry_type = 'credit_commit'
           AND COALESCE((
               SELECT -sum(amount_micros)
               FROM ledger_entries
               WHERE usage_record_id = NEW.usage_record_id
                 AND entry_type = 'credit_commit'
           ), 0) - NEW.amount_micros::numeric
               > linked_credit_applied_micros::numeric THEN
            RAISE EXCEPTION 'credit commit exceeds usage record settlement'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.entry_type = 'overage_commit'
           AND COALESCE((
               SELECT -sum(amount_micros)
               FROM ledger_entries
               WHERE usage_record_id = NEW.usage_record_id
                 AND entry_type = 'overage_commit'
           ), 0) - NEW.amount_micros::numeric
               > linked_overage_applied_micros::numeric THEN
            RAISE EXCEPTION 'overage commit exceeds usage record settlement'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER ledger_entries_validate_links
BEFORE INSERT ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION validate_ledger_entry_links();

CREATE FUNCTION require_usage_record_ledger_settlement()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    committed_credit_micros numeric;
    committed_overage_micros numeric;
    reservation_is_committed boolean;
BEGIN
    SELECT
        COALESCE(-sum(amount_micros) FILTER (
            WHERE entry_type = 'credit_commit'
        ), 0),
        COALESCE(-sum(amount_micros) FILTER (
            WHERE entry_type = 'overage_commit'
        ), 0)
    INTO committed_credit_micros, committed_overage_micros
    FROM ledger_entries
    WHERE usage_record_id = NEW.id;

    SELECT EXISTS (
        SELECT 1
        FROM usage_reservations
        WHERE id = NEW.reservation_id
          AND status = 'committed'
    )
    INTO reservation_is_committed;

    IF NOT reservation_is_committed
       OR committed_credit_micros <> NEW.credit_applied_micros::numeric
       OR committed_overage_micros <> NEW.overage_applied_micros::numeric THEN
        RAISE EXCEPTION 'usage record ledger settlement does not match committed usage'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER usage_records_require_ledger_settlement
AFTER INSERT ON usage_records
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_usage_record_ledger_settlement();
