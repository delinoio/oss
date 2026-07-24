-- name: CreateOrganizationInvitation :one
INSERT INTO organization_invitations (
    id,
    organization_id,
    token_hash,
    organization_role,
    target_team_id,
    team_role,
    created_by_account_id,
    expires_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(organization_id),
    sqlc.arg(token_hash),
    sqlc.arg(organization_role),
    sqlc.narg(target_team_id),
    sqlc.narg(team_role),
    sqlc.arg(created_by_account_id),
    transaction_timestamp() + interval '7 days'
)
RETURNING *;

-- name: GetOrganizationInvitationByTokenHash :one
SELECT
    invitation.*,
    organization.name AS organization_name,
    COALESCE(team.name, '')::text AS team_name,
    CASE
        WHEN invitation.revoked_at IS NOT NULL THEN 'revoked'
        WHEN invitation.expires_at <= statement_timestamp() THEN 'expired'
        ELSE 'active'
    END::text AS invitation_status
FROM organization_invitations AS invitation
JOIN organizations AS organization
  ON organization.id = invitation.organization_id
LEFT JOIN teams AS team
  ON team.organization_id = invitation.organization_id
 AND team.id = invitation.target_team_id
WHERE invitation.token_hash = sqlc.arg(token_hash)
  AND organization.deleted_at IS NULL;

-- name: LockOrganizationInvitationByTokenHash :one
SELECT
    invitation.*,
    organization.name AS organization_name,
    COALESCE(team.name, '')::text AS team_name,
    CASE
        WHEN invitation.revoked_at IS NOT NULL THEN 'revoked'
        WHEN invitation.expires_at <= statement_timestamp() THEN 'expired'
        ELSE 'active'
    END::text AS invitation_status
FROM organization_invitations AS invitation
JOIN organizations AS organization
  ON organization.id = invitation.organization_id
LEFT JOIN teams AS team
  ON team.organization_id = invitation.organization_id
 AND team.id = invitation.target_team_id
WHERE invitation.token_hash = sqlc.arg(token_hash)
  AND organization.deleted_at IS NULL
FOR UPDATE OF invitation;

-- name: GetOrganizationInvitationForMutation :one
SELECT
    invitation.*,
    CASE
        WHEN invitation.revoked_at IS NOT NULL THEN 'revoked'
        WHEN invitation.expires_at <= statement_timestamp() THEN 'expired'
        ELSE 'active'
    END::text AS invitation_status
FROM organization_invitations AS invitation
JOIN organizations AS organization
  ON organization.id = invitation.organization_id
WHERE invitation.organization_id = sqlc.arg(organization_id)
  AND invitation.id = sqlc.arg(invitation_id)
  AND organization.deleted_at IS NULL
FOR UPDATE OF invitation;

-- name: ListOrganizationInvitations :many
SELECT
    invitation.*,
    CASE
        WHEN invitation.revoked_at IS NOT NULL THEN 'revoked'
        WHEN invitation.expires_at <= statement_timestamp() THEN 'expired'
        ELSE 'active'
    END::text AS invitation_status
FROM organization_invitations AS invitation
JOIN organizations AS organization
  ON organization.id = invitation.organization_id
WHERE invitation.organization_id = sqlc.arg(organization_id)
  AND invitation.id > sqlc.arg(after_id)
  AND organization.deleted_at IS NULL
  AND (
      sqlc.arg(status)::text = ''
      OR sqlc.arg(status)::text = CASE
          WHEN invitation.revoked_at IS NOT NULL THEN 'revoked'
          WHEN invitation.expires_at <= statement_timestamp() THEN 'expired'
          ELSE 'active'
      END
  )
ORDER BY invitation.id
LIMIT sqlc.arg(page_limit);

-- name: CreateOrganizationInvitationAcceptance :execrows
INSERT INTO organization_invitation_acceptances (invitation_id, account_id)
VALUES (sqlc.arg(invitation_id), sqlc.arg(account_id))
ON CONFLICT (invitation_id, account_id) DO NOTHING;

-- name: CreateOrganizationMembershipIfAbsent :execrows
INSERT INTO organization_memberships (organization_id, account_id, role)
VALUES (
    sqlc.arg(organization_id),
    sqlc.arg(account_id),
    sqlc.arg(role)
)
ON CONFLICT (organization_id, account_id) DO NOTHING;

-- name: RevokeOrganizationInvitation :one
UPDATE organization_invitations
SET revoked_at = COALESCE(revoked_at, transaction_timestamp())
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(invitation_id)
RETURNING *;
