CREATE TABLE polar_customers (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    polar_customer_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_customer_id) BETWEEN 1 AND 255)
);

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE CASCADE,
    polar_subscription_id text NOT NULL UNIQUE,
    status text NOT NULL
        CHECK (status IN ('pending', 'active', 'past_due', 'canceled', 'revoked')),
    current_period_starts_at timestamptz,
    current_period_ends_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (
        current_period_starts_at IS NULL
        OR current_period_ends_at IS NULL
        OR current_period_ends_at > current_period_starts_at
    )
);

CREATE TABLE billing_periods (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subscription_id uuid REFERENCES subscriptions(id) ON DELETE SET NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    overage_limit_micros bigint NOT NULL DEFAULT 0 CHECK (overage_limit_micros >= 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id, starts_at),
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (ends_at > starts_at)
);

CREATE TABLE ledger_entries (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    billing_period_id uuid REFERENCES billing_periods(id) ON DELETE SET NULL,
    entry_type text NOT NULL
        CHECK (entry_type IN (
            'credit_grant', 'credit_reversal', 'credit_commit',
            'credit_forfeit', 'overage_commit', 'adjustment'
        )),
    amount_micros bigint NOT NULL CHECK (amount_micros <> 0),
    source_reference text NOT NULL,
    actor_reference text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id, entry_type, source_reference),
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE INDEX ledger_entries_organization_idx
    ON ledger_entries(organization_id, created_at, id);

CREATE TABLE usage_reservations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    team_id uuid NOT NULL,
    team_name_snapshot text NOT NULL,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE RESTRICT,
    price_version_id uuid NOT NULL REFERENCES catalog_price_versions(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    service_identity_id uuid NOT NULL REFERENCES service_identities(id) ON DELETE RESTRICT,
    maximum_units bigint NOT NULL CHECK (maximum_units > 0),
    usd_micros_per_unit bigint NOT NULL CHECK (usd_micros_per_unit >= 0),
    maximum_cost_micros bigint NOT NULL CHECK (maximum_cost_micros >= 0),
    held_credit_micros bigint NOT NULL CHECK (held_credit_micros >= 0),
    held_overage_micros bigint NOT NULL CHECK (held_overage_micros >= 0),
    client_reference text NOT NULL,
    status text NOT NULL DEFAULT 'held'
        CHECK (status IN ('held', 'committed', 'released', 'expired')),
    expires_at timestamptz NOT NULL,
    finalized_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (id, organization_id, team_id),
    FOREIGN KEY (organization_id, team_id)
        REFERENCES teams(organization_id, id) ON DELETE RESTRICT,
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (held_credit_micros + held_overage_micros = maximum_cost_micros),
    CHECK (
        (status = 'held' AND finalized_at IS NULL)
        OR
        (status <> 'held' AND finalized_at IS NOT NULL)
    )
);

CREATE INDEX usage_reservations_active_org_idx
    ON usage_reservations(organization_id, expires_at) WHERE status = 'held';
CREATE INDEX usage_reservations_active_team_idx
    ON usage_reservations(team_id, expires_at) WHERE status = 'held';

CREATE TABLE usage_records (
    id uuid PRIMARY KEY,
    reservation_id uuid NOT NULL UNIQUE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    team_id uuid NOT NULL,
    team_name_snapshot text NOT NULL,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    service_identity_id uuid NOT NULL REFERENCES service_identities(id) ON DELETE RESTRICT,
    committed_units bigint NOT NULL CHECK (committed_units >= 0),
    total_cost_micros bigint NOT NULL CHECK (total_cost_micros >= 0),
    credit_applied_micros bigint NOT NULL CHECK (credit_applied_micros >= 0),
    overage_applied_micros bigint NOT NULL CHECK (overage_applied_micros >= 0),
    committed_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    FOREIGN KEY (organization_id, team_id)
        REFERENCES teams(organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (reservation_id, organization_id, team_id)
        REFERENCES usage_reservations(id, organization_id, team_id) ON DELETE RESTRICT,
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (credit_applied_micros + overage_applied_micros = total_cost_micros)
);

CREATE INDEX usage_records_organization_idx
    ON usage_records(organization_id, committed_at, id);
