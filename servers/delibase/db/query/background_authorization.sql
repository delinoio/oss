-- name: CreateBackgroundUsageAuthorization :one
INSERT INTO background_usage_authorizations (
    id,
    authorizer_account_id,
    owner_type,
    owner_account_id,
    owner_organization_id,
    organization_id,
    team_id,
    service_identity_id,
    meter_id,
    purpose,
    feature_resource_id,
    period,
    maximum_units,
    actor_reference
) VALUES (
    sqlc.arg(id),
    sqlc.arg(authorizer_account_id),
    sqlc.arg(owner_type),
    sqlc.narg(owner_account_id),
    sqlc.narg(owner_organization_id),
    sqlc.arg(organization_id),
    sqlc.arg(team_id),
    sqlc.arg(service_identity_id),
    sqlc.arg(meter_id),
    sqlc.arg(purpose),
    sqlc.arg(feature_resource_id),
    sqlc.arg(period),
    sqlc.arg(maximum_units),
    sqlc.arg(actor_reference)
)
RETURNING *;

-- name: GetBackgroundUsageAuthorization :one
SELECT *
FROM background_usage_authorizations
WHERE id = sqlc.arg(authorization_id);

-- name: GetVisibleBackgroundUsageAuthorization :one
SELECT grant_row.*
FROM background_usage_authorizations AS grant_row
WHERE grant_row.id = sqlc.arg(authorization_id)
  AND (
      (
          grant_row.authorizer_account_id = sqlc.arg(caller_account_id)
          AND EXISTS (
              SELECT 1
              FROM organization_memberships AS caller_membership
              JOIN organizations AS caller_organization
                ON caller_organization.id = caller_membership.organization_id
              WHERE caller_membership.organization_id
                      = grant_row.organization_id
                AND caller_membership.account_id
                      = sqlc.arg(caller_account_id)
                AND caller_organization.deleted_at IS NULL
          )
      )
      OR (
          sqlc.arg(full_organization_access)::boolean
          AND grant_row.organization_id = sqlc.arg(organization_id)
      )
  );

-- name: ListVisibleBackgroundUsageAuthorizations :many
SELECT grant_row.*
FROM background_usage_authorizations AS grant_row
WHERE grant_row.id > sqlc.arg(after_id)
  AND EXISTS (
      SELECT 1
      FROM organization_memberships AS caller_membership
      JOIN organizations AS caller_organization
        ON caller_organization.id = caller_membership.organization_id
      WHERE caller_membership.organization_id = grant_row.organization_id
        AND caller_membership.account_id = sqlc.arg(caller_account_id)
        AND (
            grant_row.authorizer_account_id = sqlc.arg(caller_account_id)
            OR caller_membership.role IN ('owner', 'admin')
        )
        AND caller_organization.deleted_at IS NULL
  )
  AND (
      sqlc.arg(owner_type)::text = ''
      OR grant_row.owner_type = sqlc.arg(owner_type)
  )
  AND (
      sqlc.narg(owner_account_id)::uuid IS NULL
      OR grant_row.owner_account_id = sqlc.narg(owner_account_id)
  )
  AND (
      sqlc.narg(owner_organization_id)::uuid IS NULL
      OR grant_row.owner_organization_id
          = sqlc.narg(owner_organization_id)
  )
  AND (
      sqlc.narg(organization_id)::uuid IS NULL
      OR grant_row.organization_id = sqlc.narg(organization_id)
  )
  AND (
      sqlc.narg(team_id)::uuid IS NULL
      OR grant_row.team_id = sqlc.narg(team_id)
  )
  AND (
      sqlc.narg(service_identity_id)::uuid IS NULL
      OR grant_row.service_identity_id = sqlc.narg(service_identity_id)
  )
  AND (
      sqlc.narg(meter_id)::uuid IS NULL
      OR grant_row.meter_id = sqlc.narg(meter_id)
  )
  AND (
      sqlc.arg(purpose)::text = ''
      OR grant_row.purpose = sqlc.arg(purpose)
  )
  AND (
      sqlc.narg(feature_resource_id)::uuid IS NULL
      OR grant_row.feature_resource_id = sqlc.narg(feature_resource_id)
  )
  AND (
      sqlc.arg(status)::text = ''
      OR grant_row.status = sqlc.arg(status)
  )
ORDER BY grant_row.id
LIMIT sqlc.arg(page_limit);

-- name: LockBackgroundUsageAuthorizationForMutation :one
SELECT *
FROM background_usage_authorizations
WHERE id = sqlc.arg(authorization_id)
FOR UPDATE;

-- name: LockBackgroundUsageAuthorizationForReserve :one
WITH payer AS MATERIALIZED (
    SELECT organization.id
    FROM organizations AS organization
    JOIN background_usage_authorizations AS grant_row
      ON grant_row.organization_id = organization.id
    WHERE grant_row.id = sqlc.arg(authorization_id)
      AND organization.deleted_at IS NULL
    FOR UPDATE OF organization
)
SELECT grant_row.*
FROM background_usage_authorizations AS grant_row
JOIN payer ON payer.id = grant_row.organization_id
WHERE grant_row.id = sqlc.arg(authorization_id)
  AND grant_row.service_identity_id = sqlc.arg(service_identity_id)
  AND grant_row.purpose = sqlc.arg(purpose)
  AND grant_row.feature_resource_id = sqlc.arg(feature_resource_id)
  AND grant_row.period = sqlc.arg(period)
  AND grant_row.status = 'active'
  AND background_usage_authorization_access_is_current(grant_row)
FOR UPDATE OF grant_row;

-- name: RevokeBackgroundUsageAuthorization :one
UPDATE background_usage_authorizations
SET status = 'revoked',
    revision = revision + 1,
    actor_reference = sqlc.arg(actor_reference)
WHERE id = sqlc.arg(authorization_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'active'
RETURNING *;

-- name: MarkBackgroundUsageAuthorizationResourceDeleted :one
UPDATE background_usage_authorizations
SET status = 'resource_deleted',
    revision = revision + 1,
    actor_reference = ''
WHERE id = sqlc.arg(authorization_id)
  AND service_identity_id = sqlc.arg(service_identity_id)
  AND purpose = sqlc.arg(purpose)
  AND feature_resource_id = sqlc.arg(feature_resource_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'active'
RETURNING *;

-- name: GetBackgroundUsagePeriodUsage :one
SELECT
    grant_row.id AS authorization_id,
    grant_row.purpose,
    grant_row.feature_resource_id,
    grant_row.period,
    sqlc.arg(period_start)::timestamptz AS period_start,
    grant_row.maximum_units,
    COALESCE(held.units, 0)::bigint AS held_units,
    COALESCE(committed.units, 0)::bigint AS committed_units,
    COALESCE(
        committed.has_settlement,
        false
    )::boolean AS has_committed_settlement,
    GREATEST(
        grant_row.maximum_units
            - COALESCE(held.units, 0)
            - COALESCE(committed.units, 0),
        0
    )::bigint AS remaining_units,
    (
        sqlc.arg(period_start)::timestamptz + interval '1 day'
    )::timestamptz AS period_end,
    GREATEST(
        grant_row.updated_at,
        COALESCE(held.updated_at, '-infinity'::timestamptz),
        COALESCE(committed.updated_at, '-infinity'::timestamptz)
    )::timestamptz AS updated_at
FROM background_usage_authorizations AS grant_row
LEFT JOIN LATERAL (
    SELECT
        COALESCE(sum(reservation.maximum_units), 0)::bigint AS units,
        max(reservation.created_at) AS updated_at
    FROM usage_reservations AS reservation
    WHERE reservation.background_usage_authorization_id = grant_row.id
      AND reservation.background_period_start
          = sqlc.arg(period_start)::timestamptz
      AND reservation.status = 'held'
) AS held ON true
LEFT JOIN LATERAL (
    SELECT
        COALESCE(sum(record.committed_units), 0)::bigint AS units,
        max(record.committed_at) AS updated_at,
        (count(*) > 0)::boolean AS has_settlement
    FROM usage_records AS record
    WHERE record.background_usage_authorization_id = grant_row.id
      AND record.background_period_start
          = sqlc.arg(period_start)::timestamptz
) AS committed ON true
WHERE grant_row.id = sqlc.arg(authorization_id)
  AND sqlc.arg(period_start)::timestamptz = (
      date_trunc(
          'day',
          sqlc.arg(period_start)::timestamptz AT TIME ZONE 'UTC'
      ) AT TIME ZONE 'UTC'
  );

-- name: InsertAuthorizedUsageReservation :one
INSERT INTO usage_reservations (
    id,
    organization_id,
    team_id,
    team_name_snapshot,
    meter_id,
    price_version_id,
    account_id,
    service_identity_id,
    maximum_units,
    usd_micros_per_unit,
    maximum_cost_micros,
    held_credit_micros,
    held_overage_micros,
    client_reference,
    expires_at,
    user_actor_reference_snapshot,
    background_usage_authorization_id,
    background_usage_purpose,
    background_feature_resource_id,
    background_usage_period,
    background_period_start
) VALUES (
    sqlc.arg(id),
    sqlc.arg(organization_id),
    sqlc.arg(team_id),
    sqlc.arg(team_name_snapshot),
    sqlc.arg(meter_id),
    sqlc.arg(price_version_id),
    sqlc.arg(account_id),
    sqlc.arg(service_identity_id),
    sqlc.arg(maximum_units),
    sqlc.arg(usd_micros_per_unit),
    sqlc.arg(maximum_cost_micros),
    sqlc.arg(held_credit_micros),
    sqlc.arg(held_overage_micros),
    sqlc.arg(client_reference),
    statement_timestamp() + sqlc.arg(reservation_ttl_seconds)::bigint
        * interval '1 second',
    sqlc.arg(user_actor_reference_snapshot),
    sqlc.arg(background_usage_authorization_id),
    sqlc.arg(background_usage_purpose),
    sqlc.arg(background_feature_resource_id),
    sqlc.arg(background_usage_period),
    sqlc.arg(background_period_start)
)
RETURNING *;

-- name: LockAuthorizedUsageReservation :one
SELECT reservation.*
FROM usage_reservations AS reservation
WHERE reservation.id = sqlc.arg(reservation_id)
  AND reservation.background_usage_authorization_id
      = sqlc.arg(background_usage_authorization_id)
  AND reservation.service_identity_id = sqlc.arg(service_identity_id)
  AND reservation.background_usage_purpose = sqlc.arg(purpose)
  AND reservation.background_feature_resource_id
      = sqlc.arg(feature_resource_id)
  AND reservation.background_usage_period = sqlc.arg(period)
  AND reservation.background_period_start = sqlc.arg(period_start)
FOR UPDATE;

-- name: ListBackgroundUsageAuthorizationTransitions :many
SELECT *
FROM background_usage_authorization_transitions
WHERE authorization_id = sqlc.arg(authorization_id)
ORDER BY revision;

-- name: AppendBackgroundUsageAuthorizationAudit :one
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
    reservation_id,
    decision,
    result,
    safe_error_class,
    metadata,
    retain_until
) VALUES (
    sqlc.arg(id),
    sqlc.arg(occurred_at),
    sqlc.arg(event_type),
    sqlc.arg(actor_reference),
    sqlc.narg(organization_id),
    sqlc.narg(team_id),
    sqlc.narg(team_name_snapshot),
    sqlc.narg(service_identity_id),
    sqlc.narg(meter_id),
    sqlc.arg(background_usage_authorization_id),
    sqlc.narg(reservation_id),
    sqlc.arg(decision),
    sqlc.arg(result),
    sqlc.narg(safe_error_class),
    sqlc.arg(metadata),
    sqlc.arg(occurred_at)::timestamptz + interval '7 years'
)
RETURNING *;
