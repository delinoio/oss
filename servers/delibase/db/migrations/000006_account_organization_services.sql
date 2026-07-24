ALTER TABLE audit_events
    ADD COLUMN retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    ADD CONSTRAINT audit_events_retention_check
        CHECK (retain_until >= occurred_at + interval '7 years');

ALTER TABLE ledger_entries
    ADD COLUMN retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    ADD CONSTRAINT ledger_entries_retention_check
        CHECK (retain_until >= created_at + interval '7 years');

ALTER TABLE usage_records
    ADD COLUMN retain_until timestamptz NOT NULL
        DEFAULT (statement_timestamp() + interval '7 years'),
    ADD CONSTRAINT usage_records_retention_check
        CHECK (retain_until >= committed_at + interval '7 years');

CREATE TABLE deletion_tombstones (
    entity_type text NOT NULL CHECK (entity_type IN ('account', 'organization')),
    entity_id uuid NOT NULL,
    actor_reference text NOT NULL
        CHECK (actor_reference ~ '^actor:v1:[0-9a-f]{32}$'),
    deleted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    PRIMARY KEY (entity_type, entity_id),
    CHECK (is_uuid_v7(entity_id)),
    CHECK (retain_until >= deleted_at + interval '7 years')
);

CREATE FUNCTION reject_deletion_tombstone_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'deletion tombstones are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER deletion_tombstones_append_only
BEFORE UPDATE OR DELETE ON deletion_tombstones
FOR EACH ROW EXECUTE FUNCTION reject_deletion_tombstone_mutation();

CREATE TABLE deleted_account_subjects (
    subject_digest bytea PRIMARY KEY CHECK (octet_length(subject_digest) = 32),
    account_id uuid NOT NULL UNIQUE,
    actor_reference text NOT NULL
        CHECK (actor_reference ~ '^actor:v1:[0-9a-f]{32}$'),
    deleted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    retain_until timestamptz NOT NULL
        DEFAULT (transaction_timestamp() + interval '7 years'),
    CHECK (is_uuid_v7(account_id)),
    CHECK (retain_until >= deleted_at + interval '7 years')
);

CREATE TRIGGER deleted_account_subjects_append_only
BEFORE UPDATE OR DELETE ON deleted_account_subjects
FOR EACH ROW EXECUTE FUNCTION reject_deletion_tombstone_mutation();
