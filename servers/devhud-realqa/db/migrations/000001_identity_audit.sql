CREATE FUNCTION realqa_is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT
        substring(value::text FROM 15 FOR 1) = '7'
        AND substring(value::text FROM 20 FOR 1) IN ('8', '9', 'a', 'b')
$$;

CREATE TABLE realqa_identities (
    account_id uuid PRIMARY KEY,
    subject_digest bytea NOT NULL UNIQUE
        CHECK (octet_length(subject_digest) = 32),
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (realqa_is_uuid_v7(account_id))
);

CREATE TABLE realqa_owner_bindings (
    account_id uuid NOT NULL REFERENCES realqa_identities(account_id) ON DELETE CASCADE,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    payer_organization_id uuid,
    payer_team_id uuid,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (account_id, owner_kind, owner_id),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (payer_organization_id IS NULL OR realqa_is_uuid_v7(payer_organization_id)),
    CHECK (payer_team_id IS NULL OR realqa_is_uuid_v7(payer_team_id)),
    CHECK (
        (owner_kind = 'personal' AND owner_id = account_id)
        OR owner_kind = 'organization'
    ),
    CHECK (
        payer_team_id IS NULL
        OR payer_organization_id IS NOT NULL
    )
);

CREATE INDEX realqa_owner_bindings_owner_idx
    ON realqa_owner_bindings(owner_kind, owner_id, account_id);

CREATE TABLE realqa_payer_team_bindings (
    account_id uuid NOT NULL REFERENCES realqa_identities(account_id) ON DELETE CASCADE,
    organization_id uuid NOT NULL,
    team_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (account_id, organization_id, team_id),
    CHECK (realqa_is_uuid_v7(organization_id)),
    CHECK (realqa_is_uuid_v7(team_id))
);

CREATE TABLE realqa_scope_tombstones (
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    deletion_job_id uuid NOT NULL UNIQUE,
    trigger_kind text NOT NULL CHECK (trigger_kind IN (
        'owner_request',
        'delibase_account_lifecycle',
        'delibase_organization_lifecycle'
    )),
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (owner_kind, owner_id),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (realqa_is_uuid_v7(deletion_job_id))
);

CREATE TABLE realqa_idempotency_records (
    id uuid PRIMARY KEY,
    caller_kind text NOT NULL CHECK (caller_kind IN ('user', 'service')),
    caller_digest bytea NOT NULL CHECK (octet_length(caller_digest) = 32),
    operation text NOT NULL CHECK (operation IN (
        'create_preset',
        'delete_feature_data',
        'disconnect_github_connection'
    )),
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    resource_id uuid NOT NULL,
    response_payload bytea,
    completed_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (caller_kind, caller_digest, operation, idempotency_key),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(idempotency_key)),
    CHECK (realqa_is_uuid_v7(resource_id))
);

CREATE TABLE realqa_audits (
    id uuid PRIMARY KEY,
    event_type text NOT NULL CHECK (event_type IN (
        'preset_created',
        'preset_updated',
        'preset_deleted',
        'github_connection_started',
        'github_connection_disconnected',
        'feature_deletion_accepted',
        'repository_access_denied'
    )),
    actor_reference text NOT NULL,
    owner_kind text CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid,
    resource_id uuid,
    decision text NOT NULL CHECK (decision IN ('allow', 'deny')),
    result text NOT NULL CHECK (result IN ('success', 'failure', 'noop')),
    request_id text,
    trace_id text,
    occurred_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (actor_reference = 'system' OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'),
    CHECK ((owner_kind IS NULL) = (owner_id IS NULL)),
    CHECK (owner_id IS NULL OR realqa_is_uuid_v7(owner_id)),
    CHECK (resource_id IS NULL OR realqa_is_uuid_v7(resource_id)),
    CHECK (request_id IS NULL OR request_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CHECK (trace_id IS NULL OR trace_id ~ '^[0-9a-f]{32}$')
);

CREATE FUNCTION realqa_preserve_audit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'RealQA audits are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER realqa_audits_preserve
BEFORE UPDATE OR DELETE ON realqa_audits
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_audit();
