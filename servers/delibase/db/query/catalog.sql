-- name: DisableCatalogApps :exec
UPDATE catalog_apps
SET enabled = false,
    updated_at = transaction_timestamp()
WHERE enabled;

-- name: DisableCatalogMeters :exec
UPDATE catalog_meters
SET enabled = false,
    updated_at = transaction_timestamp()
WHERE enabled;

-- name: DisableServiceIdentities :exec
UPDATE service_identities
SET enabled = false,
    updated_at = transaction_timestamp()
WHERE enabled;

-- name: ClearServiceMeterAllowlists :exec
DELETE FROM service_meter_allowlists;

-- name: ClearPolarMeterMappings :exec
DELETE FROM polar_meter_mappings;

-- name: UpsertCatalogApp :exec
INSERT INTO catalog_apps (
    id,
    slug,
    name,
    summary,
    description,
    icon_url,
    enabled
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE
SET slug = EXCLUDED.slug,
    name = EXCLUDED.name,
    summary = EXCLUDED.summary,
    description = EXCLUDED.description,
    icon_url = EXCLUDED.icon_url,
    enabled = EXCLUDED.enabled,
    updated_at = transaction_timestamp();

-- name: UpsertCatalogMeter :exec
INSERT INTO catalog_meters (
    id,
    app_id,
    meter_key,
    name,
    description,
    unit_name,
    unit_precision,
    reservation_ttl_seconds,
    enabled
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE
SET app_id = EXCLUDED.app_id,
    meter_key = EXCLUDED.meter_key,
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    unit_name = EXCLUDED.unit_name,
    unit_precision = EXCLUDED.unit_precision,
    reservation_ttl_seconds = EXCLUDED.reservation_ttl_seconds,
    enabled = EXCLUDED.enabled,
    updated_at = transaction_timestamp();

-- name: CloseCatalogPriceVersion :exec
UPDATE catalog_price_versions
SET effective_until = $2
WHERE id = $1
  AND effective_until IS NULL
  AND $2 IS NOT NULL;

-- name: EnsureCatalogPriceVersion :execrows
INSERT INTO catalog_price_versions (
    id,
    meter_id,
    usd_micros_per_unit,
    effective_from,
    effective_until
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
SET id = EXCLUDED.id
WHERE catalog_price_versions.meter_id = EXCLUDED.meter_id
  AND catalog_price_versions.usd_micros_per_unit = EXCLUDED.usd_micros_per_unit
  AND catalog_price_versions.effective_from = EXCLUDED.effective_from
  AND catalog_price_versions.effective_until IS NOT DISTINCT FROM EXCLUDED.effective_until;

-- name: UpsertServiceIdentity :exec
INSERT INTO service_identities (
    id,
    logto_client_id,
    name,
    enabled
) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET logto_client_id = EXCLUDED.logto_client_id,
    name = EXCLUDED.name,
    enabled = EXCLUDED.enabled,
    updated_at = transaction_timestamp();

-- name: CreateServiceMeterAllowlist :exec
INSERT INTO service_meter_allowlists (service_identity_id, meter_id)
VALUES ($1, $2);

-- name: CreatePolarMeterMapping :exec
INSERT INTO polar_meter_mappings (meter_id, polar_meter_id)
VALUES ($1, $2);
