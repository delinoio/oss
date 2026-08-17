CREATE SEQUENCE devhud_upload_generation_seq AS bigint MINVALUE 1;

CREATE TABLE devhud_submissions (
    submission_id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL REFERENCES devhud_users(user_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

CREATE INDEX devhud_submissions_owner_idx
    ON devhud_submissions (owner_user_id, created_at DESC, submission_id DESC);

CREATE TABLE devhud_upload_groups (
    upload_group_id uuid PRIMARY KEY,
    submission_id uuid NOT NULL REFERENCES devhud_submissions(submission_id) ON DELETE CASCADE,
    owner_user_id uuid NOT NULL REFERENCES devhud_users(user_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    CONSTRAINT devhud_upload_groups_submission_owner_unique
        UNIQUE (upload_group_id, submission_id, owner_user_id)
);

CREATE TABLE devhud_upload_reservations (
    reservation_id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL REFERENCES devhud_users(user_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    signed_url_expires_at timestamptz NOT NULL,
    staging_expires_at timestamptz NOT NULL,
    CHECK (signed_url_expires_at <= created_at + interval '15 minutes'),
    CHECK (staging_expires_at <= created_at + interval '24 hours')
);

CREATE INDEX devhud_upload_reservations_owner_hour_idx
    ON devhud_upload_reservations (owner_user_id, created_at DESC);
CREATE INDEX devhud_upload_reservations_expiry_idx
    ON devhud_upload_reservations (staging_expires_at, reservation_id);

CREATE TABLE devhud_uploads (
    upload_id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL REFERENCES devhud_users(user_id) ON DELETE CASCADE,
    submission_id uuid NOT NULL REFERENCES devhud_submissions(submission_id) ON DELETE CASCADE,
    upload_group_id uuid NOT NULL,
    reservation_id uuid NOT NULL UNIQUE REFERENCES devhud_upload_reservations(reservation_id) ON DELETE CASCADE,
    public_id char(43) NOT NULL UNIQUE CHECK (public_id ~ '^[A-Za-z0-9_-]{43}$'),
    staging_id char(43) NOT NULL UNIQUE CHECK (staging_id ~ '^[A-Za-z0-9_-]{43}$'),
    staging_generation bigint NOT NULL DEFAULT nextval('devhud_upload_generation_seq') CHECK (staging_generation > 0),
    expected_size_bytes bigint NOT NULL CHECK (expected_size_bytes BETWEEN 1 AND 52428800),
    expected_sha256 bytea NOT NULL CHECK (octet_length(expected_sha256) = 32),
    content_type smallint NOT NULL CHECK (content_type = 1),
    state smallint NOT NULL DEFAULT 1 CHECK (state BETWEEN 1 AND 8),
    staging_etag text,
	staging_deleted_at timestamptz,
    public_etag text,
    replacement_etag text,
    width integer CHECK (width IS NULL OR width BETWEEN 1 AND 4096),
    height integer CHECK (height IS NULL OR height BETWEEN 1 AND 4096),
    quota_charged_at timestamptz,
    operation_token char(43),
    operation_expires_at timestamptz,
    created_at timestamptz NOT NULL,
    finalized_at timestamptz,
    removed_at timestamptz,
    removal_reason smallint,
    CONSTRAINT devhud_uploads_group_binding_fk
        FOREIGN KEY (upload_group_id, submission_id, owner_user_id)
        REFERENCES devhud_upload_groups(upload_group_id, submission_id, owner_user_id),
    CONSTRAINT devhud_uploads_finalized_fields CHECK (
        (state IN (1, 7, 8) AND finalized_at IS NULL)
        OR (state = 4)
		OR (state = 6)
        OR (state IN (2, 3, 5) AND quota_charged_at IS NOT NULL)
    )
);

CREATE INDEX devhud_uploads_owner_list_idx
    ON devhud_uploads (owner_user_id, created_at DESC, upload_id DESC);
CREATE INDEX devhud_uploads_submission_quota_idx
    ON devhud_uploads (submission_id, state);
CREATE INDEX devhud_uploads_owner_quota_idx
    ON devhud_uploads (owner_user_id, state, quota_charged_at);
CREATE INDEX devhud_uploads_staging_sweep_idx
    ON devhud_uploads (state, reservation_id)
    WHERE state IN (1, 2, 4, 7, 8);

ALTER TABLE devhud_audit_events
    DROP CONSTRAINT devhud_audit_events_action_check,
    ADD CONSTRAINT devhud_audit_events_action_check CHECK (action BETWEEN 1 AND 7);

ALTER TABLE devhud_audit_events
    ADD COLUMN target_upload_id uuid REFERENCES devhud_uploads(upload_id) ON DELETE SET NULL,
    ADD COLUMN reason text CHECK (reason IS NULL OR octet_length(reason) BETWEEN 1 AND 4096);
