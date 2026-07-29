ALTER TABLE deck_views
    ADD COLUMN repository_authorization_index bytea,
    ADD CONSTRAINT deck_views_repository_authorization_index_check
        CHECK (
            repository_authorization_index IS NULL
            OR (
                octet_length(repository_authorization_index) >= 1
                AND get_byte(repository_authorization_index, 0) = 1
                AND (octet_length(repository_authorization_index) - 1) % 32 = 0
            )
        );

-- Pre-index view definitions remain disconnected and readable by their owner,
-- but cannot be connected until a caller-authorized update writes the index.
-- This avoids opening existing encrypted queries during a migration.
