-- name: GetUsageServiceIdentity :one
SELECT *
FROM service_identities
WHERE logto_client_id = sqlc.arg(logto_client_id);

-- name: GetUsageAccountBySubject :one
SELECT *
FROM accounts
WHERE logto_subject = sqlc.arg(logto_subject);

-- name: GetUsageMeterAuthorization :one
SELECT
    meter.id AS meter_id,
    meter.name AS meter_name,
    meter.reservation_ttl_seconds,
    price.id AS price_version_id,
    price.usd_micros_per_unit,
    price.effective_from,
    price.effective_until,
    mapping.polar_meter_id
FROM service_meter_allowlists AS allowlist
JOIN service_identities AS service
  ON service.id = allowlist.service_identity_id
JOIN catalog_meters AS meter ON meter.id = allowlist.meter_id
JOIN catalog_apps AS app ON app.id = meter.app_id
JOIN polar_meter_mappings AS mapping ON mapping.meter_id = meter.id
JOIN LATERAL (
    SELECT version.id, version.usd_micros_per_unit,
           version.effective_from, version.effective_until
    FROM catalog_price_versions AS version
    WHERE version.meter_id = meter.id
      AND version.effective_from <= statement_timestamp()
      AND (
          version.effective_until IS NULL
          OR statement_timestamp() < version.effective_until
      )
    ORDER BY version.effective_from DESC
    LIMIT 1
) AS price ON true
WHERE allowlist.service_identity_id = sqlc.arg(service_identity_id)
  AND allowlist.meter_id = sqlc.arg(meter_id)
  AND allowlist.enabled
  AND service.enabled
  AND meter.enabled
  AND app.enabled;

-- name: GetUsageCapacity :one
WITH settled_credit AS (
    SELECT COALESCE(sum(amount_micros), 0)::bigint AS amount
    FROM ledger_entries
    WHERE organization_id = sqlc.arg(organization_id)
      AND entry_type IN (
          'credit_grant', 'credit_reversal', 'credit_commit',
          'credit_forfeiture'
      )
), credit_holds AS (
    SELECT COALESCE(sum(held_credit_micros), 0)::bigint AS amount
    FROM usage_reservations
    WHERE organization_id = sqlc.arg(organization_id)
      AND status = 'held'
), selected_subscription AS (
    SELECT subscription.*
    FROM subscriptions AS subscription
    WHERE subscription.organization_id = sqlc.arg(organization_id)
    ORDER BY
        (subscription.status = 'active') DESC,
        subscription.provider_event_at DESC,
        subscription.updated_at DESC,
        subscription.id DESC
    LIMIT 1
), current_period AS (
    SELECT period.*
    FROM billing_periods AS period
    JOIN selected_subscription AS subscription
      ON subscription.organization_id = period.organization_id
     AND subscription.id = period.subscription_id
    WHERE subscription.status = 'active'
      AND subscription.current_period_starts_at = period.starts_at
      AND subscription.current_period_ends_at = period.ends_at
      AND period.starts_at <= statement_timestamp()
      AND period.ends_at > statement_timestamp()
    LIMIT 1
), committed_overage AS (
    SELECT COALESCE(sum(record.overage_applied_micros), 0)::bigint AS amount
    FROM usage_records AS record
    JOIN current_period ON record.billing_period_id = current_period.id
    WHERE record.organization_id = sqlc.arg(organization_id)
), overage_holds AS (
    SELECT COALESCE(sum(reservation.held_overage_micros), 0)::bigint AS amount
    FROM usage_reservations AS reservation
    JOIN current_period
      ON reservation.overage_billing_period_id = current_period.id
    WHERE reservation.organization_id = sqlc.arg(organization_id)
      AND reservation.status = 'held'
), refund_shortfall AS (
    SELECT COALESCE(sum(shortfall.amount_micros), 0)::bigint AS amount
    FROM billing_shortfalls AS shortfall
    JOIN current_period ON shortfall.billing_period_id = current_period.id
    WHERE shortfall.organization_id = sqlc.arg(organization_id)
)
SELECT
    settled_credit.amount AS settled_credit_micros,
    credit_holds.amount AS held_credit_micros,
    GREATEST(settled_credit.amount - credit_holds.amount, 0)::bigint
        AS available_credit_micros,
    COALESCE(selected_subscription.status, 'none')::text
        AS subscription_status,
    organization.overage_limit_configured,
    current_period.id AS billing_period_id,
    current_period.starts_at AS period_starts_at,
    current_period.ends_at AS period_ends_at,
    COALESCE(current_period.requested_overage_limit_micros, 0)::bigint
        AS requested_overage_limit_micros,
    COALESCE(current_period.overage_limit_micros, 0)::bigint
        AS effective_overage_limit_micros,
    committed_overage.amount AS committed_overage_micros,
    overage_holds.amount AS held_overage_micros,
    refund_shortfall.amount AS refund_shortfall_micros,
    COALESCE(
        current_period.ends_at
            >= statement_timestamp()
                + sqlc.arg(reservation_ttl_seconds)::bigint * interval '1 second',
        false
    )::boolean AS overage_ttl_allowed,
    GREATEST(
        COALESCE(current_period.requested_overage_limit_micros, 0)
        - committed_overage.amount
        - overage_holds.amount
        - refund_shortfall.amount,
        0
    )::bigint AS available_overage_micros
FROM organizations AS organization
CROSS JOIN settled_credit
CROSS JOIN credit_holds
LEFT JOIN selected_subscription ON true
LEFT JOIN current_period ON true
CROSS JOIN committed_overage
CROSS JOIN overage_holds
CROSS JOIN refund_shortfall
WHERE organization.id = sqlc.arg(organization_id)
  AND organization.deleted_at IS NULL;

-- name: InsertUsageReservation :one
INSERT INTO usage_reservations (
    id, organization_id, team_id, team_name_snapshot, meter_id,
    price_version_id, account_id, service_identity_id, maximum_units,
    usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
    held_overage_micros, client_reference, expires_at,
    user_actor_reference_snapshot
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(team_id),
    sqlc.arg(team_name_snapshot), sqlc.arg(meter_id),
    sqlc.arg(price_version_id), sqlc.arg(account_id),
    sqlc.arg(service_identity_id), sqlc.arg(maximum_units),
    sqlc.arg(usd_micros_per_unit), sqlc.arg(maximum_cost_micros),
    sqlc.arg(held_credit_micros), sqlc.arg(held_overage_micros),
    sqlc.arg(client_reference),
    statement_timestamp() + sqlc.arg(reservation_ttl_seconds)::bigint
        * interval '1 second',
    sqlc.arg(user_actor_reference_snapshot)
)
RETURNING *;

-- name: InsertUsageLedgerEntry :one
INSERT INTO ledger_entries (
    id, organization_id, billing_period_id, entry_type, amount_micros,
    balance_after_micros, reservation_id, usage_record_id,
    team_id_snapshot, team_name_snapshot, source_reference, actor_reference
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.narg(billing_period_id),
    sqlc.arg(entry_type), sqlc.arg(amount_micros),
    sqlc.arg(balance_after_micros), sqlc.arg(reservation_id),
    sqlc.narg(usage_record_id), sqlc.arg(team_id_snapshot),
    sqlc.arg(team_name_snapshot), sqlc.arg(source_reference),
    sqlc.arg(actor_reference)
)
RETURNING *;

-- name: LockUsageReservation :one
SELECT *
FROM usage_reservations
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(reservation_id)
FOR UPDATE;

-- name: ListExpiredUsageReservationsForOrganization :many
SELECT *
FROM usage_reservations
WHERE organization_id = sqlc.arg(organization_id)
  AND status = 'held'
  AND expires_at <= statement_timestamp()
ORDER BY expires_at, id
LIMIT sqlc.arg(page_limit)
FOR UPDATE;

-- name: ListExpiredUsageReservationsForAccountInOrganization :many
SELECT *
FROM usage_reservations
WHERE organization_id = sqlc.arg(organization_id)
  AND account_id = sqlc.arg(account_id)
  AND status = 'held'
  AND expires_at <= statement_timestamp()
ORDER BY expires_at, id
LIMIT sqlc.arg(page_limit)
FOR UPDATE;

-- name: ListExpiredUsageReservationCandidates :many
SELECT id, organization_id
FROM usage_reservations
WHERE status = 'held'
  AND expires_at <= statement_timestamp()
ORDER BY expires_at, id
LIMIT sqlc.arg(page_limit);

-- name: InsertUsageRecord :one
INSERT INTO usage_records (
    id, reservation_id, organization_id, team_id, team_name_snapshot,
    meter_id, account_id, service_identity_id, committed_units,
    total_cost_micros, credit_applied_micros, overage_applied_micros
) VALUES (
    sqlc.arg(id), sqlc.arg(reservation_id), sqlc.arg(organization_id),
    sqlc.arg(team_id), sqlc.arg(team_name_snapshot), sqlc.arg(meter_id),
    sqlc.arg(account_id), sqlc.arg(service_identity_id),
    sqlc.arg(committed_units), sqlc.arg(total_cost_micros),
    sqlc.arg(credit_applied_micros), sqlc.arg(overage_applied_micros)
)
RETURNING *;

-- name: FinalizeUsageReservation :one
UPDATE usage_reservations
SET status = sqlc.arg(status)
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(reservation_id)
  AND status = 'held'
RETURNING *;

-- name: GetUsageRecordByReservation :one
SELECT *
FROM usage_records
WHERE organization_id = sqlc.arg(organization_id)
  AND reservation_id = sqlc.arg(reservation_id);

-- name: ListVisibleUsageRecords :many
SELECT
    record.*,
    CASE
        WHEN outbox.delivered_at IS NOT NULL THEN 'polar_reported'
        WHEN outbox.id IS NOT NULL THEN 'polar_pending'
        ELSE 'committed'
    END::text AS delivery_status
FROM usage_records AS record
LEFT JOIN integration_outbox AS outbox
  ON outbox.integration = 'polar'
 AND outbox.operation = 'report_usage'
 AND outbox.aggregate_type = 'usage_record'
 AND outbox.aggregate_id = record.id
WHERE record.organization_id = sqlc.arg(organization_id)
  AND record.committed_at >= sqlc.arg(from_time)
  AND record.committed_at < sqlc.arg(to_time)
  AND record.id > sqlc.arg(after_id)
  AND (
      sqlc.narg(team_id)::uuid IS NULL
      OR record.team_id = sqlc.narg(team_id)
  )
  AND (
      sqlc.narg(meter_id)::uuid IS NULL
      OR record.meter_id = sqlc.narg(meter_id)
  )
  AND (
      sqlc.narg(user_account_id)::uuid IS NULL
      OR record.account_id = sqlc.narg(user_account_id)
  )
  AND (
      sqlc.arg(full_organization_access)::boolean
      OR record.account_id = sqlc.arg(caller_account_id)
      OR effective_team_role(
          record.organization_id,
          record.team_id,
          sqlc.arg(caller_account_id)
      ) IS NOT NULL
  )
ORDER BY record.id
LIMIT sqlc.arg(page_limit);
