CREATE TABLE polar_subscription_checkouts (
    organization_id uuid PRIMARY KEY
        REFERENCES organizations(id) ON DELETE CASCADE,
    polar_checkout_id text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_checkout_id) BETWEEN 1 AND 255),
    CHECK (expires_at > created_at)
);

CREATE TABLE polar_subscription_checkout_attempts (
    organization_id uuid PRIMARY KEY
        REFERENCES organizations(id) ON DELETE CASCADE,
    provider_idempotency_key text NOT NULL UNIQUE,
    request_digest bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(provider_idempotency_key) BETWEEN 1 AND 255),
    CHECK (length(request_digest) = 32),
    CHECK (expires_at > created_at)
);
