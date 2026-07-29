-- name: Ping :one
SELECT 1::integer;

-- name: UpsertDeckAccount :exec
INSERT INTO deck_accounts (
    account_id, logto_subject, github_login_ciphertext, active
) VALUES (
    sqlc.arg(account_id), sqlc.arg(logto_subject),
    sqlc.arg(github_login_ciphertext), true
)
ON CONFLICT (account_id) DO UPDATE
SET logto_subject = EXCLUDED.logto_subject,
    github_login_ciphertext = EXCLUDED.github_login_ciphertext,
    active = true;

-- name: GetDeckAccountBySubject :one
SELECT account_id, logto_subject, github_login_ciphertext, active
FROM deck_accounts
WHERE logto_subject = sqlc.arg(logto_subject) AND active;

-- name: UpsertOrganizationMembership :exec
INSERT INTO deck_organization_memberships (
    organization_id, account_id, role, active
) VALUES (
    sqlc.arg(organization_id), sqlc.arg(account_id), sqlc.arg(role), true
)
ON CONFLICT (organization_id, account_id) DO UPDATE
SET role = EXCLUDED.role, active = true;

-- name: UpsertTeamMembership :exec
INSERT INTO deck_team_memberships (
    organization_id, team_id, account_id, active
) VALUES (
    sqlc.arg(organization_id), sqlc.arg(team_id), sqlc.arg(account_id), true
)
ON CONFLICT (organization_id, team_id, account_id) DO UPDATE
SET active = true;

-- name: DeactivateOrganizationMembershipsForAccount :exec
UPDATE deck_organization_memberships
SET active = false
WHERE account_id = sqlc.arg(account_id) AND active;

-- name: DeactivateTeamMembershipsForAccount :exec
UPDATE deck_team_memberships
SET active = false
WHERE account_id = sqlc.arg(account_id) AND active;

-- name: ListOrganizationMembershipsForAccount :many
SELECT organization_id, role
FROM deck_organization_memberships
WHERE account_id = sqlc.arg(account_id) AND active
ORDER BY organization_id;

-- name: ListTeamMembershipsForAccount :many
SELECT organization_id, team_id
FROM deck_team_memberships
WHERE account_id = sqlc.arg(account_id) AND active
ORDER BY organization_id, team_id;

-- name: InsertAuditEvent :exec
INSERT INTO deck_audit_events (
    audit_id, event_type, actor_pseudonym, owner_scope, target_hash,
    resource_type, resource_id, outcome, occurred_at
) VALUES (
    sqlc.arg(audit_id), sqlc.arg(event_type), sqlc.arg(actor_pseudonym),
    sqlc.narg(owner_scope), sqlc.narg(target_hash), sqlc.arg(resource_type),
    sqlc.narg(resource_id), sqlc.arg(outcome), sqlc.arg(occurred_at)
);
