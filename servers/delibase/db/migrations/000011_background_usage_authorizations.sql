-- Background usage authorizations contain identifiers, closed enum values,
-- limits, and pseudonymous actors only. Credential or provider fields are
-- intentionally absent.
CREATE TABLE background_usage_authorizations (
    id uuid PRIMARY KEY,
    authorizer_account_id uuid NOT NULL,
    owner_type text NOT NULL
        CHECK (owner_type IN ('personal_account', 'organization')),
    owner_account_id uuid,
    owner_organization_id uuid,
    organization_id uuid NOT NULL,
    team_id uuid NOT NULL,
    team_name_snapshot text NOT NULL
        CHECK (length(team_name_snapshot) BETWEEN 1 AND 120),
    service_identity_id uuid NOT NULL,
    meter_id uuid NOT NULL,
    purpose text NOT NULL CHECK (purpose = 'realqa_storage'),
    feature_resource_id uuid NOT NULL,
    period text NOT NULL CHECK (period = 'utc_day'),
    maximum_units bigint NOT NULL CHECK (maximum_units > 0),
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN (
            'active',
            'revoked',
            'access_lost',
            'resource_deleted',
            'owner_deleted'
        )),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    actor_reference text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    revoked_at timestamptz,
    retain_until timestamptz NOT NULL
        DEFAULT (statement_timestamp() + interval '7 years'),
    UNIQUE (
        id,
        organization_id,
        team_id,
        authorizer_account_id,
        service_identity_id,
        meter_id,
        purpose,
        feature_resource_id,
        period
    ),
    CHECK (is_uuid_v7(id)),
    CHECK (is_uuid_v7(authorizer_account_id)),
    CHECK (owner_account_id IS NULL OR is_uuid_v7(owner_account_id)),
    CHECK (
        owner_organization_id IS NULL
        OR is_uuid_v7(owner_organization_id)
    ),
    CHECK (is_uuid_v7(organization_id)),
    CHECK (is_uuid_v7(team_id)),
    CHECK (is_uuid_v7(service_identity_id)),
    CHECK (is_uuid_v7(meter_id)),
    CHECK (is_uuid_v7(feature_resource_id)),
    CHECK (
        (
            owner_type = 'personal_account'
            AND owner_account_id IS NOT NULL
            AND owner_organization_id IS NULL
            AND owner_account_id = authorizer_account_id
        )
        OR
        (
            owner_type = 'organization'
            AND owner_account_id IS NULL
            AND owner_organization_id = organization_id
        )
    ),
    CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    CHECK (
        (status = 'active' AND revoked_at IS NULL)
        OR (status <> 'active' AND revoked_at IS NOT NULL)
    ),
    CHECK (updated_at >= created_at),
    CHECK (retain_until >= updated_at + interval '7 years')
);

CREATE UNIQUE INDEX background_usage_authorizations_active_resource_idx
    ON background_usage_authorizations(
        service_identity_id,
        purpose,
        feature_resource_id
    )
    WHERE status = 'active';
CREATE INDEX background_usage_authorizations_authorizer_idx
    ON background_usage_authorizations(authorizer_account_id, id);
CREATE INDEX background_usage_authorizations_organization_idx
    ON background_usage_authorizations(organization_id, id);
CREATE INDEX background_usage_authorizations_team_idx
    ON background_usage_authorizations(team_id, id);

CREATE TABLE background_usage_authorization_transitions (
    authorization_id uuid NOT NULL
        REFERENCES background_usage_authorizations(id) ON DELETE RESTRICT,
    revision bigint NOT NULL CHECK (revision > 0),
    from_status text CHECK (
        from_status IS NULL
        OR from_status IN (
            'active',
            'revoked',
            'access_lost',
            'resource_deleted',
            'owner_deleted'
        )
    ),
    to_status text NOT NULL CHECK (to_status IN (
        'active',
        'revoked',
        'access_lost',
        'resource_deleted',
        'owner_deleted'
    )),
    actor_reference text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    retain_until timestamptz NOT NULL
        DEFAULT (statement_timestamp() + interval '7 years'),
    PRIMARY KEY (authorization_id, revision),
    CHECK (
        (revision = 1 AND from_status IS NULL AND to_status = 'active')
        OR
        (
            revision > 1
            AND from_status = 'active'
            AND to_status <> 'active'
        )
    ),
    CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    CHECK (retain_until >= occurred_at + interval '7 years')
);

CREATE FUNCTION validate_new_background_usage_authorization()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    locked_team_name text;
BEGIN
    IF NEW.status <> 'active'
       OR NEW.revision <> 1
       OR NEW.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION 'background authorization must be created active'
            USING ERRCODE = 'check_violation';
    END IF;

    -- Use the same organization-first lock order as usage reservations and
    -- membership/deletion transactions.
    PERFORM 1
    FROM organizations
    WHERE id = NEW.organization_id
      AND deleted_at IS NULL
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'background authorization payer is unavailable'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    PERFORM 1
    FROM accounts
    WHERE id = NEW.authorizer_account_id
      AND status = 'active'
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'background authorization authorizer is unavailable'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NEW.owner_type = 'personal_account' THEN
        PERFORM 1
        FROM accounts
        WHERE id = NEW.owner_account_id
          AND status = 'active'
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'background authorization owner is unavailable'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
    ELSE
        PERFORM 1
        FROM organizations
        WHERE id = NEW.owner_organization_id
          AND deleted_at IS NULL
        FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'background authorization owner is unavailable'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
    END IF;

    SELECT name
    INTO locked_team_name
    FROM teams
    WHERE organization_id = NEW.organization_id
      AND id = NEW.team_id
    FOR KEY SHARE;
    IF NOT FOUND
       OR effective_team_role(
           NEW.organization_id,
           NEW.team_id,
           NEW.authorizer_account_id
       ) IS NULL THEN
        RAISE EXCEPTION 'background authorization team access is unavailable'
            USING ERRCODE = 'check_violation';
    END IF;
    NEW.team_name_snapshot := locked_team_name;

    PERFORM 1
    FROM service_meter_allowlists AS allowlist
    JOIN service_identities AS service
      ON service.id = allowlist.service_identity_id
    JOIN catalog_meters AS meter ON meter.id = allowlist.meter_id
    JOIN catalog_apps AS app ON app.id = meter.app_id
    JOIN polar_meter_mappings AS mapping ON mapping.meter_id = meter.id
    WHERE allowlist.service_identity_id = NEW.service_identity_id
      AND allowlist.meter_id = NEW.meter_id
      AND allowlist.enabled
      AND service.enabled
      AND meter.enabled
      AND app.enabled
    FOR KEY SHARE OF allowlist, service, meter, app, mapping;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'background authorization connection is unavailable'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    NEW.created_at := statement_timestamp();
    NEW.updated_at := NEW.created_at;
    NEW.retain_until := NEW.created_at + interval '7 years';
    RETURN NEW;
END;
$$;

CREATE TRIGGER background_usage_authorizations_validate_new
BEFORE INSERT ON background_usage_authorizations
FOR EACH ROW EXECUTE FUNCTION validate_new_background_usage_authorization();

CREATE FUNCTION preserve_background_usage_authorization()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'background authorizations cannot be deleted'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.authorizer_account_id IS DISTINCT FROM OLD.authorizer_account_id
       OR NEW.owner_type IS DISTINCT FROM OLD.owner_type
       OR NEW.owner_account_id IS DISTINCT FROM OLD.owner_account_id
       OR NEW.owner_organization_id IS DISTINCT FROM OLD.owner_organization_id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.team_name_snapshot IS DISTINCT FROM OLD.team_name_snapshot
       OR NEW.service_identity_id IS DISTINCT FROM OLD.service_identity_id
       OR NEW.meter_id IS DISTINCT FROM OLD.meter_id
       OR NEW.purpose IS DISTINCT FROM OLD.purpose
       OR NEW.feature_resource_id IS DISTINCT FROM OLD.feature_resource_id
       OR NEW.period IS DISTINCT FROM OLD.period
       OR NEW.maximum_units IS DISTINCT FROM OLD.maximum_units
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'background authorization bindings are immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status <> 'active'
       OR NEW.status = 'active'
       OR NEW.status = OLD.status
       OR NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'background authorization transition is invalid'
            USING ERRCODE = 'check_violation';
    END IF;
    NEW.updated_at := statement_timestamp();
    NEW.revoked_at := NEW.updated_at;
    NEW.retain_until := GREATEST(
        OLD.retain_until,
        NEW.updated_at + interval '7 years'
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER background_usage_authorizations_preserve
BEFORE UPDATE OR DELETE ON background_usage_authorizations
FOR EACH ROW EXECUTE FUNCTION preserve_background_usage_authorization();

CREATE FUNCTION append_background_usage_authorization_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO background_usage_authorization_transitions (
        authorization_id,
        revision,
        from_status,
        to_status,
        actor_reference,
        occurred_at,
        retain_until
    ) VALUES (
        NEW.id,
        NEW.revision,
        CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE OLD.status END,
        NEW.status,
        NEW.actor_reference,
        NEW.updated_at,
        NEW.retain_until
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER background_usage_authorizations_append_transition
AFTER INSERT OR UPDATE ON background_usage_authorizations
FOR EACH ROW EXECUTE FUNCTION append_background_usage_authorization_transition();

CREATE FUNCTION reject_background_usage_authorization_transition_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'background authorization transitions are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER background_usage_authorization_transitions_append_only
BEFORE UPDATE OR DELETE ON background_usage_authorization_transitions
FOR EACH ROW
EXECUTE FUNCTION reject_background_usage_authorization_transition_mutation();

CREATE FUNCTION background_usage_authorization_owner_is_current(
    grant_row background_usage_authorizations
)
RETURNS boolean
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT CASE grant_row.owner_type
        WHEN 'personal_account' THEN
            grant_row.owner_account_id = grant_row.authorizer_account_id
            AND EXISTS (
                SELECT 1
                FROM accounts AS owner
                WHERE owner.id = grant_row.owner_account_id
                  AND owner.status = 'active'
            )
        WHEN 'organization' THEN
            grant_row.owner_organization_id = grant_row.organization_id
            AND EXISTS (
                SELECT 1
                FROM organizations AS owner
                WHERE owner.id = grant_row.owner_organization_id
                  AND owner.deleted_at IS NULL
            )
        ELSE false
    END
$$;

CREATE FUNCTION background_usage_authorization_access_is_current(
    grant_row background_usage_authorizations
)
RETURNS boolean
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT
        grant_row.status = 'active'
        AND background_usage_authorization_owner_is_current(grant_row)
        AND EXISTS (
            SELECT 1
            FROM accounts AS authorizer
            WHERE authorizer.id = grant_row.authorizer_account_id
              AND authorizer.status = 'active'
        )
        AND EXISTS (
            SELECT 1
            FROM organizations AS payer
            WHERE payer.id = grant_row.organization_id
              AND payer.deleted_at IS NULL
        )
        AND EXISTS (
            SELECT 1
            FROM teams AS team
            WHERE team.organization_id = grant_row.organization_id
              AND team.id = grant_row.team_id
        )
        AND effective_team_role(
            grant_row.organization_id,
            grant_row.team_id,
            grant_row.authorizer_account_id
        ) IS NOT NULL
        AND EXISTS (
            SELECT 1
            FROM service_meter_allowlists AS allowlist
            JOIN service_identities AS service
              ON service.id = allowlist.service_identity_id
            JOIN catalog_meters AS meter ON meter.id = allowlist.meter_id
            JOIN catalog_apps AS app ON app.id = meter.app_id
            JOIN polar_meter_mappings AS mapping ON mapping.meter_id = meter.id
            WHERE allowlist.service_identity_id
                    = grant_row.service_identity_id
              AND allowlist.meter_id = grant_row.meter_id
              AND allowlist.enabled
              AND service.enabled
              AND meter.enabled
              AND app.enabled
        )
$$;

-- Existing operation scoping already includes caller kind and caller identity.
-- Extend the closed operation set and apply credential-shape rejection only to
-- the new operations so benign historical keys remain reproducible.
ALTER TABLE idempotency_records
    DROP CONSTRAINT idempotency_records_operation_check,
    ADD CONSTRAINT idempotency_records_operation_check CHECK (operation IN (
        'complete_onboarding',
        'delete_account',
        'create_organization',
        'update_organization',
        'update_organization_slug',
        'delete_organization',
        'update_organization_member_role',
        'remove_organization_member',
        'leave_organization',
        'accept_invitation',
        'revoke_invitation',
        'create_team',
        'update_team',
        'move_team',
        'delete_team_subtree',
        'set_team_membership',
        'remove_team_membership',
        'create_subscription_checkout',
        'create_billing_portal_session',
        'update_overage_limit',
        'reserve_usage',
        'commit_usage',
        'release_usage',
        'create_background_usage_authorization',
        'revoke_background_usage_authorization',
        'reserve_authorized_usage',
        'commit_authorized_usage',
        'release_authorized_usage',
        'mark_background_usage_resource_deleted'
    )),
    ADD CONSTRAINT idempotency_records_background_key_safe_check CHECK (
        operation NOT IN (
            'create_background_usage_authorization',
            'revoke_background_usage_authorization',
            'reserve_authorized_usage',
            'commit_authorized_usage',
            'release_authorized_usage',
            'mark_background_usage_resource_deleted'
        )
        OR (
            idempotency_key
                ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$'
            AND usage_client_reference_is_credential_safe(idempotency_key)
        )
    );

-- A normal reservation remains the only financial hold. These nullable
-- columns distinguish live-user reservations from authorization-bound ones
-- and snapshot the exact authorization context without any credential.
ALTER TABLE usage_reservations
    ADD COLUMN background_usage_authorization_id uuid,
    ADD COLUMN background_usage_purpose text,
    ADD COLUMN background_feature_resource_id uuid,
    ADD COLUMN background_usage_period text,
    ADD COLUMN background_period_start timestamptz,
    ADD CONSTRAINT usage_reservations_background_binding_complete_check CHECK (
        (
            background_usage_authorization_id IS NULL
            AND background_usage_purpose IS NULL
            AND background_feature_resource_id IS NULL
            AND background_usage_period IS NULL
            AND background_period_start IS NULL
        )
        OR
        (
            background_usage_authorization_id IS NOT NULL
            AND is_uuid_v7(background_usage_authorization_id)
            AND background_usage_purpose = 'realqa_storage'
            AND background_feature_resource_id IS NOT NULL
            AND is_uuid_v7(background_feature_resource_id)
            AND background_usage_period = 'utc_day'
            AND background_period_start IS NOT NULL
            AND background_period_start = (
                date_trunc(
                    'day',
                    background_period_start AT TIME ZONE 'UTC'
                ) AT TIME ZONE 'UTC'
            )
        )
    ),
    ADD CONSTRAINT usage_reservations_background_authorization_fk
        FOREIGN KEY (
            background_usage_authorization_id,
            organization_id,
            team_id,
            account_id,
            service_identity_id,
            meter_id,
            background_usage_purpose,
            background_feature_resource_id,
            background_usage_period
        )
        REFERENCES background_usage_authorizations(
            id,
            organization_id,
            team_id,
            authorizer_account_id,
            service_identity_id,
            meter_id,
            purpose,
            feature_resource_id,
            period
        )
        ON DELETE RESTRICT;

CREATE INDEX usage_reservations_background_period_idx
    ON usage_reservations(
        background_usage_authorization_id,
        background_period_start
    )
    WHERE background_usage_authorization_id IS NOT NULL;

CREATE FUNCTION validate_background_usage_reservation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    grant_row background_usage_authorizations%ROWTYPE;
    held_units numeric;
    committed_units numeric;
    utc_today timestamptz;
BEGIN
    IF NEW.background_usage_authorization_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT *
    INTO grant_row
    FROM background_usage_authorizations
    WHERE id = NEW.background_usage_authorization_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'background authorization does not exist'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    IF NOT background_usage_authorization_access_is_current(grant_row) THEN
        RAISE EXCEPTION 'background authorization access is unavailable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.organization_id <> grant_row.organization_id
       OR NEW.team_id <> grant_row.team_id
       OR NEW.account_id <> grant_row.authorizer_account_id
       OR NEW.service_identity_id <> grant_row.service_identity_id
       OR NEW.meter_id <> grant_row.meter_id
       OR NEW.background_usage_purpose <> grant_row.purpose
       OR NEW.background_feature_resource_id
            <> grant_row.feature_resource_id
       OR NEW.background_usage_period <> grant_row.period THEN
        RAISE EXCEPTION 'background authorization binding was substituted'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.maximum_units > grant_row.maximum_units THEN
        RAISE EXCEPTION 'background authorization period limit exceeded'
            USING ERRCODE = 'check_violation';
    END IF;

    utc_today := date_trunc(
        'day',
        statement_timestamp() AT TIME ZONE 'UTC'
    ) AT TIME ZONE 'UTC';
    IF NEW.background_period_start NOT IN (
        utc_today,
        utc_today - interval '1 day'
    ) THEN
        RAISE EXCEPTION 'background authorization period is not reservable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COALESCE(sum(reservation.maximum_units), 0)
    INTO held_units
    FROM usage_reservations AS reservation
    WHERE reservation.background_usage_authorization_id = grant_row.id
      AND reservation.background_period_start = NEW.background_period_start
      AND reservation.status = 'held';

    SELECT COALESCE(sum(record.committed_units), 0)
    INTO committed_units
    FROM usage_records AS record
    WHERE record.background_usage_authorization_id = grant_row.id
      AND record.background_period_start = NEW.background_period_start;

    IF held_units + committed_units + NEW.maximum_units::numeric
       > grant_row.maximum_units::numeric THEN
        RAISE EXCEPTION 'background authorization period limit exceeded'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_validate_background_authorization
BEFORE INSERT ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION validate_background_usage_reservation();

CREATE FUNCTION preserve_background_usage_reservation_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.background_usage_authorization_id
            IS DISTINCT FROM OLD.background_usage_authorization_id
       OR NEW.background_usage_purpose
            IS DISTINCT FROM OLD.background_usage_purpose
       OR NEW.background_feature_resource_id
            IS DISTINCT FROM OLD.background_feature_resource_id
       OR NEW.background_usage_period
            IS DISTINCT FROM OLD.background_usage_period
       OR NEW.background_period_start
            IS DISTINCT FROM OLD.background_period_start THEN
        RAISE EXCEPTION 'background reservation binding is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_reservations_preserve_background_binding
BEFORE UPDATE ON usage_reservations
FOR EACH ROW EXECUTE FUNCTION preserve_background_usage_reservation_binding();

ALTER TABLE usage_records
    ADD COLUMN background_usage_authorization_id uuid,
    ADD COLUMN background_usage_purpose text,
    ADD COLUMN background_feature_resource_id uuid,
    ADD COLUMN background_usage_period text,
    ADD COLUMN background_period_start timestamptz,
    ADD CONSTRAINT usage_records_background_binding_complete_check CHECK (
        (
            background_usage_authorization_id IS NULL
            AND background_usage_purpose IS NULL
            AND background_feature_resource_id IS NULL
            AND background_usage_period IS NULL
            AND background_period_start IS NULL
        )
        OR
        (
            background_usage_authorization_id IS NOT NULL
            AND is_uuid_v7(background_usage_authorization_id)
            AND background_usage_purpose = 'realqa_storage'
            AND background_feature_resource_id IS NOT NULL
            AND is_uuid_v7(background_feature_resource_id)
            AND background_usage_period = 'utc_day'
            AND background_period_start IS NOT NULL
            AND background_period_start = (
                date_trunc(
                    'day',
                    background_period_start AT TIME ZONE 'UTC'
                ) AT TIME ZONE 'UTC'
            )
        )
    );

CREATE FUNCTION capture_background_usage_record_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    SELECT
        reservation.background_usage_authorization_id,
        reservation.background_usage_purpose,
        reservation.background_feature_resource_id,
        reservation.background_usage_period,
        reservation.background_period_start
    INTO
        NEW.background_usage_authorization_id,
        NEW.background_usage_purpose,
        NEW.background_feature_resource_id,
        NEW.background_usage_period,
        NEW.background_period_start
    FROM usage_reservations AS reservation
    WHERE reservation.id = NEW.reservation_id
      AND reservation.organization_id = NEW.organization_id
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usage record reservation binding is unavailable'
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER usage_records_capture_background_binding
BEFORE INSERT ON usage_records
FOR EACH ROW EXECUTE FUNCTION capture_background_usage_record_binding();

CREATE UNIQUE INDEX usage_records_one_background_period_settlement_idx
    ON usage_records(
        background_usage_authorization_id,
        background_period_start
    )
    WHERE background_usage_authorization_id IS NOT NULL;

-- Security audits may reference a retained authorization without introducing
-- a foreign key that could weaken the audit table's append-only lifetime.
ALTER TABLE audit_events
    ADD COLUMN background_usage_authorization_id uuid,
    ADD CONSTRAINT audit_events_background_authorization_id_check CHECK (
        background_usage_authorization_id IS NULL
        OR is_uuid_v7(background_usage_authorization_id)
    );
CREATE INDEX audit_events_background_authorization_idx
    ON audit_events(background_usage_authorization_id, occurred_at, id)
    WHERE background_usage_authorization_id IS NOT NULL;

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
        'background_authorization.created',
        'background_authorization.revoked',
        'background_authorization.access_lost',
        'background_authorization.resource_deleted',
        'background_authorization.owner_deleted',
        'account.deletion_requested',
        'organization.deletion_requested',
        'webhook.received',
        'webhook.processed'
    ));
