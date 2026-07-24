-- Read helpers keep recursive hierarchy policy inside PostgreSQL while
-- presenting sqlc with a small, typed query surface.
ALTER TABLE audit_events
    ADD COLUMN team_name_snapshot text,
    ADD CONSTRAINT audit_events_team_name_snapshot_check CHECK (
        team_name_snapshot IS NULL
        OR (
            team_id IS NOT NULL
            AND length(team_name_snapshot) BETWEEN 1 AND 120
        )
    );

ALTER TABLE organization_invitations
    DROP CONSTRAINT organization_invitations_token_hash_check,
    ADD CONSTRAINT organization_invitations_token_hash_check
        CHECK (octet_length(token_hash) = 32);

CREATE FUNCTION team_depth(
    target_organization_id uuid,
    target_team_id uuid
)
RETURNS integer
LANGUAGE sql
STABLE
STRICT
AS $$
    WITH RECURSIVE ancestors AS (
        SELECT team.id, team.parent_team_id, 0::integer AS depth
        FROM teams AS team
        WHERE team.organization_id = target_organization_id
          AND team.id = target_team_id

        UNION ALL

        SELECT parent.id, parent.parent_team_id, ancestors.depth + 1
        FROM teams AS parent
        JOIN ancestors ON parent.id = ancestors.parent_team_id
        WHERE parent.organization_id = target_organization_id
    )
    SELECT COALESCE(max(depth), 0)::integer
    FROM ancestors
$$;

CREATE FUNCTION team_is_descendant_or_self(
    target_organization_id uuid,
    target_team_id uuid,
    ancestor_team_id uuid
)
RETURNS boolean
LANGUAGE sql
STABLE
STRICT
AS $$
    WITH RECURSIVE ancestors AS (
        SELECT team.id, team.parent_team_id
        FROM teams AS team
        WHERE team.organization_id = target_organization_id
          AND team.id = target_team_id

        UNION ALL

        SELECT parent.id, parent.parent_team_id
        FROM teams AS parent
        JOIN ancestors ON parent.id = ancestors.parent_team_id
        WHERE parent.organization_id = target_organization_id
    )
    SELECT EXISTS (
        SELECT 1
        FROM ancestors
        WHERE id = ancestor_team_id
    )
$$;

CREATE FUNCTION effective_team_access(
    target_organization_id uuid,
    target_team_id uuid,
    target_account_id uuid
)
RETURNS TABLE (
    effective_role text,
    access_source text,
    source_team_id uuid,
    organization_role text
)
LANGUAGE sql
STABLE
STRICT
AS $$
    WITH RECURSIVE ancestors AS (
        SELECT team.id, team.parent_team_id, 0::integer AS distance
        FROM teams AS team
        WHERE team.organization_id = target_organization_id
          AND team.id = target_team_id

        UNION ALL

        SELECT parent.id, parent.parent_team_id, ancestors.distance + 1
        FROM teams AS parent
        JOIN ancestors ON parent.id = ancestors.parent_team_id
        WHERE parent.organization_id = target_organization_id
    ),
    selected_membership AS (
        SELECT membership.role, membership.team_id, ancestors.distance
        FROM ancestors
        JOIN team_memberships AS membership
          ON membership.organization_id = target_organization_id
         AND membership.team_id = ancestors.id
         AND membership.account_id = target_account_id
        ORDER BY
            CASE membership.role WHEN 'admin' THEN 0 ELSE 1 END,
            ancestors.distance
        LIMIT 1
    )
    SELECT
        CASE
            WHEN organization_membership.role IN ('owner', 'admin') THEN 'admin'
            ELSE selected_membership.role
        END::text,
        CASE
            WHEN organization_membership.role IN ('owner', 'admin')
                THEN 'organization_role'
            WHEN selected_membership.distance = 0
                THEN 'direct_membership'
            ELSE 'ancestor_membership'
        END::text,
        CASE
            WHEN organization_membership.role IN ('owner', 'admin') THEN NULL
            ELSE selected_membership.team_id
        END::uuid,
        organization_membership.role
    FROM organization_memberships AS organization_membership
    JOIN accounts AS account
      ON account.id = organization_membership.account_id
    JOIN organizations AS organization
      ON organization.id = organization_membership.organization_id
    LEFT JOIN selected_membership ON true
    WHERE organization_membership.organization_id = target_organization_id
      AND organization_membership.account_id = target_account_id
      AND account.status = 'active'
      AND organization.deleted_at IS NULL
      AND (
          organization_membership.role IN ('owner', 'admin')
          OR selected_membership.role IS NOT NULL
      )
$$;

CREATE FUNCTION effective_team_role(
    target_organization_id uuid,
    target_team_id uuid,
    target_account_id uuid
)
RETURNS text
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT effective_role
    FROM effective_team_access(
        target_organization_id,
        target_team_id,
        target_account_id
    )
$$;

CREATE FUNCTION effective_team_access_source(
    target_organization_id uuid,
    target_team_id uuid,
    target_account_id uuid
)
RETURNS text
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT access_source
    FROM effective_team_access(
        target_organization_id,
        target_team_id,
        target_account_id
    )
$$;

CREATE FUNCTION effective_team_access_source_team(
    target_organization_id uuid,
    target_team_id uuid,
    target_account_id uuid
)
RETURNS uuid
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT source_team_id
    FROM effective_team_access(
        target_organization_id,
        target_team_id,
        target_account_id
    )
$$;

CREATE FUNCTION effective_team_organization_role(
    target_organization_id uuid,
    target_team_id uuid,
    target_account_id uuid
)
RETURNS text
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT organization_role
    FROM effective_team_access(
        target_organization_id,
        target_team_id,
        target_account_id
    )
$$;
