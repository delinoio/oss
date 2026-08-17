ALTER TABLE devhud_users
    ADD COLUMN search_display_name text NOT NULL DEFAULT '',
    ADD COLUMN search_email text NOT NULL DEFAULT '',
    ADD COLUMN search_logto_subject text NOT NULL DEFAULT '';

CREATE INDEX devhud_users_admin_list_idx
    ON devhud_users (created_at DESC, user_id DESC);
CREATE INDEX devhud_users_search_display_name_idx
    ON devhud_users (search_display_name text_pattern_ops);
CREATE INDEX devhud_users_search_email_idx
    ON devhud_users (search_email text_pattern_ops);
CREATE INDEX devhud_users_search_logto_subject_idx
    ON devhud_users (search_logto_subject text_pattern_ops);

ALTER TABLE devhud_audit_events
    ADD COLUMN correlation_id uuid,
    ADD COLUMN outcome smallint NOT NULL DEFAULT 1 CHECK (outcome IN (1, 2)),
    ADD COLUMN rejection_reason smallint CHECK (rejection_reason BETWEEN 1 AND 8);

ALTER TABLE devhud_audit_events
    DROP CONSTRAINT devhud_audit_events_action_check,
    ADD CONSTRAINT devhud_audit_events_action_check CHECK (action BETWEEN 1 AND 9);

UPDATE devhud_audit_events SET correlation_id = audit_event_id
    WHERE correlation_id IS NULL;

ALTER TABLE devhud_audit_events
    ALTER COLUMN correlation_id SET NOT NULL,
    ADD CONSTRAINT devhud_audit_events_outcome_consistent CHECK (
        (outcome = 1 AND rejection_reason IS NULL)
        OR (outcome = 2 AND rejection_reason IS NOT NULL)
    );

CREATE INDEX devhud_audit_events_admin_list_idx
    ON devhud_audit_events (created_at DESC, audit_event_id DESC);
CREATE INDEX devhud_audit_events_correlation_idx
    ON devhud_audit_events (correlation_id);

ALTER TABLE devhud_uploads
    ADD COLUMN removal_audit_event_id uuid,
    ADD COLUMN removal_audit_actor_user_id uuid,
    ADD COLUMN removal_audit_reason text,
    ADD COLUMN removal_audit_created_at timestamptz,
    ADD COLUMN removal_audit_expires_at timestamptz,
    ADD COLUMN removal_audit_correlation_id uuid,
    ADD CONSTRAINT devhud_uploads_removal_audit_complete CHECK (
        (removal_audit_event_id IS NULL
            AND removal_audit_actor_user_id IS NULL
            AND removal_audit_reason IS NULL
            AND removal_audit_created_at IS NULL
            AND removal_audit_expires_at IS NULL
            AND removal_audit_correlation_id IS NULL)
        OR
        (removal_audit_event_id IS NOT NULL
            AND removal_audit_actor_user_id IS NOT NULL
            AND removal_audit_reason IS NOT NULL
            AND removal_audit_created_at IS NOT NULL
            AND removal_audit_expires_at IS NOT NULL
            AND removal_audit_correlation_id IS NOT NULL
            AND octet_length(removal_audit_reason) BETWEEN 1 AND 4096)
    );
