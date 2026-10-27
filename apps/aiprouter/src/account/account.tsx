// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
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
    <AccountSurface
      title={translate("signInToAiprouter")}
      subtitle={translate("useYourOrganizationSIdentityProviderTo")}
    >
      <div className="account-card">
        {reason && (
          <p className="account-error" role="alert">
            {signInErrors[reason] ?? translate("message_3590afc42f")}
          </p>
        )}
        <p>{translate("yourIdentityProviderMustSupplyAVerified")}</p>
        <a
          className="account-action account-primary"
          href={signInHref(destination)}
        >
          {translate("continueWithYourIdentityProvider")}
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
      title={translate("account")}
      subtitle={translate("yourIdentityAndAccountSettings")}
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
            <h2>{translate("profile")}</h2>
            <dl className="account-details">
              <dt>{translate("name")}</dt>
              <dd>{profile.displayName || translate("notProvided")}</dd>
              <dt>{translate("email")}</dt>
              <dd>{profile.email}</dd>
            </dl>
            <p className="account-hint">
              {translate("manageYourNameAndVerifiedEmailWith")}
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
              {logout.isPending
                ? translate("signingOut")
                : translate("signOut")}
            </button>
          </div>
          <div className="account-card">
            <h2>{translate("deleteAccount")}</h2>
            <p>{translate("firstTransferOrDeleteEveryOrganizationYou")}</p>
            {organizations.isPending && (
              <p role="status">{translate("checkingOwnedOrganizations")}</p>
            )}
            <AccountError
              error={organizations.error}
              retry={() => {
                void organizations.refetch();
              }}
            />
            {owned.length > 0 && (
              <>
                <p>{translate("theseOrganizationsBlockAccountDeletion")}</p>
                <ul>
                  {owned.map((org) => (
                    <li key={org.id}>
                      <a href={pageHref("settings", org.id)}>{org.name}</a>{" "}
                      {translate("transferOwnershipOrDeleteThisOrganization")}
                    </li>
                  ))}
                </ul>
              </>
            )}
            {!complete && (
              <>
                <p>
                  {translate("theOrganizationListIsIncompleteAccountDeletion")}
                </p>
                <button
                  type="button"
                  disabled={organizations.isFetching}
                  onClick={() => {
                    void organizations.fetchNextPage();
                  }}
                >
                  {translate("checkMoreOrganizations")}
                </button>
              </>
            )}
            <p>
              {translate("deletionRemovesYourMembershipsAndPersonalIdentity")}
            </p>
            <AccountError error={deletion.error} />
            <button
              className="account-danger"
              type="button"
              disabled={!canDelete || deletion.isPending || logout.isPending}
              onClick={() => setConfirmDelete(true)}
            >
              {translate("deleteMyAccount")}
            </button>
          </div>
        </>
      )}
      {confirmDelete && (
        <AccountDialog
          title={translate("deleteYourAccount")}
          onClose={() => setConfirmDelete(false)}
        >
          <p>{translate("thisRemovesAllYourMembershipsAndPersonal")}</p>
          <p>{translate("newRequestsStopResponsesAlreadySentTo")}</p>
          <AccountError error={deletion.error} />
          <div className="account-actions">
            <button
              autoFocus
              type="button"
              onClick={() => setConfirmDelete(false)}
            >
              {translate("cancel")}
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
