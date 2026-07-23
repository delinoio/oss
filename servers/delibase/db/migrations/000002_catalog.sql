CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE catalog_apps (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    summary text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    icon_url text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (is_uuid_v7(id)),
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$'),
    CHECK (length(name) BETWEEN 1 AND 120)
);

CREATE TABLE catalog_meters (
    id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES catalog_apps(id) ON DELETE CASCADE,
    meter_key text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    unit_name text NOT NULL,
    unit_precision integer NOT NULL DEFAULT 0 CHECK (unit_precision = 0),
    reservation_ttl_seconds bigint NOT NULL CHECK (reservation_ttl_seconds BETWEEN 1 AND 86400),
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (app_id, meter_key),
    CHECK (is_uuid_v7(id)),
    CHECK (meter_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    CHECK (length(name) BETWEEN 1 AND 120),
    CHECK (length(unit_name) BETWEEN 1 AND 64)
);

CREATE TABLE catalog_price_versions (
    id uuid PRIMARY KEY,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE RESTRICT,
    usd_micros_per_unit bigint NOT NULL CHECK (usd_micros_per_unit >= 0),
    effective_from timestamptz NOT NULL,
    effective_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (meter_id, id),
    CHECK (is_uuid_v7(id)),
    CHECK (effective_until IS NULL OR effective_until > effective_from),
    EXCLUDE USING gist (
        meter_id WITH =,
        tstzrange(effective_from, effective_until, '[)') WITH &&
    )
);

CREATE UNIQUE INDEX catalog_price_versions_current_idx
    ON catalog_price_versions(meter_id) WHERE effective_until IS NULL;

CREATE TABLE service_identities (
    id uuid PRIMARY KEY,
    logto_client_id text NOT NULL UNIQUE,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (is_uuid_v7(id)),
    CHECK (length(logto_client_id) BETWEEN 1 AND 255),
    CHECK (length(name) BETWEEN 1 AND 120)
);

CREATE TABLE service_meter_allowlists (
    service_identity_id uuid NOT NULL REFERENCES service_identities(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES catalog_meters(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (service_identity_id, meter_id)
);

CREATE TABLE polar_meter_mappings (
    meter_id uuid PRIMARY KEY REFERENCES catalog_meters(id) ON DELETE CASCADE,
    polar_meter_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(polar_meter_id) BETWEEN 1 AND 255)
);
