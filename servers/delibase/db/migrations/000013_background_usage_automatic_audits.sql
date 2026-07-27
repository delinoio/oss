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
