ALTER TABLE realqa_submissions
    ADD COLUMN client_idempotency_key uuid,
    ADD COLUMN transfer_meter_id uuid,
    ADD COLUMN transfer_service_identity_id uuid,
    ADD COLUMN transfer_reservation_id uuid,
    ADD COLUMN transfer_reserve_idempotency_key uuid,
    ADD COLUMN transfer_commit_idempotency_key uuid,
    ADD COLUMN transfer_release_idempotency_key uuid,
    ADD COLUMN transfer_reserved_units bigint NOT NULL DEFAULT 0
        CHECK (transfer_reserved_units >= 0),
    ADD COLUMN transfer_committed_units bigint NOT NULL DEFAULT 0
        CHECK (
            transfer_committed_units >= 0
            AND transfer_committed_units <= transfer_reserved_units
        ),
    ADD COLUMN transfer_state text NOT NULL DEFAULT 'unconfigured'
        CHECK (transfer_state IN (
            'unconfigured', 'pending', 'reserved', 'committed',
            'released', 'expired'
        )),
    ADD COLUMN transfer_reservation_created_at timestamptz,
    ADD COLUMN transfer_reservation_expires_at timestamptz;

UPDATE realqa_submissions
SET client_idempotency_key = id;

CREATE FUNCTION realqa_default_submission_client_key()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.client_idempotency_key IS NULL THEN
        NEW.client_idempotency_key = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_submissions_default_client_key
BEFORE INSERT ON realqa_submissions
FOR EACH ROW EXECUTE FUNCTION realqa_default_submission_client_key();

ALTER TABLE realqa_submissions
    ALTER COLUMN client_idempotency_key SET NOT NULL,
    ADD CHECK (realqa_is_uuid_v7(client_idempotency_key)),
    ADD CHECK (
        (
            transfer_state = 'unconfigured'
            AND transfer_meter_id IS NULL
            AND transfer_service_identity_id IS NULL
            AND transfer_reservation_id IS NULL
            AND transfer_reserve_idempotency_key IS NULL
            AND transfer_commit_idempotency_key IS NULL
            AND transfer_release_idempotency_key IS NULL
            AND transfer_reserved_units = 0
            AND transfer_committed_units = 0
            AND transfer_reservation_created_at IS NULL
            AND transfer_reservation_expires_at IS NULL
        )
        OR
        (
            transfer_state = 'pending'
            AND transfer_meter_id IS NOT NULL
            AND transfer_service_identity_id IS NOT NULL
            AND transfer_reservation_id IS NULL
            AND transfer_reserve_idempotency_key IS NOT NULL
            AND transfer_commit_idempotency_key IS NOT NULL
            AND transfer_release_idempotency_key IS NOT NULL
            AND transfer_reserved_units > 0
            AND transfer_committed_units = 0
            AND transfer_reservation_created_at IS NULL
            AND transfer_reservation_expires_at IS NULL
        )
        OR
        (
            transfer_state IN ('reserved', 'committed', 'released', 'expired')
            AND transfer_meter_id IS NOT NULL
            AND transfer_service_identity_id IS NOT NULL
            AND transfer_reservation_id IS NOT NULL
            AND transfer_reserve_idempotency_key IS NOT NULL
            AND transfer_commit_idempotency_key IS NOT NULL
            AND transfer_release_idempotency_key IS NOT NULL
            AND transfer_reserved_units > 0
            AND transfer_reservation_created_at IS NOT NULL
            AND transfer_reservation_expires_at IS NOT NULL
            AND transfer_reservation_expires_at
                > transfer_reservation_created_at
        )
    );

CREATE UNIQUE INDEX realqa_submissions_client_idempotency_key_unique
ON realqa_submissions (created_by_account_id, client_idempotency_key);

CREATE UNIQUE INDEX realqa_submissions_transfer_reservation_unique
ON realqa_submissions (transfer_reservation_id)
WHERE transfer_reservation_id IS NOT NULL;

CREATE TABLE realqa_storage_authorization_attempts (
    submission_id uuid PRIMARY KEY
        REFERENCES realqa_submissions(id) ON DELETE CASCADE,
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    service_identity_id uuid NOT NULL,
    meter_id uuid NOT NULL,
    maximum_units bigint NOT NULL CHECK (maximum_units > 0),
    state text NOT NULL CHECK (state IN (
        'pending', 'active', 'closure_pending', 'closed'
    )),
    authorization_id uuid,
    authorization_revision bigint,
    mapping_revision bigint NOT NULL DEFAULT 0 CHECK (mapping_revision >= 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (realqa_is_uuid_v7(submission_id)),
    CHECK (realqa_is_uuid_v7(idempotency_key)),
    CHECK (realqa_is_uuid_v7(service_identity_id)),
    CHECK (realqa_is_uuid_v7(meter_id)),
    CHECK (authorization_id IS NULL OR realqa_is_uuid_v7(authorization_id)),
    CHECK (
        (
            state = 'pending'
            AND authorization_id IS NULL
            AND authorization_revision IS NULL
            AND mapping_revision = 0
        )
        OR
        (
            state IN ('active', 'closure_pending', 'closed')
            AND authorization_id IS NOT NULL
            AND authorization_revision > 0
            AND mapping_revision > 0
        )
    )
);

CREATE UNIQUE INDEX realqa_storage_authorization_attempts_authorization_unique
ON realqa_storage_authorization_attempts (authorization_id)
WHERE authorization_id IS NOT NULL;

CREATE TABLE realqa_issue_submission_attempts (
    submission_id uuid PRIMARY KEY
        REFERENCES realqa_submissions(id) ON DELETE CASCADE,
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    final_body_digest bytea CHECK (
        final_body_digest IS NULL OR octet_length(final_body_digest) = 32
    ),
    state text NOT NULL CHECK (state IN (
        'pending', 'transfer_finalized', 'provider_pending',
        'provider_reconciled', 'promoting', 'completed', 'failed'
    )),
    provider_issue_id text,
    provider_issue_url text,
    failure_reason text,
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    completed_at timestamptz,
    CHECK (realqa_is_uuid_v7(submission_id)),
    CHECK (realqa_is_uuid_v7(idempotency_key)),
    CHECK (
        (provider_issue_id IS NULL AND provider_issue_url IS NULL)
        OR
        (provider_issue_id IS NOT NULL AND provider_issue_url IS NOT NULL)
    ),
    CHECK (
        (state IN ('provider_reconciled', 'promoting', 'completed')
            AND provider_issue_id IS NOT NULL)
        OR state NOT IN ('provider_reconciled', 'promoting', 'completed')
    ),
    CHECK ((state = 'completed') = (completed_at IS NOT NULL))
);

CREATE INDEX realqa_issue_submission_attempts_rate
ON realqa_issue_submission_attempts (accepted_at, submission_id);

ALTER TABLE realqa_idempotency_records
    DROP CONSTRAINT realqa_idempotency_records_operation_check,
    ADD CONSTRAINT realqa_idempotency_records_operation_check
        CHECK (operation IN (
            'create_preset',
            'create_submission',
            'create_image_upload',
            'finalize_image_upload',
            'submit_issue',
            'delete_image',
            'delete_submission_assets',
            'delete_feature_data',
            'disconnect_github_connection'
        ));

ALTER TABLE realqa_audits
    DROP CONSTRAINT realqa_audits_event_type_check,
    ADD CONSTRAINT realqa_audits_event_type_check CHECK (event_type IN (
        'preset_created',
        'preset_updated',
        'preset_deleted',
        'github_connection_started',
        'github_user_authorization_started',
        'github_connection_disconnected',
        'feature_deletion_accepted',
        'repository_access_denied',
        'submission_created',
        'transfer_reserved',
        'image_upload_authorized',
        'image_upload_verified',
        'transfer_committed',
        'transfer_released',
        'storage_authorization_created',
        'issue_submission_started',
        'issue_reconciled',
        'submission_completed',
        'image_deleted',
        'submission_assets_deleted'
    ));
