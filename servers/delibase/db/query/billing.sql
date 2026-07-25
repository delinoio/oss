-- name: UpsertPolarCatalogMapping :one
INSERT INTO polar_catalog_mappings (
    singleton, polar_product_id, polar_environment
)
VALUES (
    true, sqlc.arg(polar_product_id), sqlc.arg(polar_environment)
)
ON CONFLICT (singleton) DO UPDATE
SET polar_product_id = EXCLUDED.polar_product_id,
    polar_environment = EXCLUDED.polar_environment,
    updated_at = transaction_timestamp()
WHERE polar_catalog_mappings.polar_product_id = EXCLUDED.polar_product_id
  AND polar_catalog_mappings.polar_environment = EXCLUDED.polar_environment
RETURNING *;

-- name: GetPolarCatalogMapping :one
SELECT * FROM polar_catalog_mappings WHERE singleton;

-- name: GetBillingAccess :one
SELECT
    organization.id AS organization_id,
    organization.overage_limit_micros,
    organization.overage_limit_configured,
    membership.role,
    customer.polar_customer_id
FROM organizations AS organization
JOIN organization_memberships AS membership
  ON membership.organization_id = organization.id
JOIN accounts AS account ON account.id = membership.account_id
JOIN polar_customers AS customer
  ON customer.organization_id = organization.id
WHERE organization.id = sqlc.arg(organization_id)
  AND membership.account_id = sqlc.arg(account_id)
  AND organization.deleted_at IS NULL
  AND account.status = 'active';

-- name: GetBillingSummary :one
WITH selected_subscription AS (
    SELECT *
    FROM subscriptions
    WHERE organization_id = sqlc.arg(organization_id)
    ORDER BY
        (status = 'active') DESC,
        provider_event_at DESC,
        updated_at DESC,
        id DESC
    LIMIT 1
), active_checkout AS (
    SELECT organization_id
    FROM polar_subscription_checkouts
    WHERE organization_id = sqlc.arg(organization_id)
      AND expires_at > transaction_timestamp()
), current_period AS (
    SELECT period.*
    FROM selected_subscription AS subscription
    JOIN billing_periods AS period
      ON period.organization_id = subscription.organization_id
     AND period.subscription_id = subscription.id
     AND period.starts_at = subscription.current_period_starts_at
     AND period.ends_at = subscription.current_period_ends_at
    WHERE subscription.status = 'active'
      AND period.starts_at <= transaction_timestamp()
      AND period.ends_at > transaction_timestamp()
    LIMIT 1
), settled_credit AS (
    SELECT COALESCE(sum(amount_micros), 0)::bigint AS amount
    FROM ledger_entries
    WHERE organization_id = sqlc.arg(organization_id)
      AND entry_type IN (
          'credit_grant', 'credit_reversal', 'credit_commit', 'credit_forfeiture'
      )
), active_holds AS (
    SELECT
        COALESCE(sum(held_credit_micros), 0)::bigint AS credit,
        COALESCE(sum(held_overage_micros) FILTER (
            WHERE current_period.id IS NOT NULL
              AND reservation.created_at >= current_period.starts_at
              AND reservation.created_at < current_period.ends_at
        ), 0)::bigint AS overage
    FROM usage_reservations AS reservation
    LEFT JOIN current_period ON true
    WHERE reservation.organization_id = sqlc.arg(organization_id)
      AND reservation.status = 'held'
), committed AS (
    SELECT COALESCE(sum(record.overage_applied_micros), 0)::bigint AS amount
    FROM usage_records AS record
    JOIN current_period
      ON record.committed_at >= current_period.starts_at
     AND record.committed_at < current_period.ends_at
    WHERE record.organization_id = sqlc.arg(organization_id)
), shortfall AS (
    SELECT COALESCE(sum(item.amount_micros), 0)::bigint AS amount
    FROM billing_shortfalls AS item
    JOIN current_period ON current_period.id = item.billing_period_id
    WHERE item.organization_id = sqlc.arg(organization_id)
)
SELECT
    organization.id AS organization_id,
    CASE
        WHEN selected_subscription.status = 'active' THEN 'active'
        WHEN active_checkout.organization_id IS NOT NULL THEN 'pending'
        ELSE COALESCE(selected_subscription.status, 'none')
    END::text AS subscription_status,
    current_period.id AS billing_period_id,
    current_period.starts_at,
    current_period.ends_at,
    GREATEST(settled_credit.amount - active_holds.credit, 0)::bigint
        AS available_credit_micros,
    active_holds.credit AS held_credit_micros,
    (committed.amount + shortfall.amount)::bigint AS committed_overage_micros,
    active_holds.overage AS held_overage_micros,
    COALESCE(
        current_period.requested_overage_limit_micros,
        organization.overage_limit_micros
    )::bigint
        AS monthly_overage_limit_micros,
    organization.overage_limit_configured,
    (
        selected_subscription.status = 'active'
        AND current_period.id IS NOT NULL
        AND committed.amount + shortfall.amount + active_holds.overage
            < current_period.requested_overage_limit_micros
    )::boolean AS new_overage_allowed
FROM organizations AS organization
LEFT JOIN selected_subscription ON true
LEFT JOIN active_checkout ON true
LEFT JOIN current_period ON true
CROSS JOIN settled_credit
CROSS JOIN active_holds
CROSS JOIN committed
CROSS JOIN shortfall
WHERE organization.id = sqlc.arg(organization_id)
  AND organization.deleted_at IS NULL;

-- name: UpdateOrganizationOverageLimit :one
UPDATE organizations
SET overage_limit_micros = sqlc.arg(overage_limit_micros),
    overage_limit_configured = true,
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(organization_id)
  AND deleted_at IS NULL
RETURNING *;

-- name: UpdateCurrentBillingPeriodOverageLimit :execrows
WITH current_period AS (
    SELECT id, starts_at, ends_at
    FROM billing_periods AS candidate
    WHERE candidate.organization_id = sqlc.arg(organization_id)
      AND candidate.starts_at <= transaction_timestamp()
      AND candidate.ends_at > transaction_timestamp()
    FOR UPDATE
), committed AS (
    SELECT COALESCE(sum(overage_applied_micros), 0)::bigint AS amount
    FROM usage_records AS record
    JOIN current_period
      ON record.committed_at >= current_period.starts_at
     AND record.committed_at < current_period.ends_at
    WHERE record.organization_id = sqlc.arg(organization_id)
), held AS (
    SELECT COALESCE(sum(held_overage_micros), 0)::bigint AS amount
    FROM usage_reservations AS reservation
    JOIN current_period
      ON reservation.created_at >= current_period.starts_at
     AND reservation.created_at < current_period.ends_at
    WHERE reservation.organization_id = sqlc.arg(organization_id)
      AND reservation.status = 'held'
), shortfall AS (
    SELECT COALESCE(sum(amount_micros), 0)::bigint AS amount
    FROM billing_shortfalls AS item
    JOIN current_period ON item.billing_period_id = current_period.id
    WHERE item.organization_id = sqlc.arg(organization_id)
)
UPDATE billing_periods AS period
SET requested_overage_limit_micros = sqlc.arg(overage_limit_micros),
    overage_limit_micros = GREATEST(
        sqlc.arg(overage_limit_micros),
        (SELECT amount FROM committed)
            + (SELECT amount FROM held)
            + (SELECT amount FROM shortfall)
    )
FROM current_period
WHERE period.id = current_period.id;

-- name: ListLedgerEntries :many
SELECT *
FROM ledger_entries
WHERE organization_id = sqlc.arg(organization_id)
  AND (sqlc.arg(entry_type)::text = '' OR entry_type = sqlc.arg(entry_type))
  AND created_at >= sqlc.arg(from_time)
  AND created_at < sqlc.arg(to_time)
  AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_limit);

-- name: GetPolarCustomerByExternalID :one
SELECT * FROM polar_customers WHERE external_id = sqlc.arg(external_id);

-- name: GetPolarCustomerByProviderID :one
SELECT * FROM polar_customers
WHERE polar_customer_id = sqlc.arg(polar_customer_id);

-- name: LockOrganizationForBilling :one
SELECT * FROM organizations
WHERE id = sqlc.arg(id) AND deleted_at IS NULL
FOR UPDATE;

-- name: LockOrganizationForBillingHistory :one
SELECT * FROM organizations
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: GetActivePolarSubscriptionCheckout :one
SELECT * FROM polar_subscription_checkouts
WHERE organization_id = sqlc.arg(organization_id)
  AND expires_at > transaction_timestamp()
FOR UPDATE;

-- name: UpsertPolarSubscriptionCheckout :one
INSERT INTO polar_subscription_checkouts (
    organization_id, polar_checkout_id, expires_at
) VALUES (
    sqlc.arg(organization_id), sqlc.arg(polar_checkout_id), sqlc.arg(expires_at)
)
ON CONFLICT (organization_id) DO UPDATE
SET polar_checkout_id = EXCLUDED.polar_checkout_id,
    expires_at = EXCLUDED.expires_at,
    created_at = transaction_timestamp()
WHERE polar_subscription_checkouts.expires_at <= transaction_timestamp()
RETURNING *;

-- name: GetActivePolarSubscriptionCheckoutAttempt :one
SELECT * FROM polar_subscription_checkout_attempts
WHERE organization_id = sqlc.arg(organization_id)
  AND expires_at > transaction_timestamp()
FOR UPDATE;

-- name: ClaimPolarSubscriptionCheckoutAttempt :one
INSERT INTO polar_subscription_checkout_attempts (
    organization_id, provider_idempotency_key, request_digest, expires_at
) VALUES (
    sqlc.arg(organization_id),
    sqlc.arg(provider_idempotency_key),
    sqlc.arg(request_digest),
    sqlc.arg(expires_at)
)
ON CONFLICT (organization_id) DO UPDATE
SET provider_idempotency_key = EXCLUDED.provider_idempotency_key,
    request_digest = EXCLUDED.request_digest,
    expires_at = EXCLUDED.expires_at,
    created_at = transaction_timestamp()
WHERE polar_subscription_checkout_attempts.expires_at <= transaction_timestamp()
RETURNING *;

-- name: DeletePolarSubscriptionCheckoutAttempt :execrows
DELETE FROM polar_subscription_checkout_attempts
WHERE organization_id = sqlc.arg(organization_id)
  AND provider_idempotency_key = sqlc.arg(provider_idempotency_key);

-- name: CurrentSettledCreditBalance :one
SELECT COALESCE(sum(amount_micros), 0)::bigint AS balance_micros
FROM ledger_entries
WHERE organization_id = sqlc.arg(organization_id)
  AND entry_type IN (
      'credit_grant', 'credit_reversal', 'credit_commit', 'credit_forfeiture'
  );

-- name: GetSubscriptionByPolarID :one
SELECT * FROM subscriptions WHERE polar_subscription_id = sqlc.arg(polar_subscription_id);

-- name: GetActiveSubscriptionForOrganization :one
SELECT * FROM subscriptions
WHERE organization_id = sqlc.arg(organization_id)
  AND status = 'active'
FOR UPDATE;

-- name: GetCurrentActiveBillingPeriod :one
SELECT period.*
FROM billing_periods AS period
JOIN subscriptions AS subscription
  ON subscription.organization_id = period.organization_id
 AND subscription.id = period.subscription_id
WHERE period.organization_id = sqlc.arg(organization_id)
  AND period.starts_at <= transaction_timestamp()
  AND period.ends_at > transaction_timestamp()
  AND subscription.status = 'active'
  AND subscription.current_period_starts_at = period.starts_at
  AND subscription.current_period_ends_at = period.ends_at;

-- name: InsertSubscription :one
INSERT INTO subscriptions (
    id, organization_id, polar_subscription_id, status,
    current_period_starts_at, current_period_ends_at, provider_event_at
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(polar_subscription_id),
    sqlc.arg(status), sqlc.narg(current_period_starts_at),
    sqlc.narg(current_period_ends_at), sqlc.arg(provider_event_at)
)
RETURNING *;

-- name: UpdateSubscriptionFromPolar :one
UPDATE subscriptions
SET status = sqlc.arg(status),
    current_period_starts_at = sqlc.narg(current_period_starts_at),
    current_period_ends_at = sqlc.narg(current_period_ends_at),
    provider_event_at = sqlc.arg(provider_event_at),
    updated_at = transaction_timestamp()
WHERE polar_subscription_id = sqlc.arg(polar_subscription_id)
  AND provider_event_at <= sqlc.arg(provider_event_at)
  AND status <> 'revoked'
RETURNING *;

-- name: EnsureBillingPeriod :one
INSERT INTO billing_periods (
    id, organization_id, subscription_id, starts_at, ends_at,
    overage_limit_micros, requested_overage_limit_micros
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(subscription_id),
    sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.arg(overage_limit_micros),
    sqlc.arg(overage_limit_micros)
)
ON CONFLICT (organization_id, starts_at) DO UPDATE
SET subscription_id = COALESCE(billing_periods.subscription_id, EXCLUDED.subscription_id)
WHERE billing_periods.ends_at = EXCLUDED.ends_at
RETURNING *;

-- name: ReconcileInactiveBillingPeriodForReplacement :execrows
UPDATE billing_periods AS period
SET subscription_id = CASE
        WHEN period.starts_at = sqlc.arg(replacement_starts_at)
            THEN sqlc.arg(replacement_subscription_id)
        ELSE period.subscription_id
    END,
    ends_at = CASE
        WHEN period.starts_at = sqlc.arg(replacement_starts_at)
            THEN sqlc.arg(replacement_ends_at)
        ELSE sqlc.arg(replacement_starts_at)
    END
FROM subscriptions AS subscription
WHERE period.organization_id = sqlc.arg(organization_id)
  AND period.subscription_id = subscription.id
  AND subscription.organization_id = period.organization_id
  AND period.subscription_id <> sqlc.arg(replacement_subscription_id)
  AND subscription.status IN ('past_due', 'canceled', 'revoked')
  AND period.starts_at <= sqlc.arg(replacement_starts_at)
  AND period.ends_at > sqlc.arg(replacement_starts_at);

-- name: GetPolarPaidCycle :one
SELECT * FROM polar_paid_cycles WHERE polar_order_id = sqlc.arg(polar_order_id);

-- name: GetPolarPaidCycleBinding :one
SELECT cycle.organization_id,
       subscription.polar_subscription_id,
       cycle.period_starts_at,
       cycle.period_ends_at
FROM polar_paid_cycles AS cycle
JOIN subscriptions AS subscription
  ON subscription.organization_id = cycle.organization_id
 AND subscription.id = cycle.subscription_id
WHERE cycle.polar_order_id = sqlc.arg(polar_order_id);

-- name: InsertPolarPaidCycle :one
INSERT INTO polar_paid_cycles (
    polar_order_id, organization_id, subscription_id, billing_period_id,
    period_starts_at, period_ends_at, grant_micros, paid_at
) VALUES (
    sqlc.arg(polar_order_id), sqlc.arg(organization_id),
    sqlc.arg(subscription_id), sqlc.arg(billing_period_id),
    sqlc.arg(period_starts_at), sqlc.arg(period_ends_at), 10000000,
    sqlc.arg(paid_at)
)
ON CONFLICT (polar_order_id) DO UPDATE
SET polar_order_id = EXCLUDED.polar_order_id
WHERE polar_paid_cycles.organization_id = EXCLUDED.organization_id
  AND polar_paid_cycles.subscription_id = EXCLUDED.subscription_id
  AND polar_paid_cycles.billing_period_id = EXCLUDED.billing_period_id
  AND polar_paid_cycles.period_starts_at = EXCLUDED.period_starts_at
  AND polar_paid_cycles.period_ends_at = EXCLUDED.period_ends_at
RETURNING *;

-- name: InsertBillingLedgerEntry :one
INSERT INTO ledger_entries (
    id, organization_id, billing_period_id, entry_type, amount_micros,
    balance_after_micros, source_reference, actor_reference
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.narg(billing_period_id),
    sqlc.arg(entry_type), sqlc.arg(amount_micros),
    sqlc.arg(balance_after_micros), sqlc.arg(source_reference), ''
)
ON CONFLICT (organization_id, entry_type, source_reference) DO NOTHING
RETURNING *;

-- name: UpsertPolarRefund :one
INSERT INTO polar_refunds (
    polar_refund_id, polar_order_id, status, requested_micros,
    reversed_micros, chargeback, provider_event_at
) VALUES (
    sqlc.arg(polar_refund_id), sqlc.arg(polar_order_id), sqlc.arg(status),
    sqlc.arg(requested_micros), sqlc.arg(reversed_micros),
    sqlc.arg(chargeback), sqlc.arg(provider_event_at)
)
ON CONFLICT (polar_refund_id) DO UPDATE
SET status = EXCLUDED.status,
    requested_micros = EXCLUDED.requested_micros,
    reversed_micros = EXCLUDED.reversed_micros,
    chargeback = EXCLUDED.chargeback,
    provider_event_at = EXCLUDED.provider_event_at
WHERE polar_refunds.polar_order_id = EXCLUDED.polar_order_id
  AND polar_refunds.provider_event_at <= EXCLUDED.provider_event_at
  AND polar_refunds.reversed_micros <= EXCLUDED.reversed_micros
RETURNING *;

-- name: GetPolarRefund :one
SELECT * FROM polar_refunds WHERE polar_refund_id = sqlc.arg(polar_refund_id);

-- name: AddPolarCycleReversal :one
UPDATE polar_paid_cycles
SET reversed_micros = reversed_micros + sqlc.arg(amount_micros)
WHERE polar_order_id = sqlc.arg(polar_order_id)
  AND reversed_micros + sqlc.arg(amount_micros) <= grant_micros
RETURNING *;

-- name: InsertBillingShortfall :one
INSERT INTO billing_shortfalls (
    id, organization_id, billing_period_id, polar_refund_id,
    source_reference, amount_micros
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(billing_period_id),
    sqlc.arg(polar_refund_id), sqlc.arg(source_reference), sqlc.arg(amount_micros)
)
ON CONFLICT (source_reference) DO NOTHING
RETURNING *;
