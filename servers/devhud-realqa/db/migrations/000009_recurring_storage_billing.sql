-- RealQA storage is charged after the submitting user's live session is gone.
-- Keep only authorization/resource UUIDs and aggregate retention facts here;
-- no user bearer, provider credential, issue body, or screenshot bytes belong
-- in the recurring-billing ledger.
CREATE TABLE realqa_storage_authorization_bindings (
    authorization_id uuid PRIMARY KEY,
    submission_id uuid NOT NULL
        REFERENCES realqa_submissions(id) ON DELETE RESTRICT,
    mapping_revision bigint NOT NULL CHECK (mapping_revision > 0),
    authorizer_account_id uuid NOT NULL,
    owner_kind text NOT NULL CHECK (owner_kind IN (
        'personal', 'organization'
    )),
    owner_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    team_id uuid NOT NULL,
    service_identity_id uuid NOT NULL,
    meter_id uuid NOT NULL,
    maximum_units bigint NOT NULL CHECK (maximum_units > 0),
    status text NOT NULL CHECK (status IN (
        'active', 'revoked', 'access_lost',
        'resource_deleted', 'owner_deleted'
    )),
    authorization_revision bigint NOT NULL
        CHECK (authorization_revision > 0),
    closure_state text NOT NULL DEFAULT 'open' CHECK (closure_state IN (
        'open', 'resource_deletion_pending', 'revoke_pending', 'closed'
    )),
    closure_owner_deleted_allowed boolean NOT NULL DEFAULT false,
    accrual_cutoff_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    closed_at timestamptz,
    CHECK (realqa_is_uuid_v7(authorization_id)),
    CHECK (realqa_is_uuid_v7(submission_id)),
    CHECK (realqa_is_uuid_v7(authorizer_account_id)),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (realqa_is_uuid_v7(organization_id)),
    CHECK (realqa_is_uuid_v7(team_id)),
    CHECK (realqa_is_uuid_v7(service_identity_id)),
    CHECK (realqa_is_uuid_v7(meter_id)),
    CHECK (
        (closure_state = 'closed' AND closed_at IS NOT NULL)
        OR (closure_state <> 'closed' AND closed_at IS NULL)
    ),
    UNIQUE (submission_id, mapping_revision)
);

CREATE INDEX realqa_storage_authorization_bindings_submission
ON realqa_storage_authorization_bindings (submission_id, mapping_revision DESC);

-- Deletion before this ledger existed could leave an active grant after its
-- last public asset was tombstoned. Restrict repair to retained/terminal
-- submission states so an in-flight promotion with no public asset stays open.
WITH binding_backfill AS (
    SELECT attempt.*,
           submission.created_by_account_id,
           submission.owner_kind,
           submission.owner_id,
           submission.payer_organization_id,
           submission.payer_team_id,
           submission.updated_at AS submission_updated_at,
           attempt.state = 'active'
               AND submission.state IN (
                   'submitted', 'failed', 'storage_billing_grace',
                   'assets_deleted', 'deleted'
               )
               AND NOT EXISTS (
                   SELECT 1
                   FROM realqa_assets AS retained
                   WHERE retained.submission_id = submission.id
                     AND retained.state = 'public_retained'
                     AND retained.upload_state = 'verified'
               ) AS requires_resource_deletion
    FROM realqa_storage_authorization_attempts AS attempt
    JOIN realqa_submissions AS submission
      ON submission.id = attempt.submission_id
    WHERE attempt.authorization_id IS NOT NULL
      AND attempt.state IN ('active', 'closure_pending', 'closed')
      AND submission.payer_organization_id IS NOT NULL
      AND submission.payer_team_id IS NOT NULL
)
INSERT INTO realqa_storage_authorization_bindings (
    authorization_id, submission_id, mapping_revision,
    authorizer_account_id, owner_kind, owner_id,
    organization_id, team_id, service_identity_id, meter_id,
    maximum_units, status, authorization_revision, closure_state,
    accrual_cutoff_at, closed_at
)
SELECT backfill.authorization_id,
       backfill.submission_id,
       backfill.mapping_revision,
       backfill.created_by_account_id,
       backfill.owner_kind,
       backfill.owner_id,
       backfill.payer_organization_id,
       backfill.payer_team_id,
       backfill.service_identity_id,
       backfill.meter_id,
       backfill.maximum_units,
       CASE
           WHEN backfill.state = 'closed' THEN 'resource_deleted'
           ELSE 'active'
       END,
       backfill.authorization_revision,
       CASE
           WHEN backfill.state = 'closure_pending'
                 OR backfill.requires_resource_deletion
               THEN 'resource_deletion_pending'
           WHEN backfill.state = 'closed' THEN 'closed'
           ELSE 'open'
       END,
       CASE
           WHEN backfill.state IN ('closure_pending', 'closed')
               THEN backfill.updated_at
           WHEN backfill.requires_resource_deletion
               THEN backfill.submission_updated_at
           ELSE NULL
       END,
       CASE
           WHEN backfill.state = 'closed' THEN backfill.updated_at
           ELSE NULL
       END
FROM binding_backfill AS backfill;

-- Each interval is an immutable attribution of one retained image byte count
-- to one authorization. Its end may be set once by deletion, grace, or rebind.
CREATE TABLE realqa_storage_retention_intervals (
    authorization_id uuid NOT NULL
        REFERENCES realqa_storage_authorization_bindings(authorization_id)
        ON DELETE RESTRICT,
    asset_id uuid NOT NULL,
    retained_bytes bigint NOT NULL CHECK (retained_bytes > 0),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (authorization_id, asset_id, starts_at),
    CHECK (realqa_is_uuid_v7(authorization_id)),
    CHECK (realqa_is_uuid_v7(asset_id)),
    CHECK (ends_at IS NULL OR ends_at >= starts_at)
);

CREATE UNIQUE INDEX realqa_storage_retention_intervals_open_asset
ON realqa_storage_retention_intervals (asset_id)
WHERE ends_at IS NULL;

CREATE INDEX realqa_storage_retention_intervals_period
ON realqa_storage_retention_intervals (
    authorization_id, starts_at, ends_at
);

INSERT INTO realqa_storage_retention_intervals (
    authorization_id, asset_id, retained_bytes, starts_at
)
SELECT attempt.authorization_id,
       asset.id,
       asset.encoded_bytes,
       COALESCE(submission.submitted_at, asset.verified_at, asset.created_at)
FROM realqa_assets AS asset
JOIN realqa_submissions AS submission ON submission.id = asset.submission_id
JOIN realqa_storage_authorization_attempts AS attempt
  ON attempt.submission_id = submission.id
WHERE asset.state = 'public_retained'
  AND asset.upload_state = 'verified'
  AND asset.encoded_bytes > 0
  AND attempt.authorization_id IS NOT NULL
  AND attempt.state = 'active'
ON CONFLICT DO NOTHING;

CREATE TABLE realqa_storage_daily_settlements (
    authorization_id uuid NOT NULL
        REFERENCES realqa_storage_authorization_bindings(authorization_id)
        ON DELETE RESTRICT,
    period_start timestamptz NOT NULL,
    byte_seconds bigint NOT NULL CHECK (byte_seconds >= 0),
    units bigint NOT NULL CHECK (units >= 0),
    state text NOT NULL CHECK (state IN (
        'pending', 'reserved', 'committed', 'released',
        'settled_zero', 'grace_skipped'
    )),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    reserve_idempotency_key uuid NOT NULL,
    commit_idempotency_key uuid NOT NULL,
    release_idempotency_key uuid NOT NULL,
    reservation_id uuid,
    reservation_created_at timestamptz,
    reservation_expires_at timestamptz,
    reservation_price_version_id uuid,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_attempted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    settled_at timestamptz,
    PRIMARY KEY (authorization_id, period_start),
    CHECK (
        period_start =
            date_trunc('day', period_start AT TIME ZONE 'UTC')
                AT TIME ZONE 'UTC'
    ),
    CHECK (realqa_is_uuid_v7(authorization_id)),
    CHECK (realqa_is_uuid_v7(reserve_idempotency_key)),
    CHECK (realqa_is_uuid_v7(commit_idempotency_key)),
    CHECK (realqa_is_uuid_v7(release_idempotency_key)),
    CHECK (reservation_id IS NULL OR realqa_is_uuid_v7(reservation_id)),
    CHECK (
        reservation_price_version_id IS NULL
        OR realqa_is_uuid_v7(reservation_price_version_id)
    ),
    CHECK (
        (
            state = 'pending'
            AND units > 0
            AND reservation_id IS NULL
            AND reservation_created_at IS NULL
            AND reservation_expires_at IS NULL
            AND reservation_price_version_id IS NULL
            AND settled_at IS NULL
        )
        OR
        (
            state = 'reserved'
            AND units > 0
            AND reservation_id IS NOT NULL
            AND reservation_created_at IS NOT NULL
            AND reservation_expires_at > reservation_created_at
            AND reservation_price_version_id IS NOT NULL
            AND settled_at IS NULL
        )
        OR
        (
            state IN ('committed', 'released')
            AND units > 0
            AND reservation_id IS NOT NULL
            AND reservation_created_at IS NOT NULL
            AND reservation_expires_at > reservation_created_at
            AND reservation_price_version_id IS NOT NULL
            AND settled_at IS NOT NULL
        )
        OR
        (
            state IN ('settled_zero', 'grace_skipped')
            AND reservation_id IS NULL
            AND reservation_created_at IS NULL
            AND reservation_expires_at IS NULL
            AND reservation_price_version_id IS NULL
            AND settled_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX realqa_storage_daily_settlements_reservation
ON realqa_storage_daily_settlements (reservation_id)
WHERE reservation_id IS NOT NULL;

CREATE INDEX realqa_storage_daily_settlements_pending
ON realqa_storage_daily_settlements (period_start, authorization_id)
WHERE state IN ('pending', 'reserved');

CREATE TABLE realqa_storage_recoveries (
    id uuid PRIMARY KEY,
    submission_id uuid NOT NULL
        REFERENCES realqa_submissions(id) ON DELETE RESTRICT,
    authorization_id uuid NOT NULL
        REFERENCES realqa_storage_authorization_bindings(authorization_id)
        ON DELETE RESTRICT,
    reason text NOT NULL CHECK (reason IN (
        'authorization_revoked', 'authorization_access_lost',
        'payment_required', 'overage_required', 'billing_unavailable',
        'github_disconnected', 'security_conflict'
    )),
    notification_state text NOT NULL DEFAULT 'pending' CHECK (
        notification_state IN ('pending', 'notified')
    ),
    grace_started_at timestamptz NOT NULL,
    grace_expires_at timestamptz NOT NULL,
    recovered_at timestamptz,
    expired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(submission_id)),
    CHECK (realqa_is_uuid_v7(authorization_id)),
    CHECK (grace_expires_at = grace_started_at + interval '30 days'),
    CHECK (recovered_at IS NULL OR expired_at IS NULL),
    UNIQUE (submission_id, id)
);

CREATE UNIQUE INDEX realqa_storage_recoveries_unresolved
ON realqa_storage_recoveries (submission_id)
WHERE recovered_at IS NULL AND expired_at IS NULL;

CREATE INDEX realqa_storage_recoveries_expiry
ON realqa_storage_recoveries (grace_expires_at, submission_id)
WHERE recovered_at IS NULL AND expired_at IS NULL;

CREATE TABLE realqa_storage_rebind_attempts (
    submission_id uuid NOT NULL
        REFERENCES realqa_submissions(id) ON DELETE RESTRICT,
    caller_digest bytea NOT NULL CHECK (octet_length(caller_digest) = 32),
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    expected_authorization_id uuid NOT NULL,
    expected_mapping_revision bigint NOT NULL
        CHECK (expected_mapping_revision > 0),
    replacement_organization_id uuid NOT NULL,
    replacement_team_id uuid NOT NULL,
    replacement_maximum_units bigint NOT NULL
        CHECK (replacement_maximum_units > 0),
    replacement_service_identity_id uuid NOT NULL,
    replacement_meter_id uuid NOT NULL,
    revoke_idempotency_key uuid NOT NULL,
    create_idempotency_key uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'completed')),
    replacement_authorization_id uuid,
    replacement_authorization_revision bigint,
    resulting_mapping_revision bigint,
    cutoff_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    completed_at timestamptz,
    PRIMARY KEY (caller_digest, idempotency_key),
    CHECK (realqa_is_uuid_v7(submission_id)),
    CHECK (realqa_is_uuid_v7(idempotency_key)),
    CHECK (realqa_is_uuid_v7(expected_authorization_id)),
    CHECK (realqa_is_uuid_v7(replacement_organization_id)),
    CHECK (realqa_is_uuid_v7(replacement_team_id)),
    CHECK (realqa_is_uuid_v7(replacement_service_identity_id)),
    CHECK (realqa_is_uuid_v7(replacement_meter_id)),
    CHECK (realqa_is_uuid_v7(revoke_idempotency_key)),
    CHECK (realqa_is_uuid_v7(create_idempotency_key)),
    CHECK (
        (
            state = 'pending'
            AND replacement_authorization_id IS NULL
            AND replacement_authorization_revision IS NULL
            AND resulting_mapping_revision IS NULL
            AND cutoff_at IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            state = 'completed'
            AND realqa_is_uuid_v7(replacement_authorization_id)
            AND replacement_authorization_revision > 0
            AND resulting_mapping_revision > expected_mapping_revision
            AND cutoff_at IS NOT NULL
            AND completed_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX realqa_storage_rebind_attempts_pending_submission
ON realqa_storage_rebind_attempts (submission_id)
WHERE state = 'pending';

CREATE FUNCTION realqa_preserve_storage_authorization_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.authorization_id IS DISTINCT FROM OLD.authorization_id
       OR NEW.submission_id IS DISTINCT FROM OLD.submission_id
       OR NEW.mapping_revision IS DISTINCT FROM OLD.mapping_revision
       OR NEW.authorizer_account_id IS DISTINCT FROM OLD.authorizer_account_id
       OR NEW.owner_kind IS DISTINCT FROM OLD.owner_kind
       OR NEW.owner_id IS DISTINCT FROM OLD.owner_id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.team_id IS DISTINCT FROM OLD.team_id
       OR NEW.service_identity_id IS DISTINCT FROM OLD.service_identity_id
       OR NEW.meter_id IS DISTINCT FROM OLD.meter_id
       OR NEW.maximum_units IS DISTINCT FROM OLD.maximum_units
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (
           OLD.accrual_cutoff_at IS NOT NULL
           AND (
               NEW.accrual_cutoff_at IS NULL
               OR NEW.accrual_cutoff_at > OLD.accrual_cutoff_at
           )
       )
       OR (OLD.closure_state = 'closed' AND NEW IS DISTINCT FROM OLD) THEN
        RAISE EXCEPTION 'RealQA storage authorization history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_storage_authorization_bindings_preserve
BEFORE UPDATE OR DELETE ON realqa_storage_authorization_bindings
FOR EACH ROW EXECUTE FUNCTION
    realqa_preserve_storage_authorization_binding();

CREATE FUNCTION realqa_preserve_storage_retention_interval()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.authorization_id IS DISTINCT FROM OLD.authorization_id
       OR NEW.asset_id IS DISTINCT FROM OLD.asset_id
       OR NEW.retained_bytes IS DISTINCT FROM OLD.retained_bytes
       OR NEW.starts_at IS DISTINCT FROM OLD.starts_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (
           OLD.ends_at IS NOT NULL
           AND (NEW.ends_at IS NULL OR NEW.ends_at > OLD.ends_at)
       ) THEN
        RAISE EXCEPTION 'RealQA storage retention history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_storage_retention_intervals_preserve
BEFORE UPDATE OR DELETE ON realqa_storage_retention_intervals
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_storage_retention_interval();

CREATE FUNCTION realqa_preserve_storage_daily_settlement()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.authorization_id IS DISTINCT FROM OLD.authorization_id
       OR NEW.period_start IS DISTINCT FROM OLD.period_start
       OR NEW.byte_seconds IS DISTINCT FROM OLD.byte_seconds
       OR NEW.units IS DISTINCT FROM OLD.units
       OR NEW.request_digest IS DISTINCT FROM OLD.request_digest
       OR NEW.reserve_idempotency_key IS DISTINCT FROM
            OLD.reserve_idempotency_key
       OR NEW.commit_idempotency_key IS DISTINCT FROM
            OLD.commit_idempotency_key
       OR NEW.release_idempotency_key IS DISTINCT FROM
            OLD.release_idempotency_key
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (
           OLD.reservation_id IS NOT NULL
           AND (
               NEW.reservation_id IS DISTINCT FROM OLD.reservation_id
               OR NEW.reservation_created_at IS DISTINCT FROM
                    OLD.reservation_created_at
               OR NEW.reservation_expires_at IS DISTINCT FROM
                    OLD.reservation_expires_at
               OR NEW.reservation_price_version_id IS DISTINCT FROM
                    OLD.reservation_price_version_id
           )
       )
       OR (
           OLD.state IN (
               'committed', 'released', 'settled_zero', 'grace_skipped'
           )
           AND NEW IS DISTINCT FROM OLD
       ) THEN
        RAISE EXCEPTION 'RealQA storage settlement history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_storage_daily_settlements_preserve
BEFORE UPDATE OR DELETE ON realqa_storage_daily_settlements
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_storage_daily_settlement();

CREATE FUNCTION realqa_preserve_storage_recovery()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.id IS DISTINCT FROM OLD.id
       OR NEW.submission_id IS DISTINCT FROM OLD.submission_id
       OR NEW.authorization_id IS DISTINCT FROM OLD.authorization_id
       OR NEW.reason IS DISTINCT FROM OLD.reason
       OR NEW.grace_started_at IS DISTINCT FROM OLD.grace_started_at
       OR NEW.grace_expires_at IS DISTINCT FROM OLD.grace_expires_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.notification_state = 'notified'
           AND NEW.notification_state <> 'notified')
       OR (OLD.recovered_at IS NOT NULL
           AND NEW.recovered_at IS DISTINCT FROM OLD.recovered_at)
       OR (OLD.expired_at IS NOT NULL
           AND NEW.expired_at IS DISTINCT FROM OLD.expired_at) THEN
        RAISE EXCEPTION 'RealQA storage recovery history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_storage_recoveries_preserve
BEFORE UPDATE OR DELETE ON realqa_storage_recoveries
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_storage_recovery();

CREATE FUNCTION realqa_preserve_storage_rebind_attempt()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.submission_id IS DISTINCT FROM OLD.submission_id
       OR NEW.caller_digest IS DISTINCT FROM OLD.caller_digest
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.request_digest IS DISTINCT FROM OLD.request_digest
       OR NEW.expected_authorization_id IS DISTINCT FROM
            OLD.expected_authorization_id
       OR NEW.expected_mapping_revision IS DISTINCT FROM
            OLD.expected_mapping_revision
       OR NEW.replacement_organization_id IS DISTINCT FROM
            OLD.replacement_organization_id
       OR NEW.replacement_team_id IS DISTINCT FROM OLD.replacement_team_id
       OR NEW.replacement_maximum_units IS DISTINCT FROM
            OLD.replacement_maximum_units
       OR NEW.replacement_service_identity_id IS DISTINCT FROM
            OLD.replacement_service_identity_id
       OR NEW.replacement_meter_id IS DISTINCT FROM
            OLD.replacement_meter_id
       OR NEW.revoke_idempotency_key IS DISTINCT FROM
            OLD.revoke_idempotency_key
       OR NEW.create_idempotency_key IS DISTINCT FROM
            OLD.create_idempotency_key
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.state = 'completed' AND NEW IS DISTINCT FROM OLD) THEN
        RAISE EXCEPTION 'RealQA storage rebind history is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_storage_rebind_attempts_preserve
BEFORE UPDATE OR DELETE ON realqa_storage_rebind_attempts
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_storage_rebind_attempt();

ALTER TABLE realqa_idempotency_records
    DROP CONSTRAINT realqa_idempotency_records_operation_check,
    ADD CONSTRAINT realqa_idempotency_records_operation_check
        CHECK (operation IN (
            'create_preset',
            'create_submission',
            'create_image_upload',
            'finalize_image_upload',
            'submit_issue',
            'rebind_submission_storage_authorization',
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
        'storage_daily_reserved',
        'storage_daily_committed',
        'storage_daily_released',
        'storage_billing_grace_started',
        'storage_authorization_rebound',
        'storage_rebind_replacement_closed',
        'storage_authorization_closed',
        'issue_submission_started',
        'issue_reconciled',
        'submission_completed',
        'image_deleted',
        'submission_assets_deleted'
    ));
