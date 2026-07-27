-- Organization-owned authorizations require an Owner or Admin not only at
-- creation, but for as long as the authorization remains active. Personal
-- authorizations continue to depend only on the author's effective team access.
CREATE OR REPLACE FUNCTION background_usage_authorization_access_is_current(
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
        AND (
            grant_row.owner_type = 'personal_account'
            OR EXISTS (
                SELECT 1
                FROM organization_memberships AS authorizer_membership
                WHERE authorizer_membership.organization_id
                        = grant_row.organization_id
                  AND authorizer_membership.account_id
                        = grant_row.authorizer_account_id
                  AND authorizer_membership.role IN ('owner', 'admin')
            )
        )
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

-- Close any authorization whose manager role was already lost before this
-- predicate was tightened.
SELECT close_invalid_background_usage_authorizations();
