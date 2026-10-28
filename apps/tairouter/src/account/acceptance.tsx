// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { pageHref } from "../routes";
import { useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  AccountService,
  InvitationService,
  LifecycleState,
  OrganizationService,
  OwnershipService,
  Role,
  type Organization,
} from "@tairouter/client";
import {
  AccountError,
  AccountSurface,
  dateLabel,
  timestampMs,
  useNow,
  useMyInvitations,
} from "./shared";

export function InvitationPage({
  token: suppliedToken,
  onAccepted,
}: {
  token?: string;
  onAccepted?: (organization: Organization) => void;
}) {
  useLocale();
  const [token, setToken] = useState(
    suppliedToken ??
      new URLSearchParams(window.location.search).get("token") ??
      "",
  );
  const [input, setInput] = useState("");
  const invitationId =
    new URLSearchParams(window.location.search).get("invitation_id") ?? "";
  const session = useQuery(
    AccountService.method.getSession,
    {},
    { retry: false },
  );
  const detail = useQuery(
    InvitationService.method.getInvitation,
    { token },
    { enabled: Boolean(token), retry: false },
  );
  const mine = useMyInvitations(Boolean(invitationId) && !token);
  const accept = useMutation(InvitationService.method.acceptInvitation, {
    retry: false,
  });
  const cache = useQueryClient();
  const organization = accept.data?.organization;
  const invitation = token
    ? detail.data?.invitation
    : mine.invitations.find((value) => value.id === invitationId);
  const now = useNow();
  const deadline = timestampMs(invitation?.expiresAt);
  const expired = deadline !== undefined && deadline <= now;
  const eligible =
    invitation?.state === LifecycleState.PENDING &&
    deadline !== undefined &&
    !expired &&
    session.isSuccess &&
    !detail.isError &&
    !mine.isError;
  return (
    <AccountSurface
      title={
        organization
          ? translate("invitationAccepted")
          : translate("joinAnOrganization")
      }
      subtitle={translate("useTheAccountWithTheInvitedVerified")}
    >
      {organization ? (
        <div className="account-card">
          <p role="status">
            {translate("youJoined", { value0: organization.name })}
          </p>
          <a href={pageHref("overview", organization.id)}>
            {translate("openOrganization")}
          </a>
        </div>
      ) : (
        <div className="account-card">
          <AccountError
            error={session.error}
            retry={() => {
              void session.refetch();
            }}
          />
          {!token && !invitationId ? (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                // Keep invitation credentials in memory; never persist pasted links in browser storage.
                try {
                  const url = new URL(input, window.location.origin);
                  setToken(url.searchParams.get("token") ?? input.trim());
                } catch {
                  setToken(input.trim());
                }
              }}
            >
              <label>
                {translate("invitationLinkOrCode")}
                <input
                  autoFocus
                  type="text"
                  autoComplete="off"
                  spellCheck={false}
                  value={input}
                  required
                  onChange={(event) => setInput(event.target.value)}
                />
              </label>
              <button className="account-primary" type="submit">
                {translate("reviewInvitation")}
              </button>
            </form>
          ) : (
            <>
              <p>
                {translate("signedInAs", {
                  value0: session.data?.email ?? "…",
                })}
              </p>
              {(token ? detail.isPending : mine.isPending) && (
                <p role="status">{translate("loadingInvitation")}</p>
              )}
              <AccountError
                error={detail.error ?? mine.error}
                retry={() => {
                  if (token) void detail.refetch();
                  else void mine.refetch();
                }}
              />
              {invitation && (
                <>
                  <h2>{invitation.organizationName}</h2>
                  <dl className="account-details">
                    <dt>{translate("invitedEmail")}</dt>
                    <dd>{invitation.email}</dd>
                    <dt>{translate("role")}</dt>
                    <dd>
                      {invitation.role === Role.ADMINISTRATOR
                        ? translate("administrator")
                        : invitation.role === Role.MEMBER
                          ? translate("member")
                          : translate("unknown")}
                    </dd>
                    <dt>{translate("expires_f6725f")}</dt>
                    <dd>{dateLabel(invitation.expiresAt)}</dd>
                  </dl>
                  {expired && invitation.state === LifecycleState.PENDING ? (
                    <p role="status">
                      {translate("thisInvitationHasExpiredAskAnAdministrator")}
                    </p>
                  ) : (
                    invitation.state !== LifecycleState.PENDING && (
                      <p role="status">
                        {invitationStates[invitation.state] ??
                          translate("invitationStatusIsUnavailable")}
                      </p>
                    )
                  )}
                </>
              )}
              {!token && mine.isSuccess && !invitation && !mine.hasNextPage && (
                <p role="alert">
                  {translate("thisInvitationIsNoLongerPendingOr")}
                </p>
              )}
              <AccountError error={accept.error} />
              {!token && mine.hasNextPage && !invitation && (
                <button
                  type="button"
                  disabled={mine.isFetching}
                  onClick={() => {
                    void mine.fetchNextPage();
                  }}
                >
                  {translate("checkMoreInvitations")}
                </button>
              )}
              <button
                type="button"
                className="account-primary"
                disabled={!eligible || accept.isPending}
                onClick={() => {
                  if (!eligible) return;
                  accept.mutate(token ? { token } : { invitationId }, {
                    onSuccess: (response) => {
                      if (response.organization) {
                        void cache.invalidateQueries();
                        onAccepted?.(response.organization);
                      }
                    },
                    onError: () => {
                      if (token) void detail.refetch();
                      else void mine.refetch();
                    },
                  });
                }}
              >
                {accept.isPending
                  ? translate("message_a168fd64e9")
                  : translate("acceptInvitation")}
              </button>
            </>
          )}
        </div>
      )}
    </AccountSurface>
  );
}

const invitationStates: Partial<Record<LifecycleState, string>> = {
  get [LifecycleState.ACCEPTED]() {
    return translate("thisInvitationHasAlreadyBeenUsed");
  },
  get [LifecycleState.CANCELED]() {
    return translate("thisInvitationWasCanceled");
  },
  get [LifecycleState.EXPIRED]() {
    return translate("thisInvitationHasExpiredAskAnAdministrator");
  },
  get [LifecycleState.SUPERSEDED]() {
    return translate("thisInvitationWasReplacedOpenTheNewest");
  },
  get [LifecycleState.FAILED]() {
    return translate("invitationDeliveryFailedAndThisLinkIs");
  },
};

const transferStates: Partial<Record<LifecycleState, string>> = {
  get [LifecycleState.ACCEPTED]() {
    return translate("thisOwnershipTransferHasAlreadyBeenAccepted");
  },
  get [LifecycleState.CANCELED]() {
    return translate("thisOwnershipTransferWasCanceled");
  },
  get [LifecycleState.EXPIRED]() {
    return translate("thisOwnershipTransferHasExpired");
  },
  get [LifecycleState.SUPERSEDED]() {
    return translate("thisOwnershipTransferWasReplacedByA");
  },
  get [LifecycleState.FAILED]() {
    return translate("thisOwnershipTransferIsNoLongerAvailable");
  },
};

export function OwnershipAcceptancePage({
  organizationId: suppliedId,
  onAccepted,
}: {
  organizationId?: string;
  onAccepted?: (organization: Organization) => void;
}) {
  useLocale();
  const organizationId =
    suppliedId ??
    new URLSearchParams(window.location.search).get("organization_id") ??
    "";
  const requestedTransfer = new URLSearchParams(window.location.search).get(
    "transfer_id",
  );
  const session = useQuery(
    AccountService.method.getSession,
    {},
    { retry: false },
  );
  const organization = useQuery(
    OrganizationService.method.getOrganization,
    { organizationId },
    { enabled: Boolean(organizationId), retry: false },
  );
  const transfer = useQuery(
    OwnershipService.method.getOwnershipTransfer,
    { organizationId },
    { enabled: Boolean(organizationId), retry: false, refetchInterval: 15000 },
  );
  const accept = useMutation(OwnershipService.method.acceptOwnership, {
    retry: false,
  });
  const cache = useQueryClient();
  const now = useNow();
  const nomination = transfer.data?.transfer;
  const deadline = timestampMs(nomination?.expiresAt);
  const replaced =
    requestedTransfer && nomination && requestedTransfer !== nomination.id;
  const expired = deadline !== undefined && deadline <= now;
  const eligible =
    nomination?.state === LifecycleState.PENDING &&
    nomination.toUserId === session.data?.userId &&
    deadline !== undefined &&
    !expired &&
    !replaced &&
    !transfer.isError &&
    !organization.isError &&
    !session.isError;
  return (
    <AccountSurface
      title={translate("acceptOrganizationOwnership")}
      subtitle={translate("reviewTheChangeBeforeAccepting")}
    >
      <div className="account-card">
        {!organizationId && (
          <p role="alert">{translate("thisLinkIsMissingItsOrganizationAsk")}</p>
        )}
        {organizationId &&
          (transfer.isPending ||
            organization.isPending ||
            session.isPending) && (
            <p role="status">{translate("loadingOwnershipTransfer")}</p>
          )}
        <AccountError
          error={session.error ?? organization.error ?? transfer.error}
          retry={() => {
            void session.refetch();
            void organization.refetch();
            void transfer.refetch();
          }}
        />
        {nomination && organization.data?.organization && (
          <>
            <h2>{organization.data.organization.name}</h2>
            <p>{translate("youWillBecomeTheOwnerThePrevious")}</p>
            <p>
              {translate("expires", {
                value0: dateLabel(nomination.expiresAt),
              })}
            </p>
            {replaced ? (
              <p role="alert">{translate("thisRequestWasReplacedByANewer")}</p>
            ) : expired ? (
              <p role="status">
                {translate("thisOwnershipTransferHasExpired")}
              </p>
            ) : nomination.state !== LifecycleState.PENDING ? (
              <p role="status">
                {transferStates[nomination.state] ??
                  translate("ownershipTransferStatusIsUnavailable")}
              </p>
            ) : (
              nomination.toUserId !== session.data?.userId && (
                <p role="alert">
                  {translate("onlyTheNominatedMemberCanAcceptThis")}
                </p>
              )
            )}
            <AccountError error={accept.error} />
            {accept.isSuccess ? (
              <p role="status">
                {translate("ownershipAcceptedYouAreNowTheOwner")}
              </p>
            ) : (
              <button
                type="button"
                className="account-primary"
                disabled={!eligible || accept.isPending}
                onClick={() => {
                  if (!eligible || !nomination) return;
                  accept.mutate(
                    { organizationId, transferId: nomination.id },
                    {
                      onSuccess: (response) => {
                        void cache.invalidateQueries();
                        if (response.organization)
                          onAccepted?.(response.organization);
                      },
                      onError: () => {
                        void transfer.refetch();
                      },
                    },
                  );
                }}
              >
                {accept.isPending
                  ? translate("message_a168fd64e9")
                  : translate("acceptOwnership")}
              </button>
            )}
            <p>
              <a href={pageHref("overview", organizationId)}>
                {translate("backToOrganization")}
              </a>
            </p>
          </>
        )}
        {transfer.isSuccess && !nomination && (
          <p>{translate("noPendingOwnershipTransferIsAvailable")}</p>
        )}
      </div>
    </AccountSurface>
  );
}
