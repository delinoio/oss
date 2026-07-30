CREATE TABLE deck_github_removed_repositories (
    connection_id uuid NOT NULL
        REFERENCES deck_connections(connection_id) ON DELETE CASCADE,
    repository_hash bytea NOT NULL
        CHECK (octet_length(repository_hash) = 32),
    removed_at timestamptz NOT NULL,
    PRIMARY KEY (connection_id, repository_hash)
);
