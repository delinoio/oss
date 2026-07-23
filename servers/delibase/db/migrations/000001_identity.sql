CREATE TABLE accounts (
    id uuid PRIMARY KEY,
    logto_subject text NOT NULL UNIQUE,
    display_name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
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
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
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
        DELETE FROM organization_slug_registry
        WHERE slug = OLD.slug AND organization_id = OLD.id;
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

CREATE TABLE teams (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_team_id uuid,
    name text NOT NULL,
    protected_general boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, parent_team_id, name),
    FOREIGN KEY (organization_id, parent_team_id)
        REFERENCES teams(organization_id, id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (length(name) BETWEEN 1 AND 120),
    CHECK (NOT protected_general OR parent_team_id IS NULL)
);

CREATE UNIQUE INDEX teams_one_general_per_organization_idx
    ON teams(organization_id) WHERE protected_general;
CREATE INDEX teams_parent_idx ON teams(organization_id, parent_team_id);

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
    created_by_account_id uuid NOT NULL REFERENCES accounts(id),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    FOREIGN KEY (organization_id, target_team_id)
        REFERENCES teams(organization_id, id) ON DELETE CASCADE,
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (octet_length(token_hash) >= 32),
    CHECK (
        (organization_role = 'admin' AND target_team_id IS NULL AND team_role IS NULL)
        OR
        (organization_role = 'member' AND target_team_id IS NOT NULL AND team_role IS NOT NULL)
    )
);

CREATE INDEX organization_invitations_active_idx
    ON organization_invitations(organization_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE organization_invitation_acceptances (
    invitation_id uuid NOT NULL REFERENCES organization_invitations(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (invitation_id, account_id)
);
