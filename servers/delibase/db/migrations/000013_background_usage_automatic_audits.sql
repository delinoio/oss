-- Migration 000011 may already be recorded in deployed databases, so add the
-- immutable team snapshot without changing that migration's checksum. Prefer
-- the creation audit's historical name and fall back to the current team for
-- rows created outside the service transaction.
ALTER TABLE background_usage_authorizations
    ADD COLUMN team_name_snapshot text;

ALTER TABLE background_usage_authorizations
    DISABLE TRIGGER background_usage_authorizations_preserve;
ALTER TABLE background_usage_authorizations
    DISABLE TRIGGER background_usage_authorizations_append_transition;

UPDATE background_usage_authorizations AS grant_row
SET team_name_snapshot = COALESCE(
    (
        SELECT audit.team_name_snapshot
        FROM audit_events AS audit
        WHERE audit.background_usage_authorization_id = grant_row.id
          AND audit.event_type = 'background_authorization.created'
          AND audit.organization_id = grant_row.organization_id
          AND audit.team_id = grant_row.team_id
          AND audit.team_name_snapshot IS NOT NULL
        ORDER BY audit.occurred_at, audit.id
        LIMIT 1
    ),
    (
        SELECT team.name
        FROM teams AS team
        WHERE team.organization_id = grant_row.organization_id
          AND team.id = grant_row.team_id
    )
);

ALTER TABLE background_usage_authorizations
    ENABLE TRIGGER background_usage_authorizations_append_transition;
ALTER TABLE background_usage_authorizations
    ENABLE TRIGGER background_usage_authorizations_preserve;

ALTER TABLE background_usage_authorizations
    ALTER COLUMN team_name_snapshot SET NOT NULL,
    ADD CONSTRAINT background_usage_authorizations_team_name_snapshot_check
        CHECK (length(team_name_snapshot) BETWEEN 1 AND 120);

CREATE OR REPLACE FUNCTION validate_new_background_usage_authorization()
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

CREATE OR REPLACE FUNCTION preserve_background_usage_authorization()
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

-- Automatic access and owner loss runs inside deferred database triggers, so
-- it must append its authorization audit in the same storage-owned path.
CREATE OR REPLACE FUNCTION append_background_usage_authorization_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    audit_entropy text;
    audit_id uuid;
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

    IF TG_OP = 'UPDATE'
       AND NEW.status IN ('access_lost', 'owner_deleted') THEN
        -- Preserve the authorization's UUID v7 timestamp and random-a bits,
        -- then derive the remaining entropy from this unique transition.
        -- This keeps trigger-owned audit IDs in the repository's UUID v7
        -- contract without depending on an application-side ID generator.
        audit_entropy := md5(
            NEW.id::text || ':' || NEW.revision::text || ':' || NEW.status
        );
        audit_id := (
            substring(NEW.id::text FROM 1 FOR 18)
            || '-8'
            || substring(audit_entropy FROM 2 FOR 3)
            || '-'
            || substring(audit_entropy FROM 5 FOR 12)
        )::uuid;

        INSERT INTO audit_events (
            id,
            occurred_at,
            event_type,
            actor_reference,
            organization_id,
            team_id,
            team_name_snapshot,
            service_identity_id,
            meter_id,
            background_usage_authorization_id,
            decision,
            result,
            metadata,
            retain_until
        ) VALUES (
            audit_id,
            NEW.updated_at,
            CASE NEW.status
                WHEN 'access_lost'
                    THEN 'background_authorization.access_lost'
                WHEN 'owner_deleted'
                    THEN 'background_authorization.owner_deleted'
            END,
            '',
            NEW.organization_id,
            NEW.team_id,
            NEW.team_name_snapshot,
            NEW.service_identity_id,
            NEW.meter_id,
            NEW.id,
            'allow',
            'success',
            '{}'::jsonb,
            NEW.retain_until
        );
    END IF;
    RETURN NEW;
END;
$$;
