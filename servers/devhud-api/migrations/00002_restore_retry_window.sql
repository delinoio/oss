ALTER TABLE devhud_users
    ADD COLUMN restore_retry_until timestamptz;

ALTER TABLE devhud_users
    ADD CONSTRAINT devhud_users_restore_retry_consistent CHECK (
        restore_retry_until IS NULL OR deletion_state = 1
    );
