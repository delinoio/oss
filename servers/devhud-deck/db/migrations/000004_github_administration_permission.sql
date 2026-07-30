ALTER TABLE deck_connections
    ADD COLUMN github_administration_permission smallint;

-- Existing installations must be refreshed before administration-only
-- operations are advertised; no other retained permission proves this grant.
UPDATE deck_connections
SET github_administration_permission = 0
WHERE github_installation_id IS NOT NULL;

ALTER TABLE deck_connections
    DROP CONSTRAINT deck_connections_github_values_check,
    ADD CONSTRAINT deck_connections_github_values_check CHECK (
        (github_installation_id IS NULL
            AND github_account_id IS NULL
            AND github_account_kind IS NULL
            AND github_account_login_ciphertext IS NULL
            AND github_metadata_permission IS NULL
            AND github_administration_permission IS NULL
            AND github_contents_permission IS NULL
            AND github_pull_requests_permission IS NULL
            AND github_checks_permission IS NULL
            AND github_members_permission IS NULL)
        OR
        (github_installation_id > 0
            AND github_account_id > 0
            AND github_account_kind IN (1, 2)
            AND octet_length(github_account_login_ciphertext) > 0
            AND github_metadata_permission BETWEEN 0 AND 3
            AND github_administration_permission BETWEEN 0 AND 3
            AND github_contents_permission BETWEEN 0 AND 3
            AND github_pull_requests_permission BETWEEN 0 AND 3
            AND github_checks_permission BETWEEN 0 AND 3
            AND github_members_permission BETWEEN 0 AND 3)
    );
