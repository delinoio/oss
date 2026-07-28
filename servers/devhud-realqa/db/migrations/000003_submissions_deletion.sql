CREATE TABLE realqa_submissions (
    id uuid PRIMARY KEY,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    created_by_account_id uuid NOT NULL REFERENCES realqa_identities(account_id),
    preset_id uuid REFERENCES realqa_presets(id) ON DELETE SET NULL,
    destination_id uuid REFERENCES realqa_destinations(id) ON DELETE SET NULL,
    state text NOT NULL CHECK (state IN (
        'draft', 'uploading', 'ready', 'submitting', 'reconciling',
        'submitted', 'failed', 'storage_billing_grace', 'assets_deleted', 'deleted'
    )),
    provider_issue_id text,
    provider_issue_url text,
    idempotency_digest bytea NOT NULL CHECK (octet_length(idempotency_digest) = 32),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    submitted_at timestamptz,
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (
        (provider_issue_id IS NULL AND provider_issue_url IS NULL)
        OR
        (provider_issue_id IS NOT NULL AND provider_issue_url IS NOT NULL)
    )
);

CREATE TABLE realqa_assets (
    id uuid PRIMARY KEY,
    submission_id uuid NOT NULL REFERENCES realqa_submissions(id) ON DELETE CASCADE,
    public_id text UNIQUE,
    object_key_ciphertext bytea,
    state text NOT NULL CHECK (state IN (
        'private_staging', 'verified_unlinked', 'public_retained',
        'removed_placeholder', 'expired', 'deleted'
    )),
    encoded_bytes bigint NOT NULL CHECK (encoded_bytes >= 0),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    removed_at timestamptz,
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (public_id IS NULL OR length(public_id) BETWEEN 22 AND 128)
);

CREATE TABLE realqa_deletion_jobs (
    id uuid PRIMARY KEY,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    trigger_kind text NOT NULL CHECK (trigger_kind IN (
        'owner_request',
        'delibase_account_lifecycle',
        'delibase_organization_lifecycle'
    )),
    status text NOT NULL DEFAULT 'completed'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    already_absent boolean NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    completed_at timestamptz,
    UNIQUE (owner_kind, owner_id),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(owner_id))
);

CREATE FUNCTION realqa_preserve_deletion_job()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.id IS DISTINCT FROM OLD.id
       OR NEW.owner_kind IS DISTINCT FROM OLD.owner_kind
       OR NEW.owner_id IS DISTINCT FROM OLD.owner_id
       OR NEW.trigger_kind IS DISTINCT FROM OLD.trigger_kind
       OR NEW.already_absent IS DISTINCT FROM OLD.already_absent
       OR NEW.accepted_at IS DISTINCT FROM OLD.accepted_at THEN
        RAISE EXCEPTION 'RealQA deletion job identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'completed' AND NEW.status <> 'completed' THEN
        RAISE EXCEPTION 'RealQA deletion completion is terminal'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER realqa_deletion_jobs_preserve
BEFORE UPDATE OR DELETE ON realqa_deletion_jobs
FOR EACH ROW EXECUTE FUNCTION realqa_preserve_deletion_job();
