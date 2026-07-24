-- name: GetTeamByID :one
SELECT *
FROM teams
WHERE id = sqlc.arg(id);

-- name: GetTeamInOrganization :one
SELECT team.*, team_depth(team.organization_id, team.id) AS depth
FROM teams AS team
WHERE team.organization_id = sqlc.arg(organization_id)
  AND team.id = sqlc.arg(team_id);

-- name: ListTeamsForAccount :many
SELECT team.*, team_depth(team.organization_id, team.id) AS depth
FROM teams AS team
JOIN organization_memberships AS caller
  ON caller.organization_id = team.organization_id
 AND caller.account_id = sqlc.arg(account_id)
JOIN accounts AS caller_account ON caller_account.id = caller.account_id
JOIN organizations AS organization ON organization.id = team.organization_id
WHERE team.organization_id = sqlc.arg(organization_id)
  AND team.id > sqlc.arg(after_id)
  AND caller_account.status = 'active'
  AND organization.deleted_at IS NULL
  AND (
      (
          sqlc.narg(parent_team_id)::uuid IS NULL
          AND (
              sqlc.arg(include_descendants)::boolean
              OR team.parent_team_id IS NULL
          )
      )
      OR
      (
          sqlc.narg(parent_team_id)::uuid IS NOT NULL
          AND (
              team.parent_team_id = sqlc.narg(parent_team_id)
              OR (
                  sqlc.arg(include_descendants)::boolean
                  AND team.id <> sqlc.narg(parent_team_id)
                  AND team_is_descendant_or_self(
                      team.organization_id,
                      team.id,
                      sqlc.narg(parent_team_id)
                  )
              )
          )
      )
  )
  AND (
      caller.role IN ('owner', 'admin')
      OR EXISTS (
          SELECT 1
          FROM effective_team_access(
              team.organization_id,
              team.id,
              caller.account_id
          )
      )
  )
ORDER BY team.id
LIMIT sqlc.arg(page_limit);

-- name: GetEffectiveTeamAccess :one
SELECT
    sqlc.arg(team_id)::uuid AS team_id,
    sqlc.arg(account_id)::uuid AS account_id,
    effective_team_role(
        sqlc.arg(organization_id), sqlc.arg(team_id), sqlc.arg(account_id)
    ) AS effective_role,
    effective_team_access_source(
        sqlc.arg(organization_id), sqlc.arg(team_id), sqlc.arg(account_id)
    ) AS access_source,
    effective_team_access_source_team(
        sqlc.arg(organization_id), sqlc.arg(team_id), sqlc.arg(account_id)
    ) AS source_team_id,
    effective_team_organization_role(
        sqlc.arg(organization_id), sqlc.arg(team_id), sqlc.arg(account_id)
    ) AS organization_role
WHERE effective_team_role(
    sqlc.arg(organization_id), sqlc.arg(team_id), sqlc.arg(account_id)
) IS NOT NULL;

-- name: ListEffectiveTeamAccess :many
SELECT
    team.id AS team_id,
    sqlc.arg(account_id)::uuid AS account_id,
    effective_team_role(
        team.organization_id, team.id, sqlc.arg(account_id)
    ) AS effective_role,
    effective_team_access_source(
        team.organization_id, team.id, sqlc.arg(account_id)
    ) AS access_source,
    effective_team_access_source_team(
        team.organization_id, team.id, sqlc.arg(account_id)
    ) AS source_team_id,
    effective_team_organization_role(
        team.organization_id, team.id, sqlc.arg(account_id)
    ) AS organization_role
FROM teams AS team
WHERE team.organization_id = sqlc.arg(organization_id)
  AND team.id > sqlc.arg(after_id)
  AND effective_team_role(
      team.organization_id, team.id, sqlc.arg(account_id)
  ) IS NOT NULL
ORDER BY team.id
LIMIT sqlc.arg(page_limit);

-- name: CreateTeam :one
INSERT INTO teams (id, organization_id, parent_team_id, name)
VALUES (
    sqlc.arg(id),
    sqlc.arg(organization_id),
    sqlc.narg(parent_team_id),
    sqlc.arg(name)
)
RETURNING *;

-- name: UpdateTeamName :one
UPDATE teams
SET name = sqlc.arg(name),
    updated_at = transaction_timestamp()
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(team_id)
RETURNING *;

-- name: MoveTeam :one
UPDATE teams
SET parent_team_id = sqlc.narg(parent_team_id),
    updated_at = transaction_timestamp()
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(team_id)
RETURNING *;

-- name: ListTeamSubtree :many
SELECT
    team.*,
    (
        team_depth(team.organization_id, team.id)
        - team_depth(team.organization_id, sqlc.arg(team_id))
    )::integer AS relative_depth
FROM teams AS team
WHERE team.organization_id = sqlc.arg(organization_id)
  AND team_is_descendant_or_self(
      team.organization_id,
      team.id,
      sqlc.arg(team_id)
  )
ORDER BY relative_depth DESC, team.id;

-- name: HasActiveReservationsForTeamSubtree :one
SELECT EXISTS (
    SELECT 1
    FROM usage_reservations AS reservation
    WHERE reservation.organization_id = sqlc.arg(organization_id)
      AND reservation.status = 'held'
      AND team_is_descendant_or_self(
          reservation.organization_id,
          reservation.team_id,
          sqlc.arg(team_id)
      )
);

-- name: DeleteTeamSubtree :execrows
DELETE FROM teams
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(team_id);

-- name: ListTeamMemberships :many
SELECT
    membership.organization_id,
    membership.team_id,
    membership.account_id,
    account.display_name,
    membership.role,
    membership.created_at,
    membership.updated_at
FROM team_memberships AS membership
JOIN accounts AS account ON account.id = membership.account_id
WHERE membership.organization_id = sqlc.arg(organization_id)
  AND membership.team_id = sqlc.arg(team_id)
  AND membership.account_id > sqlc.arg(after_id)
  AND account.status = 'active'
ORDER BY membership.account_id
LIMIT sqlc.arg(page_limit);

-- name: GetTeamMembership :one
SELECT
    membership.organization_id,
    membership.team_id,
    membership.account_id,
    account.display_name,
    membership.role,
    membership.created_at,
    membership.updated_at
FROM team_memberships AS membership
JOIN accounts AS account ON account.id = membership.account_id
WHERE membership.organization_id = sqlc.arg(organization_id)
  AND membership.team_id = sqlc.arg(team_id)
  AND membership.account_id = sqlc.arg(account_id)
  AND account.status = 'active';

-- name: UpsertTeamMembership :one
INSERT INTO team_memberships (organization_id, team_id, account_id, role)
VALUES (
    sqlc.arg(organization_id),
    sqlc.arg(team_id),
    sqlc.arg(account_id),
    sqlc.arg(role)
)
ON CONFLICT (team_id, account_id) DO UPDATE
SET role = EXCLUDED.role,
    updated_at = transaction_timestamp()
RETURNING *;

-- name: InsertTeamMembershipIfAbsent :execrows
INSERT INTO team_memberships (organization_id, team_id, account_id, role)
VALUES (
    sqlc.arg(organization_id),
    sqlc.arg(team_id),
    sqlc.arg(account_id),
    sqlc.arg(role)
)
ON CONFLICT (team_id, account_id) DO NOTHING;

-- name: DeleteTeamMembership :execrows
DELETE FROM team_memberships
WHERE organization_id = sqlc.arg(organization_id)
  AND team_id = sqlc.arg(team_id)
  AND account_id = sqlc.arg(account_id);
