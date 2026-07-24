-- name: EnsureAccount :one
INSERT INTO accounts (id, logto_subject, display_name)
VALUES (sqlc.arg(id), sqlc.arg(logto_subject), sqlc.arg(display_name))
ON CONFLICT (logto_subject) DO UPDATE
SET logto_subject = EXCLUDED.logto_subject
RETURNING *;

-- name: GetAccountByID :one
SELECT *
FROM accounts
WHERE id = sqlc.arg(id);

-- name: LockAccountByLogtoSubject :one
SELECT *
FROM accounts
WHERE logto_subject = sqlc.arg(logto_subject)
FOR UPDATE;

-- name: UpdateAccountDisplayName :one
UPDATE accounts
SET display_name = sqlc.arg(display_name),
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND status = 'active'
RETURNING *;

-- name: DisableAndEraseAccount :one
UPDATE accounts
SET display_name = '',
    status = 'disabled',
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND status = 'active'
RETURNING *;

-- name: DeleteDisabledAccount :execrows
DELETE FROM accounts
WHERE id = sqlc.arg(id)
  AND status = 'disabled';

-- name: ListAccountOrganizations :many
SELECT
    organization.id,
    organization.name,
    organization.slug,
    membership.role
FROM organization_memberships AS membership
JOIN organizations AS organization
  ON organization.id = membership.organization_id
WHERE membership.account_id = sqlc.arg(account_id)
  AND organization.deleted_at IS NULL
ORDER BY organization.id;

-- name: HasAccountOrganization :one
SELECT EXISTS (
    SELECT 1
    FROM organization_memberships AS membership
    JOIN organizations AS organization
      ON organization.id = membership.organization_id
    WHERE membership.account_id = sqlc.arg(account_id)
      AND organization.deleted_at IS NULL
);

-- name: LockOwnedOrganizations :many
SELECT organization.id
FROM organizations AS organization
JOIN organization_memberships AS membership
  ON membership.organization_id = organization.id
WHERE membership.account_id = sqlc.arg(account_id)
  AND membership.role = 'owner'
  AND organization.deleted_at IS NULL
ORDER BY organization.id
FOR UPDATE OF organization;

-- name: ListLastOwnerBlockers :many
SELECT organization.id, organization.name
FROM organizations AS organization
JOIN organization_memberships AS membership
  ON membership.organization_id = organization.id
WHERE membership.account_id = sqlc.arg(account_id)
  AND membership.role = 'owner'
  AND organization.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM organization_memberships AS other_membership
      JOIN accounts AS other_account
        ON other_account.id = other_membership.account_id
      WHERE other_membership.organization_id = organization.id
        AND other_membership.role = 'owner'
        AND other_membership.account_id <> sqlc.arg(account_id)
        AND other_account.status = 'active'
  )
ORDER BY organization.id;

-- name: ListActiveReservationBlockersForAccount :many
SELECT DISTINCT
    organization.id,
    organization.name,
    team.id AS team_id,
    team.name AS team_name
FROM usage_reservations AS reservation
JOIN organizations AS organization
  ON organization.id = reservation.organization_id
JOIN teams AS team
  ON team.organization_id = reservation.organization_id
 AND team.id = reservation.team_id
WHERE reservation.account_id = sqlc.arg(account_id)
  AND reservation.status = 'held'
ORDER BY organization.id, team.id;

-- name: DeleteAccountMemberships :execrows
DELETE FROM organization_memberships
WHERE account_id = sqlc.arg(account_id);

-- name: CreateOrganization :one
INSERT INTO organizations (id, name, slug)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(slug))
RETURNING *;

-- name: CreateOrganizationMembership :one
INSERT INTO organization_memberships (organization_id, account_id, role)
VALUES (
    sqlc.arg(organization_id),
    sqlc.arg(account_id),
    sqlc.arg(role)
)
RETURNING *;

-- name: CreateGeneralTeam :one
INSERT INTO teams (id, organization_id, name, protected_general)
VALUES (sqlc.arg(id), sqlc.arg(organization_id), 'General', true)
RETURNING *;

-- name: CreateTeamMembership :one
INSERT INTO team_memberships (organization_id, team_id, account_id, role)
VALUES (
    sqlc.arg(organization_id),
    sqlc.arg(team_id),
    sqlc.arg(account_id),
    sqlc.arg(role)
)
RETURNING *;

-- name: CreatePendingPolarCustomer :one
INSERT INTO polar_customers (organization_id, polar_customer_id)
VALUES (
    sqlc.arg(organization_id)::uuid,
    'pending:' || sqlc.arg(organization_id)::text
)
RETURNING *;

-- name: ListOrganizationsForAccount :many
SELECT organization.*
FROM organizations AS organization
JOIN organization_memberships AS membership
  ON membership.organization_id = organization.id
WHERE membership.account_id = sqlc.arg(account_id)
  AND organization.deleted_at IS NULL
  AND organization.id > sqlc.arg(after_id)
ORDER BY organization.id
LIMIT sqlc.arg(page_limit);

-- name: GetOrganizationForAccount :one
SELECT organization.*, membership.role AS caller_role
FROM organizations AS organization
JOIN organization_memberships AS membership
  ON membership.organization_id = organization.id
WHERE organization.id = sqlc.arg(organization_id)
  AND membership.account_id = sqlc.arg(account_id)
  AND organization.deleted_at IS NULL;

-- name: GetOrganizationByID :one
SELECT *
FROM organizations
WHERE id = sqlc.arg(id);

-- name: ResolveOrganizationSlugForAccount :one
SELECT
    organization.*,
    (organization.slug <> sqlc.arg(slug))::boolean AS matched_alias
FROM organization_slug_registry AS registry
JOIN organizations AS organization
  ON organization.id = registry.organization_id
JOIN organization_memberships AS membership
  ON membership.organization_id = organization.id
WHERE registry.slug = sqlc.arg(slug)
  AND membership.account_id = sqlc.arg(account_id)
  AND organization.deleted_at IS NULL;

-- name: UpdateOrganizationName :one
UPDATE organizations
SET name = sqlc.arg(name),
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING *;

-- name: UpdateOrganizationSlug :one
UPDATE organizations
SET slug = sqlc.arg(slug),
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING *;

-- name: MarkOrganizationDeleted :one
UPDATE organizations
SET deleted_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING *;

-- name: GetCancelablePolarSubscriptionForOrganization :one
SELECT polar_subscription_id
FROM subscriptions
WHERE organization_id = sqlc.arg(organization_id)
  AND status IN ('pending', 'active', 'past_due')
ORDER BY created_at DESC
LIMIT 1;

-- name: GetOrganizationMembership :one
SELECT membership.*
FROM organization_memberships AS membership
JOIN accounts AS account ON account.id = membership.account_id
JOIN organizations AS organization
  ON organization.id = membership.organization_id
WHERE membership.organization_id = sqlc.arg(organization_id)
  AND membership.account_id = sqlc.arg(account_id)
  AND account.status = 'active'
  AND organization.deleted_at IS NULL;

-- name: ListOrganizationMembers :many
SELECT
    membership.organization_id,
    membership.account_id,
    account.display_name,
    membership.role,
    membership.created_at,
    membership.updated_at
FROM organization_memberships AS membership
JOIN accounts AS account ON account.id = membership.account_id
JOIN organizations AS organization
  ON organization.id = membership.organization_id
WHERE membership.organization_id = sqlc.arg(organization_id)
  AND membership.account_id > sqlc.arg(after_id)
  AND account.status = 'active'
  AND organization.deleted_at IS NULL
ORDER BY membership.account_id
LIMIT sqlc.arg(page_limit);

-- name: GetOrganizationMember :one
SELECT
    membership.organization_id,
    membership.account_id,
    account.display_name,
    membership.role,
    membership.created_at,
    membership.updated_at
FROM organization_memberships AS membership
JOIN accounts AS account ON account.id = membership.account_id
JOIN organizations AS organization
  ON organization.id = membership.organization_id
WHERE membership.organization_id = sqlc.arg(organization_id)
  AND membership.account_id = sqlc.arg(account_id)
  AND account.status = 'active'
  AND organization.deleted_at IS NULL;

-- name: UpdateOrganizationMembershipRole :one
UPDATE organization_memberships
SET role = sqlc.arg(role),
    updated_at = transaction_timestamp()
WHERE organization_id = sqlc.arg(organization_id)
  AND account_id = sqlc.arg(account_id)
RETURNING *;

-- name: DeleteOrganizationMembership :execrows
DELETE FROM organization_memberships
WHERE organization_id = sqlc.arg(organization_id)
  AND account_id = sqlc.arg(account_id);

-- name: CurrentOrganizationBalance :one
SELECT COALESCE((
    SELECT balance_after_micros
    FROM ledger_entries
    WHERE organization_id = sqlc.arg(organization_id)
    ORDER BY created_at DESC, id DESC
    LIMIT 1
), 0)::bigint AS balance_micros;

-- name: ForfeitOrganizationCredit :one
INSERT INTO ledger_entries (
    id,
    organization_id,
    entry_type,
    amount_micros,
    balance_after_micros,
    source_reference,
    actor_reference
) VALUES (
    sqlc.arg(id),
    sqlc.arg(organization_id),
    'credit_forfeiture',
    -sqlc.arg(amount_micros)::bigint,
    0,
    sqlc.arg(source_reference),
    sqlc.arg(actor_reference)
)
RETURNING *;

-- name: InsertDeletionTombstone :one
INSERT INTO deletion_tombstones (
    entity_type,
    entity_id,
    actor_reference
) VALUES (
    sqlc.arg(entity_type),
    sqlc.arg(entity_id),
    sqlc.arg(actor_reference)
)
ON CONFLICT (entity_type, entity_id) DO NOTHING
RETURNING *;

-- name: InsertDeletedAccountSubject :one
INSERT INTO deleted_account_subjects (
    subject_digest,
    account_id,
    actor_reference
) VALUES (
    sqlc.arg(subject_digest),
    sqlc.arg(account_id),
    sqlc.arg(actor_reference)
)
ON CONFLICT (subject_digest) DO NOTHING
RETURNING *;

-- name: GetDeletedAccountSubject :one
SELECT *
FROM deleted_account_subjects
WHERE subject_digest = sqlc.arg(subject_digest);

-- name: GetIdempotencyRecord :one
SELECT *
FROM idempotency_records
WHERE caller_kind = sqlc.arg(caller_kind)
  AND caller_id = sqlc.arg(caller_id)
  AND operation = sqlc.arg(operation)
  AND idempotency_key = sqlc.arg(idempotency_key)
  AND expires_at > transaction_timestamp();

-- name: InsertIdempotencyRecord :one
INSERT INTO idempotency_records (
    id,
    caller_kind,
    caller_id,
    operation,
    idempotency_key,
    request_hash,
    response_payload,
    connect_code,
    expires_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(caller_kind),
    sqlc.arg(caller_id),
    sqlc.arg(operation),
    sqlc.arg(idempotency_key),
    sqlc.arg(request_hash),
    sqlc.arg(response_payload),
    sqlc.arg(connect_code),
    transaction_timestamp() + interval '24 hours'
)
RETURNING *;
