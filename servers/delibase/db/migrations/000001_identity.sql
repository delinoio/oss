CREATE FUNCTION is_uuid_v7(value uuid)
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

CREATE TABLE accounts (
    id uuid PRIMARY KEY,
    logto_subject text NOT NULL UNIQUE,
    display_name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (is_uuid_v7(id)),
    CHECK (length(logto_subject) BETWEEN 1 AND 255)
);

CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    slug text NOT NULL UNIQUE,
    overage_limit_micros bigint NOT NULL DEFAULT 0 CHECK (overage_limit_micros >= 0),
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (is_uuid_v7(id)),
    CHECK (length(name) BETWEEN 1 AND 120),
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$')
);

CREATE TABLE organization_slug_aliases (
    slug text PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$')
);

CREATE TABLE organization_slug_registry (
    slug text PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$')
);

CREATE FUNCTION register_current_organization_slug()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.slug = OLD.slug THEN
            RETURN NEW;
        END IF;
        DELETE FROM organization_slug_registry
        WHERE slug = OLD.slug AND organization_id = OLD.id;
        DELETE FROM organization_slug_aliases
        WHERE slug = NEW.slug AND organization_id = NEW.id;
        INSERT INTO organization_slug_aliases (slug, organization_id)
        VALUES (OLD.slug, OLD.id);
    END IF;
    INSERT INTO organization_slug_registry (slug, organization_id)
    VALUES (NEW.slug, NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER organizations_register_slug
AFTER INSERT OR UPDATE OF slug ON organizations
FOR EACH ROW EXECUTE FUNCTION register_current_organization_slug();

CREATE FUNCTION register_organization_slug_alias()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        DELETE FROM organization_slug_registry
        WHERE slug = OLD.slug AND organization_id = OLD.organization_id;
    END IF;
    INSERT INTO organization_slug_registry (slug, organization_id)
    VALUES (NEW.slug, NEW.organization_id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER organization_slug_aliases_register_slug
AFTER INSERT OR UPDATE OF slug, organization_id ON organization_slug_aliases
FOR EACH ROW EXECUTE FUNCTION register_organization_slug_alias();

CREATE FUNCTION unregister_organization_slug_alias()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM organization_slug_registry
    WHERE slug = OLD.slug AND organization_id = OLD.organization_id;
    RETURN OLD;
END;
$$;

CREATE TRIGGER organization_slug_aliases_unregister_slug
AFTER DELETE ON organization_slug_aliases
FOR EACH ROW EXECUTE FUNCTION unregister_organization_slug_alias();

CREATE TABLE organization_memberships (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (organization_id, account_id)
);

CREATE INDEX organization_memberships_account_idx
    ON organization_memberships(account_id, organization_id);

CREATE FUNCTION require_organization_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM organizations
        WHERE id = NEW.id
    ) AND NOT EXISTS (
        SELECT 1
        FROM organization_memberships
        WHERE organization_id = NEW.id
          AND role = 'owner'
    ) THEN
        RAISE EXCEPTION 'organization must have at least one owner'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER organizations_require_owner
AFTER INSERT ON organizations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_organization_owner();

CREATE FUNCTION preserve_organization_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.role <> 'owner' THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;
    IF TG_OP = 'UPDATE'
       AND NEW.organization_id = OLD.organization_id
       AND NEW.role = 'owner' THEN
        RETURN NEW;
    END IF;

    -- Serialize owner removal for one organization so concurrent demotions
    -- cannot both observe another owner and leave the organization ownerless.
    PERFORM 1
    FROM organizations
    WHERE id = OLD.organization_id
    FOR UPDATE;
    IF NOT FOUND THEN
        -- Organization deletion intentionally cascades its memberships.
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM organization_memberships
        WHERE organization_id = OLD.organization_id
          AND role = 'owner'
          AND account_id <> OLD.account_id
    ) THEN
        RAISE EXCEPTION 'organization must retain at least one owner'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER organization_memberships_preserve_owner
BEFORE DELETE OR UPDATE OF organization_id, account_id, role
ON organization_memberships
FOR EACH ROW EXECUTE FUNCTION preserve_organization_owner();

CREATE TABLE teams (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_team_id uuid,
    name text NOT NULL,
    protected_general boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id, id),
    UNIQUE NULLS NOT DISTINCT (organization_id, parent_team_id, name),
    FOREIGN KEY (organization_id, parent_team_id)
        REFERENCES teams(organization_id, id) ON DELETE CASCADE,
    CHECK (is_uuid_v7(id)),
    CHECK (length(name) BETWEEN 1 AND 120),
    CHECK (
        NOT protected_general
        OR (parent_team_id IS NULL AND name = 'General')
    )
);

CREATE UNIQUE INDEX teams_one_general_per_organization_idx
    ON teams(organization_id) WHERE protected_general;
CREATE INDEX teams_parent_idx ON teams(organization_id, parent_team_id);

CREATE FUNCTION require_organization_general_team()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM organizations
        WHERE id = NEW.id
    ) AND NOT EXISTS (
        SELECT 1
        FROM teams
        WHERE organization_id = NEW.id
          AND protected_general
    ) THEN
        RAISE EXCEPTION 'organization must have a protected General team'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER organizations_require_general_team
AFTER INSERT ON organizations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_organization_general_team();

CREATE FUNCTION enforce_team_hierarchy()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_depth integer := 0;
    subtree_height integer := 1;
    creates_cycle boolean := false;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.organization_id IS DISTINCT FROM OLD.organization_id THEN
        RAISE EXCEPTION 'teams cannot move between organizations'
            USING ERRCODE = 'check_violation';
    END IF;

    -- Serialize hierarchy validation for one organization so concurrent moves
    -- cannot each validate against the other's previous parent.
    PERFORM 1
    FROM organizations
    WHERE id IN (
        NEW.organization_id,
        CASE
            WHEN TG_OP = 'UPDATE' THEN OLD.organization_id
            ELSE NEW.organization_id
        END
    )
    ORDER BY id
    FOR UPDATE;

    IF NEW.parent_team_id = NEW.id THEN
        RAISE EXCEPTION 'team hierarchy cannot contain a cycle'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.parent_team_id IS NOT NULL THEN
        WITH RECURSIVE ancestors AS (
            SELECT
                team.id,
                team.parent_team_id,
                1 AS depth,
                ARRAY[team.id] AS path
            FROM teams AS team
            WHERE team.organization_id = NEW.organization_id
              AND team.id = NEW.parent_team_id

            UNION ALL

            SELECT
                parent.id,
                parent.parent_team_id,
                ancestors.depth + 1,
                ancestors.path || parent.id
            FROM teams AS parent
            JOIN ancestors ON parent.id = ancestors.parent_team_id
            WHERE parent.organization_id = NEW.organization_id
              AND NOT parent.id = ANY(ancestors.path)
        )
        SELECT
            COALESCE(max(depth), 0),
            COALESCE(bool_or(id = NEW.id), false)
        INTO parent_depth, creates_cycle
        FROM ancestors;

        IF creates_cycle THEN
            RAISE EXCEPTION 'team hierarchy cannot contain a cycle'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        WITH RECURSIVE descendants AS (
            SELECT team.id, 1 AS depth
            FROM teams AS team
            WHERE team.organization_id = OLD.organization_id
              AND team.id = OLD.id

            UNION ALL

            SELECT child.id, descendants.depth + 1
            FROM teams AS child
            JOIN descendants ON child.parent_team_id = descendants.id
            WHERE child.organization_id = OLD.organization_id
        )
        SELECT COALESCE(max(depth), 1)
        INTO subtree_height
        FROM descendants;
    END IF;

    IF parent_depth + subtree_height > 5 THEN
        RAISE EXCEPTION 'team hierarchy exceeds five levels'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER teams_enforce_hierarchy
BEFORE INSERT OR UPDATE OF organization_id, parent_team_id
ON teams
FOR EACH ROW EXECUTE FUNCTION enforce_team_hierarchy();

CREATE FUNCTION protect_general_team()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT OLD.protected_general THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    IF TG_OP = 'DELETE' THEN
        IF EXISTS (
            SELECT 1 FROM organizations WHERE id = OLD.organization_id
        ) THEN
            RAISE EXCEPTION 'protected General team cannot be deleted'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN OLD;
    END IF;

    IF NOT NEW.protected_general
       OR NEW.id <> OLD.id
       OR NEW.organization_id <> OLD.organization_id
       OR NEW.parent_team_id IS DISTINCT FROM OLD.parent_team_id
       OR NEW.name <> OLD.name THEN
        RAISE EXCEPTION 'protected General team cannot be renamed or unprotected'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER teams_protect_general
BEFORE UPDATE OR DELETE ON teams
FOR EACH ROW EXECUTE FUNCTION protect_general_team();

CREATE TABLE team_memberships (
    organization_id uuid NOT NULL,
    team_id uuid NOT NULL,
    account_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (team_id, account_id),
    FOREIGN KEY (organization_id, team_id)
        REFERENCES teams(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, account_id)
        REFERENCES organization_memberships(organization_id, account_id) ON DELETE CASCADE
);

CREATE INDEX team_memberships_account_idx
    ON team_memberships(account_id, organization_id, team_id);

CREATE TABLE organization_invitations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    organization_role text NOT NULL CHECK (organization_role IN ('admin', 'member')),
    target_team_id uuid,
    team_role text CHECK (team_role IN ('admin', 'member')),
    created_by_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    FOREIGN KEY (organization_id, target_team_id)
        REFERENCES teams(organization_id, id) ON DELETE CASCADE,
    CHECK (is_uuid_v7(id)),
    CHECK (octet_length(token_hash) >= 32),
    CHECK (
        (organization_role = 'admin' AND target_team_id IS NULL AND team_role IS NULL)
        OR
        (organization_role = 'member' AND target_team_id IS NOT NULL AND team_role IS NOT NULL)
    ),
    CHECK (
        expires_at > created_at
        AND expires_at <= created_at + interval '7 days'
    )
);

CREATE INDEX organization_invitations_active_idx
    ON organization_invitations(organization_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE FUNCTION preserve_organization_invitation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.token_hash IS DISTINCT FROM OLD.token_hash
       OR NEW.organization_role IS DISTINCT FROM OLD.organization_role
       OR NEW.target_team_id IS DISTINCT FROM OLD.target_team_id
       OR NEW.team_role IS DISTINCT FROM OLD.team_role
       OR NEW.created_by_account_id IS DISTINCT FROM OLD.created_by_account_id
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (
           OLD.revoked_at IS NOT NULL
           AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at
       ) THEN
        RAISE EXCEPTION 'organization invitation terms are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
        NEW.revoked_at := transaction_timestamp();
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER organization_invitations_preserve_terms
BEFORE UPDATE ON organization_invitations
FOR EACH ROW EXECUTE FUNCTION preserve_organization_invitation();

CREATE TABLE organization_invitation_acceptances (
    invitation_id uuid NOT NULL REFERENCES organization_invitations(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (invitation_id, account_id)
);

CREATE FUNCTION validate_organization_invitation_acceptance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    invitation_created_at timestamptz;
    invitation_expires_at timestamptz;
    invitation_revoked_at timestamptz;
BEGIN
    SELECT created_at, expires_at, revoked_at
    INTO invitation_created_at, invitation_expires_at, invitation_revoked_at
    FROM organization_invitations
    WHERE id = NEW.invitation_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'invitation does not exist'
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    NEW.accepted_at := transaction_timestamp();
    IF invitation_revoked_at IS NOT NULL
       OR NEW.accepted_at < invitation_created_at
       OR invitation_expires_at <= NEW.accepted_at THEN
        RAISE EXCEPTION 'invitation is no longer valid'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER organization_invitation_acceptances_validate
BEFORE INSERT ON organization_invitation_acceptances
FOR EACH ROW EXECUTE FUNCTION validate_organization_invitation_acceptance();
