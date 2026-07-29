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

UPDATE realqa_assets
SET client_image_id = id,
    media_type = 'image/png',
    declared_encoded_bytes = encoded_bytes,
    pixel_width = 1,
    pixel_height = 1,
    source_sha256 = decode(repeat('00', 32), 'hex'),
    upload_state = CASE
        WHEN state = 'private_staging' THEN 'declared'
        WHEN state = 'verified_unlinked' THEN 'verified'
        WHEN state = 'public_retained' THEN 'verified'
        WHEN state = 'removed_placeholder' THEN 'deleted'
        WHEN state = 'expired' THEN 'expired'
        ELSE 'deleted'
    END;

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

CREATE UNIQUE INDEX realqa_assets_upload_token_digest_unique
ON realqa_assets (upload_token_digest)
WHERE upload_token_digest IS NOT NULL;

CREATE INDEX realqa_assets_cleanup
ON realqa_assets (upload_expires_at, created_at)
WHERE state IN ('private_staging', 'verified_unlinked');

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
