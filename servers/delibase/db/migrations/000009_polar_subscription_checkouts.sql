CREATE TABLE polar_subscription_checkouts (
    organization_id uuid PRIMARY KEY
        REFERENCES organizations(id) ON DELETE CASCADE,
    polar_checkout_id text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_checkout_id) BETWEEN 1 AND 255),
    CHECK (expires_at > created_at)
);
