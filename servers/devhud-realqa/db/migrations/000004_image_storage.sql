ALTER TABLE realqa_submissions
    ADD COLUMN payer_organization_id uuid,
    ADD COLUMN payer_team_id uuid,
    ADD COLUMN preset_revision bigint NOT NULL DEFAULT 1 CHECK (preset_revision > 0),
    ADD COLUMN declared_encoded_bytes bigint NOT NULL DEFAULT 0
        CHECK (declared_encoded_bytes BETWEEN 0 AND 262144000),
    ADD COLUMN verified_encoded_bytes bigint NOT NULL DEFAULT 0
        CHECK (verified_encoded_bytes BETWEEN 0 AND 262144000),
    ADD COLUMN upload_deadline timestamptz,
    ADD COLUMN upload_expires_at timestamptz;

UPDATE realqa_submissions
SET upload_deadline = created_at + interval '23 hours',
    upload_expires_at = created_at + interval '24 hours';

ALTER TABLE realqa_submissions
    ALTER COLUMN upload_deadline SET NOT NULL,
    ALTER COLUMN upload_expires_at SET NOT NULL,
    ADD CHECK (upload_deadline > created_at),
    ADD CHECK (upload_expires_at > upload_deadline),
    ADD CHECK (
        (payer_organization_id IS NULL AND payer_team_id IS NULL)
        OR
        (payer_organization_id IS NOT NULL AND payer_team_id IS NOT NULL)
    );

ALTER TABLE realqa_idempotency_records
    DROP CONSTRAINT realqa_idempotency_records_operation_check,
    ADD CONSTRAINT realqa_idempotency_records_operation_check
        CHECK (operation IN (
            'create_preset',
            'create_submission',
            'create_image_upload',
            'finalize_image_upload',
            'delete_image',
            'delete_submission_assets',
            'delete_feature_data',
            'disconnect_github_connection'
        ));

ALTER TABLE realqa_audits
    DROP CONSTRAINT realqa_audits_event_type_check,
    ADD CONSTRAINT realqa_audits_event_type_check
        CHECK (event_type IN (
            'preset_created',
            'preset_updated',
            'preset_deleted',
            'github_connection_started',
            'github_connection_disconnected',
            'feature_deletion_accepted',
            'repository_access_denied',
            'submission_created',
            'image_upload_authorized',
            'image_upload_verified',
            'image_deleted',
            'submission_assets_deleted'
        ));

ALTER TABLE realqa_assets
    ADD COLUMN client_image_id uuid,
    ADD COLUMN media_type text,
    ADD COLUMN declared_encoded_bytes bigint,
    ADD COLUMN pixel_width integer,
    ADD COLUMN pixel_height integer,
    ADD COLUMN source_sha256 bytea,
    ADD COLUMN sanitized_sha256 bytea,
    ADD COLUMN upload_state text,
    ADD COLUMN upload_token_digest bytea,
    ADD COLUMN upload_expires_at timestamptz,
    ADD COLUMN uploaded_at timestamptz,
    ADD COLUMN verified_at timestamptz;

-- Migration 000003 allowed zero-length and arbitrarily large legacy assets.
-- Keep only the bounded retained prefix billable and terminalize rows that the
-- image-storage contract could never have accepted.
WITH legacy_assets AS (
    SELECT id,
           encoded_bytes BETWEEN 1 AND 26214400 AS valid_image_size,
           sum(encoded_bytes) FILTER (
               WHERE encoded_bytes BETWEEN 1 AND 26214400
                 AND state IN ('verified_unlinked', 'public_retained')
           ) OVER (
               PARTITION BY submission_id
               ORDER BY id
           ) AS retained_encoded_bytes
    FROM realqa_assets
)
UPDATE realqa_assets AS asset
SET client_image_id = id,
    media_type = 'image/png',
    declared_encoded_bytes = CASE
        WHEN legacy.valid_image_size THEN asset.encoded_bytes
        ELSE 1
    END,
    pixel_width = 1,
    pixel_height = 1,
    source_sha256 = decode(repeat('00', 32), 'hex'),
    upload_state = CASE
        WHEN NOT legacy.valid_image_size
          OR (
              asset.state IN ('verified_unlinked', 'public_retained')
              AND legacy.retained_encoded_bytes > 262144000
          ) THEN 'deleted'
        WHEN asset.state = 'private_staging' THEN 'declared'
        WHEN asset.state = 'verified_unlinked' THEN 'verified'
        WHEN asset.state = 'public_retained' THEN 'verified'
        WHEN asset.state = 'removed_placeholder' THEN 'deleted'
        WHEN asset.state = 'expired' THEN 'expired'
        ELSE 'deleted'
    END,
    state = CASE
        WHEN NOT legacy.valid_image_size
          OR (
              asset.state IN ('verified_unlinked', 'public_retained')
              AND legacy.retained_encoded_bytes > 262144000
          ) THEN CASE
              WHEN asset.public_id IS NULL THEN 'deleted'
              ELSE 'removed_placeholder'
          END
        ELSE asset.state
    END,
    removed_at = CASE
        WHEN NOT legacy.valid_image_size
          OR (
              asset.state IN ('verified_unlinked', 'public_retained')
              AND legacy.retained_encoded_bytes > 262144000
          ) THEN COALESCE(asset.removed_at, transaction_timestamp())
        ELSE asset.removed_at
    END
FROM legacy_assets AS legacy
WHERE legacy.id = asset.id;

ALTER TABLE realqa_assets
    ALTER COLUMN client_image_id SET NOT NULL,
    ALTER COLUMN media_type SET NOT NULL,
    ALTER COLUMN declared_encoded_bytes SET NOT NULL,
    ALTER COLUMN pixel_width SET NOT NULL,
    ALTER COLUMN pixel_height SET NOT NULL,
    ALTER COLUMN source_sha256 SET NOT NULL,
    ALTER COLUMN upload_state SET NOT NULL,
    ADD CHECK (realqa_is_uuid_v7(client_image_id)),
    ADD CHECK (media_type IN ('image/png', 'image/webp')),
    ADD CHECK (declared_encoded_bytes BETWEEN 1 AND 26214400),
    ADD CHECK (pixel_width > 0 AND pixel_height > 0),
    ADD CHECK (pixel_width::bigint * pixel_height::bigint <= 100000000),
    ADD CHECK (octet_length(source_sha256) = 32),
    ADD CHECK (sanitized_sha256 IS NULL OR octet_length(sanitized_sha256) = 32),
    ADD CHECK (upload_state IN (
        'declared', 'put_authorized', 'uploaded', 'verifying', 'verified',
        'rejected', 'expired', 'deleted'
    )),
    ADD CHECK (upload_token_digest IS NULL OR octet_length(upload_token_digest) = 32),
    ADD CHECK (
        (upload_token_digest IS NULL AND upload_expires_at IS NULL)
        OR
        (upload_token_digest IS NOT NULL AND upload_expires_at IS NOT NULL)
    ),
    ADD UNIQUE (submission_id, client_image_id);

WITH totals AS (
    SELECT submission_id,
           COALESCE(sum(declared_encoded_bytes), 0)::bigint
               AS declared_encoded_bytes,
           COALESCE(sum(encoded_bytes), 0)::bigint
               AS verified_encoded_bytes
    FROM realqa_assets
    WHERE upload_state = 'verified'
      AND state IN ('verified_unlinked', 'public_retained')
    GROUP BY submission_id
)
UPDATE realqa_submissions AS submission
SET declared_encoded_bytes = totals.declared_encoded_bytes,
    verified_encoded_bytes = totals.verified_encoded_bytes
FROM totals
WHERE totals.submission_id = submission.id;

CREATE UNIQUE INDEX realqa_assets_upload_token_digest_unique
ON realqa_assets (upload_token_digest)
WHERE upload_token_digest IS NOT NULL;

CREATE INDEX realqa_assets_cleanup
ON realqa_assets (upload_expires_at, created_at)
WHERE state IN ('private_staging', 'verified_unlinked');

CREATE TABLE realqa_object_deletion_jobs (
    asset_id uuid NOT NULL,
    object_kind text NOT NULL CHECK (object_kind IN (
        'staging', 'verified', 'public'
    )),
    public_id text,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    last_attempted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (realqa_is_uuid_v7(asset_id)),
    CHECK (
        (object_kind = 'public' AND public_id IS NOT NULL)
        OR
        (object_kind <> 'public' AND public_id IS NULL)
    ),
    CHECK (public_id IS NULL OR length(public_id) BETWEEN 22 AND 128)
);

CREATE UNIQUE INDEX realqa_object_deletion_jobs_private_unique
ON realqa_object_deletion_jobs (asset_id, object_kind)
WHERE object_kind <> 'public';

CREATE UNIQUE INDEX realqa_object_deletion_jobs_public_unique
ON realqa_object_deletion_jobs (public_id)
WHERE object_kind = 'public';

CREATE INDEX realqa_object_deletion_jobs_pending
ON realqa_object_deletion_jobs (next_attempt_at, created_at);

CREATE TABLE realqa_public_asset_tombstones (
    public_id text PRIMARY KEY CHECK (length(public_id) BETWEEN 22 AND 128),
    removed_at timestamptz NOT NULL DEFAULT transaction_timestamp()
);

CREATE FUNCTION realqa_preserve_public_asset_tombstone()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.public_id IS DISTINCT FROM OLD.public_id
       OR NEW.removed_at IS DISTINCT FROM OLD.removed_at THEN
        RAISE EXCEPTION 'RealQA public asset tombstone is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_public_asset_tombstones_preserve
BEFORE UPDATE OR DELETE ON realqa_public_asset_tombstones
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_public_asset_tombstone();

INSERT INTO realqa_public_asset_tombstones (public_id, removed_at)
SELECT public_id, COALESCE(removed_at, transaction_timestamp())
FROM realqa_assets
WHERE upload_state = 'deleted'
  AND public_id IS NOT NULL
ON CONFLICT (public_id) DO NOTHING;

INSERT INTO realqa_object_deletion_jobs (asset_id, object_kind)
SELECT id, object_kind
FROM realqa_assets
CROSS JOIN (VALUES ('staging'), ('verified')) AS kinds(object_kind)
WHERE upload_state = 'deleted'
ON CONFLICT DO NOTHING;

INSERT INTO realqa_object_deletion_jobs (asset_id, object_kind, public_id)
SELECT id, 'public', public_id
FROM realqa_assets
WHERE upload_state = 'deleted'
  AND public_id IS NOT NULL
ON CONFLICT DO NOTHING;
