// SPDX-License-Identifier: Apache-2.0
import { pageHref } from "../routes";
import { useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  AccountService,
  Role,
  type GetSessionResponse,
} from "@aiprouter/client";
import {
  AccountDialog,
  AccountError,
  AccountSurface,
  LoadingAccount,
  signInHref,
  useOrganizations,
} from "./shared";

const signInErrors: Record<string, string> = {
  missing_email:
    "Your identity provider did not provide an email address. Add an email there, then try again.",
  unverified_email:
    "Verify your email address with your identity provider, then try again.",
  verified_email_required:
    "Your identity provider must provide a verified email address. Correct this at your identity provider, then try again.",
  login_denied: "Sign-in was canceled or denied. Try again when you are ready.",
  login_expired: "This sign-in attempt expired. Start a new sign-in.",
  authentication_unavailable:
    "Sign-in is temporarily unavailable. Try again later.",
};

export function SignInPage({
  error,
  returnTo,
}: {
  error?: string;
  returnTo?: string;
}) {
  const params = new URLSearchParams(window.location.search);
  const reason = error ?? params.get("error");
  const destination = returnTo ?? params.get("return_to") ?? "/overview";
  return (
    <AccountSurface
      title="Sign in to AIPRouter"
      subtitle="Use your organization's identity provider to continue."
    >
      <div className="account-card">
        {reason && (
          <p className="account-error" role="alert">
            {signInErrors[reason] ??
              "Sign-in could not be completed. Start a new sign-in."}
          </p>
        )}
        <p>Your identity provider must supply a verified email address.</p>
        <a
          className="account-action account-primary"
          href={signInHref(destination)}
        >
          Continue with your identity provider
        </a>
      </div>
    </AccountSurface>
  );
}

export function AccountPage({
  session: suppliedSession,
  onSessionChange,
}: {
  section?: string;
  session?: GetSessionResponse;
  onSessionChange?: () => void;
}) {
  const cache = useQueryClient();
  const session = useQuery(
    AccountService.method.getSession,
    {},
    { enabled: !suppliedSession, retry: false },
  );
  const organizations = useOrganizations();
  const logout = useMutation(AccountService.method.logout, { retry: false });
  const deletion = useMutation(AccountService.method.deleteAccount, {
    retry: false,
  });
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [ended, setEnded] = useState(false);
  const profile = suppliedSession ?? session.data;
  const owned = organizations.organizations.filter(
    (org) => org.role === Role.OWNER,
  );
  const complete = !organizations.hasNextPage;
  const canDelete =
    organizations.isSuccess &&
    !organizations.isFetching &&
    complete &&
    owned.length === 0;
  const endSession = async () => {
    setEnded(true);
    setConfirmDelete(false);
    await cache.cancelQueries();
    cache.clear();
    onSessionChange?.();
  };
  if (ended)
    return (
      <AccountSurface
        title={deletion.isSuccess ? "Account deleted" : "Signed out"}
      >
        <p role="status">
          {deletion.isSuccess
            ? "Your account has been deleted."
            : "Your session has ended."}
        </p>
        <a href={signInHref("/overview")}>Sign in</a>
      </AccountSurface>
    );
  return (
    <AccountSurface
      title="Account"
      subtitle="Your identity and account settings."
    >
      {!profile && session.isPending && <LoadingAccount />}
      <AccountError
        error={session.error}
        retry={() => {
          void session.refetch();
        }}
      />
      {profile && (
        <>
          <div className="account-card">
            <h2>Profile</h2>
            <dl className="account-details">
              <dt>Name</dt>
              <dd>{profile.displayName || "Not provided"}</dd>
              <dt>Email</dt>
              <dd>{profile.email}</dd>
            </dl>
            <p className="account-hint">
              Manage your name and verified email with your identity provider.
            </p>
            <AccountError error={logout.error} />
            <button
              type="button"
              disabled={logout.isPending || deletion.isPending}
              onClick={() =>
                logout.mutate(
                  {},
                  {
                    onSuccess: () => {
                      void endSession();
                    },
                  },
                )
              }
            >
              {logout.isPending ? "Signing out…" : "Sign out"}
            </button>
          </div>
          <div className="account-card">
            <h2>Delete account</h2>
            <p>First transfer or delete every organization you own.</p>
            {organizations.isPending && (
              <p role="status">Checking owned organizations…</p>
            )}
            <AccountError
              error={organizations.error}
              retry={() => {
                void organizations.refetch();
              }}
            />
            {owned.length > 0 && (
              <>
                <p>These organizations block account deletion:</p>
                <ul>
                  {owned.map((org) => (
                    <li key={org.id}>
                      <a href={pageHref("settings", org.id)}>{org.name}</a> —
                      transfer ownership or delete this organization.
                    </li>
                  ))}
                </ul>
              </>
            )}
            {!complete && (
              <>
                <p>
                  The organization list is incomplete. Account deletion is
                  blocked until all ownership can be checked.
                </p>
                <button
                  type="button"
                  disabled={organizations.isFetching}
                  onClick={() => {
                    void organizations.fetchNextPage();
                  }}
                >
                  Check more organizations
                </button>
              </>
            )}
            <p>
              Deletion removes your memberships and personal identity, and
              anonymizes surviving usage and audit records. Existing backups
              expire under the operator's policy.
            </p>
            <AccountError error={deletion.error} />
            <button
              className="account-danger"
              type="button"
              disabled={!canDelete || deletion.isPending || logout.isPending}
              onClick={() => setConfirmDelete(true)}
            >
              Delete my account
            </button>
          </div>
        </>
      )}
      {confirmDelete && (
        <AccountDialog
          title="Delete your account?"
          onClose={() => setConfirmDelete(false)}
        >
          <p>
            This removes all your memberships and personal identity. Surviving
            usage and audit records are anonymized. Existing backups expire
            under the operator's policy. This action cannot be undone.
          </p>
          <p>
            New requests stop. Responses already sent to a provider may finish.
          </p>
          <AccountError error={deletion.error} />
          <div className="account-actions">
            <button
              autoFocus
              type="button"
              onClick={() => setConfirmDelete(false)}
            >
              Cancel
            </button>
            <button
              type="button"
              className="account-danger"
              disabled={!canDelete || deletion.isPending}
              onClick={() =>
                deletion.mutate(
                  {},
                  {
                    onSuccess: () => {
                      void endSession();
                    },
                    onError: () => {
                      void organizations.refetch();
                    },
                  },
                )
              }
            >
              {deletion.isPending ? "Deleting…" : "Delete my account"}
            </button>
          </div>
        </AccountDialog>
      )}
    </AccountSurface>
  );
}
