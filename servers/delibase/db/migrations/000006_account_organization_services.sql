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

CREATE OR REPLACE FUNCTION preserve_organization_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (
           NEW.organization_id IS DISTINCT FROM OLD.organization_id
           OR NEW.account_id IS DISTINCT FROM OLD.account_id
       ) THEN
        RAISE EXCEPTION 'organization membership identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.role <> 'owner' THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;
    IF TG_OP = 'UPDATE'
       AND NEW.organization_id = OLD.organization_id
       AND NEW.account_id = OLD.account_id
       AND NEW.role = 'owner' THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM organizations
    WHERE id = OLD.organization_id
      AND deleted_at IS NULL
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM organization_memberships AS membership
        JOIN accounts AS account ON account.id = membership.account_id
        WHERE membership.organization_id = OLD.organization_id
          AND membership.role = 'owner'
          AND membership.account_id <> OLD.account_id
          AND account.status = 'active'
    ) THEN
        RAISE EXCEPTION 'organization must retain at least one owner'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE OR REPLACE FUNCTION preserve_active_organization_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM organization.id
    FROM organizations AS organization
    JOIN organization_memberships AS membership
      ON membership.organization_id = organization.id
    WHERE membership.account_id = NEW.id
      AND membership.role = 'owner'
      AND organization.deleted_at IS NULL
    ORDER BY organization.id
    FOR UPDATE OF organization;

    IF EXISTS (
        SELECT 1
        FROM organization_memberships AS affected_membership
        JOIN organizations AS organization
          ON organization.id = affected_membership.organization_id
        WHERE affected_membership.account_id = NEW.id
          AND affected_membership.role = 'owner'
          AND organization.deleted_at IS NULL
          AND NOT EXISTS (
              SELECT 1
              FROM organization_memberships AS owner_membership
              JOIN accounts AS owner_account
                ON owner_account.id = owner_membership.account_id
              WHERE owner_membership.organization_id = organization.id
                AND owner_membership.role = 'owner'
                AND owner_account.status = 'active'
          )
    ) THEN
        RAISE EXCEPTION 'organization must retain at least one active owner'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
