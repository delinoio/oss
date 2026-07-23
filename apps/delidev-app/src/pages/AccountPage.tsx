import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  AccountService,
  OrganizationRole,
  OrganizationService,
} from "@delinoio/delibase-connect";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { useAuthSession } from "../auth/AuthSession";
import { Dialog } from "../components/Dialog";
import {
  ErrorState,
  LoadingState,
  OfflineActionHint,
} from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";
import { useOnline } from "../hooks/useOnline";
import {
  createIdempotencyKey,
  formatEnumLabel,
} from "../utils/format";

const slugPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export function AccountPage() {
  useDocumentMetadata("Account", "Manage your DeliDev account.");
  const auth = useAuthSession();
  const navigate = useNavigate();
  const online = useOnline();
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [organizationName, setOrganizationName] = useState("");
  const [organizationSlug, setOrganizationSlug] = useState("");
  const [organizationError, setOrganizationError] = useState("");
  const account = useQuery(
    AccountService.method.getAccountState,
    {},
    {
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport: auth.transport,
    },
  );
  const impact = useQuery(
    AccountService.method.getAccountDeletionImpact,
    {},
    {
      enabled: deleteDialogOpen && online,
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport: auth.transport,
    },
  );
  const remove = useMutation(AccountService.method.deleteAccount, {
    transport: auth.transport,
  });
  const createOrganization = useMutation(
    OrganizationService.method.createOrganization,
    { transport: auth.transport },
  );

  const submitOrganization = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setOrganizationError("");
    const name = organizationName.trim();
    const slug = organizationSlug.trim().toLowerCase();
    if (!name) {
      setOrganizationError("Enter an organization name.");
      return;
    }
    if (!slugPattern.test(slug)) {
      setOrganizationError(
        "Use lowercase letters, numbers, and single hyphens for the slug.",
      );
      return;
    }
    createOrganization.mutate(
      {
        idempotency: createIdempotencyKey(),
        name,
        slug,
      },
      {
        onError: (error) => setOrganizationError(error.message),
        onSuccess: (data) => {
          void navigate(`/o/${data.organization?.slug ?? slug}/apps`);
        },
      },
    );
  };

  if (account.isPending) {
    return (
      <div className="page narrow">
        <LoadingState label="Loading account" />
      </div>
    );
  }
  if (account.isError) {
    return (
      <div className="page narrow">
        <ErrorState
          error={account.error}
          onRetry={() => void account.refetch()}
          title="Account unavailable"
        />
      </div>
    );
  }

  return (
    <div className="page narrow">
      <header className="page-heading">
        <span className="eyebrow">Your profile</span>
        <h1>Account</h1>
        <p>Manage your profile, organizations, and account lifecycle.</p>
      </header>
      <section className="content-card account-profile">
        <span className="avatar large" aria-hidden="true">
          {account.data.account?.displayName.slice(0, 1)}
        </span>
        <div>
          <h2>{account.data.account?.displayName || "DeliDev account"}</h2>
          <p>Your identity is secured by Logto.</p>
        </div>
      </section>
      <section className="content-card">
        <h2>Organizations</h2>
        {account.data.organizations.length ? (
          <ul className="organization-list">
            {account.data.organizations.map((organization) => (
              <li key={organization.organizationId?.value}>
                <div>
                  <strong>{organization.name}</strong>
                  <span>
                    {formatEnumLabel(
                      OrganizationRole[organization.role] ?? organization.role,
                    )}
                  </span>
                </div>
                <Link to={`/o/${organization.slug}/apps`}>
                  Open <span aria-hidden="true">→</span>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">No organizations found.</p>
        )}
      </section>
      <form
        className="form-card account-organization-form"
        onSubmit={submitOrganization}
      >
        <div>
          <span className="eyebrow">New workspace</span>
          <h2>Create another organization</h2>
          <p className="muted">
            Each organization starts with you as Owner and a protected General
            team.
          </p>
        </div>
        <label>
          Organization name
          <input
            autoComplete="organization"
            maxLength={120}
            onChange={(event) => setOrganizationName(event.target.value)}
            required
            value={organizationName}
          />
        </label>
        <label>
          Organization URL
          <span className="slug-input">
            <span aria-hidden="true">deli.dev/o/</span>
            <input
              aria-describedby="new-organization-slug-help"
              autoCapitalize="none"
              autoCorrect="off"
              maxLength={63}
              onChange={(event) => setOrganizationSlug(event.target.value)}
              pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
              required
              spellCheck={false}
              value={organizationSlug}
            />
          </span>
        </label>
        <small className="field-help" id="new-organization-slug-help">
          Use lowercase letters, numbers, and single hyphens.
        </small>
        {organizationError ? (
          <p className="inline-error" role="alert">
            {organizationError}
          </p>
        ) : null}
        <button
          className="button primary"
          disabled={!online || createOrganization.isPending}
          type="submit"
        >
          {createOrganization.isPending
            ? "Creating organization…"
            : "Create organization"}
        </button>
        {!online ? <OfflineActionHint /> : null}
      </form>
      <section className="content-card danger-zone">
        <div>
          <h2>Delete account</h2>
          <p>
            Account deletion is permanent and may be blocked if you are an
            organization’s last owner.
          </p>
        </div>
        <button
          className="button danger"
          disabled={!online}
          onClick={() => setDeleteDialogOpen(true)}
          type="button"
        >
          Review deletion
        </button>
        {!online ? <OfflineActionHint /> : null}
      </section>
      {deleteDialogOpen ? (
        <Dialog
          descriptionId="delete-description"
          onClose={() => setDeleteDialogOpen(false)}
          titleId="delete-title"
        >
          <h2 id="delete-title">Delete your DeliDev account?</h2>
          <p id="delete-description">
            We’ll remove operational profile and membership data, sign you out,
            and queue identity deletion.
          </p>
          {impact.isPending ? (
            <LoadingState label="Checking account ownership" />
          ) : null}
          {impact.isError ? (
            <ErrorState
              error={impact.error}
              onRetry={() => void impact.refetch()}
              title="Deletion check failed"
            />
          ) : null}
          {impact.data?.blockers.length ? (
            <div className="inline-error" role="alert">
              <strong>Deletion is blocked:</strong>
              <ul>
                {impact.data.blockers.map((blocker, index) => (
                  <li key={index}>
                    {blocker.organizationName
                      ? `Transfer ownership of ${blocker.organizationName} first.`
                      : "Transfer organization ownership first."}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {remove.error ? (
            <p className="inline-error" role="alert">
              {remove.error.message}
            </p>
          ) : null}
          <div className="dialog-actions">
            <button
              className="button secondary"
              onClick={() => setDeleteDialogOpen(false)}
              type="button"
            >
              Keep account
            </button>
            <button
              className="button danger"
              disabled={
                !impact.data?.canDelete || !online || remove.isPending
              }
              onClick={() =>
                remove.mutate(
                  {
                    confirm: true,
                    idempotency: createIdempotencyKey(),
                  },
                  { onSuccess: () => void auth.signOut() },
                )
              }
              type="button"
            >
              {remove.isPending ? "Deleting…" : "Delete account"}
            </button>
          </div>
        </Dialog>
      ) : null}
    </div>
  );
}
