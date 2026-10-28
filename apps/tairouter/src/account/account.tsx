// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { pageHref } from "../routes";
import { useId, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  AccountService,
  Role,
  type GetSessionResponse,
  type Organization,
} from "@tairouter/client";
import {
  AccountDialog,
  AccountDialogClosePlacement,
  AccountError,
  AccountSurface,
  LoadingAccount,
  signInHref,
  useOrganizations,
} from "./shared";

const signInErrors: Record<string, string> = {
  get missing_email() {
    return translate("yourIdentityProviderDidNotProvideAn");
  },
  get unverified_email() {
    return translate("verifyYourEmailAddressWithYourIdentity");
  },
  get verified_email_required() {
    return translate("yourIdentityProviderMustProvideAVerified");
  },
  get login_denied() {
    return translate("message_d1b41c73b8");
  },
  get login_expired() {
    return translate("thisSignInAttemptExpiredStartA");
  },
  get authentication_unavailable() {
    return translate("message_40ec40dda5");
  },
};

export function SignInPage({
  error,
  returnTo,
}: {
  error?: string;
  returnTo?: string;
}) {
  useLocale();
  const params = new URLSearchParams(window.location.search);
  const reason = error ?? params.get("error");
  const destination = returnTo ?? params.get("return_to") ?? "/overview";
  return (
    <div className="sign-in">
      <p className="sign-in-wordmark">TaiRouter</p>
      <AccountSurface
        className="sign-in-card"
        focusHeading
        title={translate("signInToTairouter")}
        subtitle={translate("useYourOrganizationSIdentityProviderTo")}
      >
        {reason && (
          <p className="account-error sign-in-alert" role="alert">
            <svg
              className="sign-in-alert-icon"
              aria-hidden="true"
              focusable="false"
              viewBox="0 0 20 20"
              width="20"
              height="20"
            >
              <path
                fill="currentColor"
                d="M10 1.5 19 17.5H1L10 1.5Zm-1 6v5h2v-5H9Zm0 6.5v2h2v-2H9Z"
              />
            </svg>
            <span>
              {signInErrors[reason] ?? translate("message_3590afc42f")}
            </span>
          </p>
        )}
        <p className="sign-in-guidance">
          {translate("yourIdentityProviderMustSupplyAVerified")}
        </p>
        <a
          className="account-action account-primary sign-in-action"
          href={signInHref(destination)}
        >
          {translate("continueWithYourIdentityProvider")}
        </a>
      </AccountSurface>
    </div>
  );
}

function OwnedOrganizations({
  owned,
  headingId,
}: {
  owned: Organization[];
  headingId: string;
}) {
  useLocale();
  return (
    <ul className="account-owner-list" aria-labelledby={headingId}>
      {owned.map((org) => (
        <li key={org.id} className="account-owner">
          <span className="account-owner-name">{org.name}</span>
          <span className="account-owner-role">{translate("owner")}</span>
          <a
            className="account-owner-link"
            href={pageHref("settings", org.id)}
            aria-label={translate("openOrganizationSettingsFor", {
              value0: org.name,
            })}
          >
            {translate("openOrganizationSettings")}
          </a>
          <span className="account-owner-help">
            {translate("transferOwnershipOrDeleteThisOrganization")}
          </span>
        </li>
      ))}
    </ul>
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
  useLocale();
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
  const profileHeadingId = useId();
  const deleteHeadingId = useId();
  const ownershipHeadingId = useId();
  const blockedHelpId = useId();
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
  // The ownership check is only conclusive after every page was fetched
  // successfully; a failed or partial response never produces the clear state.
  const checkFailed = organizations.isError;
  const checkIncomplete = !organizations.isPending && !complete;
  const checkClear = canDelete;
  const mutationPending = logout.isPending || deletion.isPending;
  const deletionInFlightAfterDismiss = deletion.isPending && !confirmDelete;
  const submitDeletion = () => {
    // Guard with the current eligibility so a stale click never sends a
    // request, and never start a second request while one is pending.
    if (!canDelete || mutationPending) return;
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
    );
  };
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
        focusHeading
        title={
          deletion.isSuccess
            ? translate("accountDeleted")
            : translate("signedOut")
        }
      >
        <p role="status">
          {deletion.isSuccess
            ? translate("yourAccountHasBeenDeleted")
            : translate("yourSessionHasEnded")}
        </p>
        <a href={signInHref("/overview")}>{translate("signIn")}</a>
      </AccountSurface>
    );
  return (
    <AccountSurface
      className="account-page"
      title={translate("account")}
      subtitle={translate("yourIdentityAndAccountSettings")}
    >
      {!profile && session.isPending && (
        <>
          <div className="account-skeleton" aria-hidden="true">
            <span className="account-skeleton-line account-skeleton-title" />
            <span className="account-skeleton-line" />
            <span className="account-skeleton-line" />
          </div>
          <LoadingAccount />
        </>
      )}
      <AccountError
        error={session.error}
        retry={() => {
          void session.refetch();
        }}
      />
      {profile && (
        <>
          <section
            className="account-card account-profile"
            aria-labelledby={profileHeadingId}
          >
            <h2 id={profileHeadingId}>{translate("profile")}</h2>
            <dl className="account-details">
              <dt>{translate("name")}</dt>
              <dd>{profile.displayName || translate("notProvided")}</dd>
              <dt>{translate("email")}</dt>
              <dd>{profile.email}</dd>
            </dl>
            <p className="account-hint">
              {translate("manageYourNameAndVerifiedEmailWith")}
            </p>
            <button
              type="button"
              className="account-signout"
              disabled={mutationPending}
              onClick={() => {
                if (mutationPending) return;
                logout.mutate(
                  {},
                  {
                    onSuccess: () => {
                      void endSession();
                    },
                  },
                );
              }}
            >
              {logout.isPending
                ? translate("signingOut")
                : translate("signOut")}
            </button>
            <AccountError error={logout.error} />
          </section>
          <section
            className="account-card account-delete"
            aria-labelledby={deleteHeadingId}
          >
            <h2 id={deleteHeadingId}>{translate("deleteAccount")}</h2>
            <p>{translate("firstTransferOrDeleteEveryOrganizationYou")}</p>
            {/* Always mounted so check progress and the clear result are
                announced politely and only once per change. */}
            <div role="status" className="account-ownership-status">
              {organizations.isPending && (
                <>
                  <div className="account-skeleton" aria-hidden="true">
                    <span className="account-skeleton-line" />
                    <span className="account-skeleton-line account-skeleton-short" />
                  </div>
                  <p>{translate("checkingOwnedOrganizations")}</p>
                </>
              )}
              {!organizations.isPending &&
                organizations.isFetching &&
                !organizations.isFetchingNextPage &&
                !checkFailed && (
                  <p>{translate("checkingOwnedOrganizations")}</p>
                )}
              {checkClear && (
                <div className="account-ownership-panel">
                  <h3>{translate("ownershipCheckComplete")}</h3>
                  <p>{translate("youDoNotOwnAnyOrganizations")}</p>
                </div>
              )}
            </div>
            <AccountError
              error={organizations.error}
              retry={() => {
                if (organizations.isFetching) return;
                if (organizations.isFetchNextPageError)
                  void organizations.fetchNextPage();
                else void organizations.refetch();
              }}
            />
            {(checkIncomplete || checkFailed) && (
              <div className="account-ownership-panel">
                <h3>{translate("ownershipCheckIncomplete")}</h3>
                <p>{translate("accountDeletionStaysBlockedUntilAll")}</p>
              </div>
            )}
            {owned.length > 0 && (
              <div className="account-owner-section">
                {complete && !checkFailed ? (
                  <>
                    <h3 id={ownershipHeadingId}>
                      {translate("ownershipBlocksDeletion")}
                    </h3>
                    <p>
                      {translate("transferOrDeleteTheseOrganizationsBefore")}
                    </p>
                  </>
                ) : (
                  <h3 id={ownershipHeadingId}>
                    {translate("organizationsThatBlockDeletion")}
                  </h3>
                )}
                <OwnedOrganizations
                  owned={owned}
                  headingId={ownershipHeadingId}
                />
              </div>
            )}
            {checkIncomplete && !checkFailed && (
              <button
                type="button"
                className="account-check-more"
                disabled={organizations.isFetching}
                onClick={() => {
                  if (organizations.isFetching) return;
                  void organizations.fetchNextPage();
                }}
              >
                {organizations.isFetchingNextPage
                  ? translate("checkingMoreOrganizations")
                  : translate("checkMoreOrganizations")}
              </button>
            )}
            <div className="account-consequences">
              <h3>{translate("whatDeletionChanges")}</h3>
              <ul>
                <li>
                  {translate("removesYourMembershipsAndPersonalIdentity")}
                </li>
                <li>{translate("anonymizesSurvivingUsageAndAuditRecords")}</li>
                <li>{translate("existingBackupsExpireUnderTheOperator")}</li>
              </ul>
            </div>
            {/* Always mounted so the in-flight notice is announced politely once
                after the dialog is dismissed; the open dialog owns its own copy. */}
            <div role="status" className="account-deletion-status">
              {deletionInFlightAfterDismiss && (
                <p>{translate("deletingYourAccountClosingTheDialog")}</p>
              )}
            </div>
            {!confirmDelete && <AccountError error={deletion.error} />}
            <button
              className="account-danger"
              type="button"
              disabled={!canDelete || mutationPending}
              aria-describedby={canDelete ? undefined : blockedHelpId}
              onClick={() => setConfirmDelete(true)}
            >
              {translate("deleteMyAccount")}
            </button>
            {!canDelete && (
              <p id={blockedHelpId} className="account-hint">
                {translate("resolveOwnershipAndCompleteTheCheck")}
              </p>
            )}
          </section>
        </>
      )}
      {confirmDelete && (
        <AccountDialog
          title={translate("deleteYourAccount")}
          className="account-dialog-review"
          closePlacement={AccountDialogClosePlacement.Header}
          onClose={() => setConfirmDelete(false)}
        >
          <p className="account-dialog-lead">
            {translate("thisActionCannotBeUndone")}
          </p>
          <h3>{translate("whatWillHappen")}</h3>
          <ul className="account-dialog-list">
            <li>{translate("removesAllYourMembershipsAndPersonal")}</li>
            <li>{translate("survivingUsageAndAuditRecordsAre")}</li>
            <li>{translate("existingBackupsExpireUnderTheOperator")}</li>
          </ul>
          <p className="account-dialog-notice">
            {translate("newRequestsStopResponsesAlreadySentTo")}
          </p>
          <div role="status" className="account-deletion-status">
            {deletion.isPending && (
              <p>{translate("deletingYourAccountClosingTheDialog")}</p>
            )}
          </div>
          <AccountError error={deletion.error} />
          <div className="account-actions account-dialog-actions">
            <button
              data-dialog-initial-focus
              type="button"
              onClick={() => setConfirmDelete(false)}
            >
              {translate("cancel")}
            </button>
            <button
              type="button"
              className="account-danger"
              disabled={!canDelete || mutationPending}
              onClick={submitDeletion}
            >
              {deletion.isPending
                ? translate("message_43b5894c3e")
                : translate("deleteMyAccount")}
            </button>
          </div>
        </AccountDialog>
      )}
    </AccountSurface>
  );
}
