CREATE TABLE realqa_repository_schema_revisions (
    installation_id uuid NOT NULL
        REFERENCES realqa_github_installations(id) ON DELETE CASCADE,
    repository_id text NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (installation_id, repository_id),
    CHECK (length(repository_id) BETWEEN 1 AND 255)
);

INSERT INTO realqa_repository_schema_revisions (
    installation_id, repository_id, revision
)
SELECT
    installation_id, repository_id, max(revision)
FROM realqa_repository_definitions
GROUP BY installation_id, repository_id;
