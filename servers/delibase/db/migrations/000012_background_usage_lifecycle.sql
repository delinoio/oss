-- Revalidate affected active authorizations after authoritative source state
-- changes. Constraint triggers invoke this function at transaction end so
-- catalog synchronization may disable and restore an unchanged connection in
-- one transaction without irreversibly closing its authorizations.
CREATE FUNCTION close_invalid_background_usage_authorizations(
    affected_account_id uuid DEFAULT NULL,
    affected_organization_id uuid DEFAULT NULL,
    affected_service_identity_id uuid DEFAULT NULL,
    affected_meter_id uuid DEFAULT NULL
)
RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
    affected bigint;
BEGIN
    UPDATE background_usage_authorizations AS grant_row
    SET status = CASE
            WHEN background_usage_authorization_owner_is_current(grant_row)
                THEN 'access_lost'
            ELSE 'owner_deleted'
        END,
        revision = grant_row.revision + 1,
        actor_reference = ''
    WHERE grant_row.status = 'active'
      AND (
          (
              affected_account_id IS NULL
              AND affected_organization_id IS NULL
              AND affected_service_identity_id IS NULL
              AND affected_meter_id IS NULL
          )
          OR grant_row.authorizer_account_id = affected_account_id
          OR grant_row.owner_account_id = affected_account_id
          OR grant_row.organization_id = affected_organization_id
          OR grant_row.owner_organization_id = affected_organization_id
          OR grant_row.service_identity_id = affected_service_identity_id
          OR grant_row.meter_id = affected_meter_id
      )
      AND NOT background_usage_authorization_access_is_current(grant_row);
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected;
END;
$$;

CREATE FUNCTION revalidate_background_authorizations_for_account()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_account_id => OLD.id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER accounts_revalidate_background_authorizations
AFTER UPDATE OF status OR DELETE ON accounts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_account();

CREATE FUNCTION revalidate_background_authorizations_for_organization()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_organization_id => OLD.id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER organizations_revalidate_background_authorizations
AFTER UPDATE OF deleted_at OR DELETE ON organizations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_organization();

CREATE FUNCTION revalidate_background_authorizations_for_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_account_id => OLD.account_id,
        affected_organization_id => OLD.organization_id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER organization_memberships_revalidate_background
AFTER UPDATE OF role OR DELETE ON organization_memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_membership();

CREATE CONSTRAINT TRIGGER team_memberships_revalidate_background
AFTER UPDATE OF role OR DELETE ON team_memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_membership();

CREATE FUNCTION revalidate_background_authorizations_for_team()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- A move or subtree deletion can alter inherited access for any
    -- authorization in the organization, not only for the directly changed
    -- team.
    PERFORM close_invalid_background_usage_authorizations(
        affected_organization_id => OLD.organization_id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER teams_revalidate_background_authorizations
AFTER UPDATE OF parent_team_id OR DELETE ON teams
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_team();

CREATE FUNCTION revalidate_background_authorizations_for_service()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_service_identity_id => OLD.id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER service_identities_revalidate_background
AFTER UPDATE OF enabled OR DELETE ON service_identities
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_service();

CREATE FUNCTION revalidate_background_authorizations_for_connection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_service_identity_id => OLD.service_identity_id,
        affected_meter_id => OLD.meter_id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER service_meter_allowlists_revalidate_background
AFTER UPDATE OF enabled OR DELETE ON service_meter_allowlists
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_connection();

CREATE FUNCTION revalidate_background_authorizations_for_meter()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_meter_id => OLD.id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER catalog_meters_revalidate_background
AFTER UPDATE OF enabled OR DELETE ON catalog_meters
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_meter();

CREATE FUNCTION revalidate_background_authorizations_for_app()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE background_usage_authorizations AS grant_row
    SET status = CASE
            WHEN background_usage_authorization_owner_is_current(grant_row)
                THEN 'access_lost'
            ELSE 'owner_deleted'
        END,
        revision = grant_row.revision + 1,
        actor_reference = ''
    WHERE grant_row.status = 'active'
      AND EXISTS (
          SELECT 1
          FROM catalog_meters AS meter
          WHERE meter.id = grant_row.meter_id
            AND meter.app_id = OLD.id
      )
      AND NOT background_usage_authorization_access_is_current(grant_row);
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER catalog_apps_revalidate_background
AFTER UPDATE OF enabled OR DELETE ON catalog_apps
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_app();

CREATE FUNCTION revalidate_background_authorizations_for_polar_mapping()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM close_invalid_background_usage_authorizations(
        affected_meter_id => OLD.meter_id
    );
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER polar_meter_mappings_revalidate_background
AFTER DELETE ON polar_meter_mappings
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION revalidate_background_authorizations_for_polar_mapping();
