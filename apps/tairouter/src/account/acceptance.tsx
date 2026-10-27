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
import { Code, ConnectError } from "@connectrpc/connect";
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

/**
 * Query failures that mean the caller may no longer be authorized to see the
 * reviewed transfer. Retained cached details are dropped for these codes so a
 * stale payload is never presented as current authority.
 */
function revokesReviewContext(error: unknown): boolean {
  if (!error) return false;
  const code = ConnectError.from(error).code;
  return (
    code === Code.Unauthenticated ||
    code === Code.PermissionDenied ||
    code === Code.NotFound
  );
}

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
  // A confirmed acceptance is tracked by the mutation alone, so later empty or
  // NotFound transfer polls can neither hide it nor re-enable submission.
  const accepted = accept.isSuccess;
  const contextRevoked = [session, organization, transfer].some(
    (query) => query.isError && revokesReviewContext(query.error),
  );
  const nomination = contextRevoked ? undefined : transfer.data?.transfer;
  const reviewedOrganization = contextRevoked
    ? undefined
    : organization.data?.organization;
  const deadline = timestampMs(nomination?.expiresAt);
  const replaced =
    requestedTransfer && nomination && requestedTransfer !== nomination.id;
  const pendingState = nomination?.state === LifecycleState.PENDING;
  const expired = deadline !== undefined && deadline <= now;
  const locallyExpired = pendingState && expired;
  const wrongNominee =
    pendingState && nomination.toUserId !== session.data?.userId;
  const eligible =
    !accepted &&
    pendingState &&
    nomination.toUserId === session.data?.userId &&
    deadline !== undefined &&
    !expired &&
    !replaced &&
    !transfer.isError &&
    !organization.isError &&
    !session.isError;
  const loading =
    !accepted &&
    Boolean(organizationId) &&
    (transfer.isPending || organization.isPending || session.isPending);
  // The role outcomes only describe a transfer that can still be reviewed.
  const reviewable = pendingState && !replaced && !locallyExpired;
  const showDetails =
    !accepted && !loading && Boolean(nomination && reviewedOrganization);
  let statusMessage = "";
  if (accepted) statusMessage = translate("ownershipAcceptedYouAreNowTheOwner");
  else if (loading) statusMessage = translate("loadingOwnershipTransfer");
  else if (showDetails && nomination && !replaced) {
    if (locallyExpired)
      statusMessage = translate("thisOwnershipTransferHasExpired");
    else if (!pendingState)
      statusMessage =
        transferStates[nomination.state] ??
        translate("ownershipTransferStatusIsUnavailable");
  } else if (
    !nomination &&
    transfer.isSuccess &&
    !transfer.isError &&
    !contextRevoked
  )
    statusMessage = translate("noPendingOwnershipTransferIsAvailable");
  const organizationName =
    (accepted ? accept.data?.organization?.name : undefined) ??
    reviewedOrganization?.name;
  const showReturn = accepted || Boolean(reviewedOrganization);
  return (
    <AccountSurface
      title={translate("acceptOrganizationOwnership")}
      subtitle={translate("reviewTheChangeBeforeAccepting")}
      className="ownership-surface"
      focusHeading
    >
      <div className="ownership-card">
        {!organizationId && (
          <p role="alert" className="ownership-alert">
            {translate("thisLinkIsMissingItsOrganizationAsk")}
          </p>
        )}
        {/* Always mounted so state changes, not polls, are announced once. */}
        <p role="status" className="ownership-status">
          {statusMessage}
        </p>
        {loading && (
          <div className="ownership-skeleton" aria-hidden="true">
            <span className="ownership-skeleton-line ownership-skeleton-short" />
            <span className="ownership-skeleton-line ownership-skeleton-title" />
            <span className="ownership-skeleton-line" />
            <span className="ownership-skeleton-line" />
          </div>
        )}
        {!accepted && (
          <AccountError
            error={session.error ?? organization.error ?? transfer.error}
            retry={() => {
              void session.refetch();
              void organization.refetch();
              void transfer.refetch();
            }}
          />
        )}
        {(showDetails || accepted) && organizationName !== undefined && (
          <header className="ownership-organization">
            <p className="ownership-label">{translate("organization")}</p>
            <h2>{organizationName}</h2>
            {eligible && (
              <p className="ownership-badge">
                {translate("pendingAcceptance")}
              </p>
            )}
          </header>
        )}
        {showDetails && nomination && (
          <>
            {replaced && (
              <p role="alert" className="ownership-alert">
                {translate("thisRequestWasReplacedByANewer")}
              </p>
            )}
            {reviewable && (
              <section className="ownership-section">
                <h3>{translate("whatWillChange")}</h3>
                <dl className="ownership-facts">
                  <div>
                    <dt>{translate("you")}</dt>
                    <dd>{translate("becomeOwner")}</dd>
                  </div>
                  <div>
                    <dt>{translate("previousOwner")}</dt>
                    <dd>{translate("becomeAdministrator")}</dd>
                  </div>
                </dl>
              </section>
            )}
            {!replaced && (
              <section className="ownership-section">
                <dl className="ownership-facts">
                  <div>
                    <dt>{translate("expires_f6725f")}</dt>
                    <dd>{dateLabel(nomination.expiresAt)}</dd>
                  </div>
                </dl>
                <p className="account-hint">
                  {translate("transferRequestsAreValidFor7")}
                </p>
              </section>
            )}
            {wrongNominee && !replaced && (
              <p role="alert" className="ownership-alert">
                {translate("onlyTheNominatedMemberCanAcceptThis")}
              </p>
            )}
            {reviewable && (
              <p className="ownership-note">
                {translate("ownershipDoesNotChangeUntilYou")}
              </p>
            )}
          </>
        )}
        <AccountError error={accept.error} />
        {showDetails && nomination && pendingState && (
          <button
            type="button"
            className="account-primary ownership-accept"
            disabled={!eligible || accept.isPending}
            onClick={() => {
              if (!eligible || !nomination || accept.isPending || accepted)
                return;
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
        {showReturn && (
          <p className="ownership-return">
            <a href={pageHref("overview", organizationId)}>
              {translate("backToOrganization")}
            </a>
          </p>
        )}
        {showDetails && reviewable && (
          <p className="ownership-quiet">
            {translate("leavingThisPageDoesNotCancel")}
          </p>
        )}
      </div>
    </AccountSurface>
  );
}
