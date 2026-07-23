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

-- name: DisableServiceMeterAllowlists :exec
UPDATE service_meter_allowlists
SET enabled = false
WHERE enabled;

-- name: DeleteDisabledServiceMeterAllowlists :exec
DELETE FROM service_meter_allowlists AS allowlist
WHERE NOT allowlist.enabled
  AND NOT EXISTS (
      SELECT 1
      FROM usage_reservations AS reservation
      WHERE reservation.active_service_identity_id = allowlist.service_identity_id
        AND reservation.active_meter_id = allowlist.meter_id
  );

-- name: DeleteUnusedPolarMeterMappings :exec
DELETE FROM polar_meter_mappings AS mapping
WHERE NOT EXISTS (
    SELECT 1
    FROM usage_reservations AS reservation
    WHERE reservation.active_meter_id = mapping.meter_id
);

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
  AND effective_until IS NULL;

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

-- name: UpsertServiceMeterAllowlist :exec
INSERT INTO service_meter_allowlists (service_identity_id, meter_id, enabled)
VALUES ($1, $2, true)
ON CONFLICT (service_identity_id, meter_id) DO UPDATE
SET enabled = true;

-- name: EnsurePolarMeterMapping :execrows
INSERT INTO polar_meter_mappings (meter_id, polar_meter_id)
VALUES ($1, $2)
ON CONFLICT (meter_id) DO UPDATE
SET meter_id = EXCLUDED.meter_id
WHERE polar_meter_mappings.polar_meter_id = EXCLUDED.polar_meter_id;
