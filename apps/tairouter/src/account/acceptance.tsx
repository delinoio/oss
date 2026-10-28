// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { pageHref } from "../routes";
import { useEffect, useId, useRef, useState } from "react";
import {
  createConnectQueryKey,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  AccountService,
  ErrorDetailSchema,
  ErrorReason,
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
  const [inputInvalid, setInputInvalid] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const pagingRef = useRef<HTMLButtonElement>(null);
  const restorePagingFocus = useRef(false);
  const inputId = useId();
  const helperId = useId();
  const errorId = useId();
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
  const manualEntry = !token && !invitationId;
  const sessionEmail = session.isSuccess ? (session.data?.email ?? "") : "";
  // Authorized detail is only presented for a valid session and a source that
  // has not lost authority; retained query data is otherwise dropped.
  const contextRevoked = [
    session.error,
    token ? detail.error : mine.error,
  ].some(revokesInvitationContext);
  const reviewContext = session.isSuccess && !contextRevoked;
  const invitation = !reviewContext
    ? undefined
    : token
      ? detail.data?.invitation
      : mine.invitations.find((value) => value.id === invitationId);
  const now = useNow();
  const deadline = timestampMs(invitation?.expiresAt);
  const expired = deadline !== undefined && deadline <= now;
  const pending = invitation?.state === LifecycleState.PENDING;
  const eligible =
    pending &&
    deadline !== undefined &&
    !expired &&
    session.isSuccess &&
    !detail.isError &&
    !mine.isError &&
    !contextRevoked;
  const loading =
    !manualEntry &&
    (session.isPending || (token ? detail.isPending : mine.isPending));
  const pagingMore = !token && mine.isFetchingNextPage;
  // Another account must never see detail loaded for the previous one.
  const lastEmail = useRef("");
  useEffect(() => {
    if (!sessionEmail) return;
    if (lastEmail.current && lastEmail.current !== sessionEmail) {
      void cache.resetQueries({
        queryKey: createConnectQueryKey({
          schema: InvitationService.method.getInvitation,
          cardinality: undefined,
        }),
      });
      void cache.resetQueries({
        queryKey: createConnectQueryKey({
          schema: InvitationService.method.listMyInvitations,
          cardinality: undefined,
        }),
      });
    }
    lastEmail.current = sessionEmail;
  }, [cache, sessionEmail]);
  // Keep keyboard focus on the paging control once its fetch settles.
  useEffect(() => {
    if (mine.isFetching || !restorePagingFocus.current) return;
    restorePagingFocus.current = false;
    pagingRef.current?.focus();
  }, [mine.isFetching]);
  let statusMessage = "";
  if (organization)
    statusMessage = translate("youJoined", { value0: organization.name });
  else if (loading) statusMessage = translate("loadingInvitation");
  else if (pagingMore) statusMessage = translate("checkingMoreInvitations");
  else if (invitation && pending && expired)
    statusMessage = invitationStates[LifecycleState.EXPIRED] ?? "";
  else if (invitation && !pending)
    statusMessage =
      invitationStates[invitation.state] ??
      translate("invitationStatusIsUnavailable");
  const notFoundSoFar =
    !token &&
    reviewContext &&
    mine.isSuccess &&
    !invitation &&
    mine.hasNextPage;
  const exhausted =
    !token &&
    reviewContext &&
    mine.isSuccess &&
    !invitation &&
    !mine.hasNextPage;
  const reviewable = Boolean(invitation) && pending && !expired;
  return (
    <AccountSurface
      title={
        organization
          ? translate("invitationAccepted")
          : translate("joinAnOrganization")
      }
      subtitle={translate("useTheAccountWithTheInvitedVerified")}
      className="invitation-surface"
      focusHeading={!manualEntry}
    >
      <div className="account-card invitation-card">
        {!organization && sessionEmail && (
          <p className="invitation-identity">
            {translate("signedInAs", { value0: sessionEmail })}
          </p>
        )}
        {/* Always mounted so state changes are announced politely once. */}
        <p role="status" className="invitation-status">
          {statusMessage}
        </p>
        {!organization && (
          <AccountError
            error={session.error}
            retry={() => {
              void session.refetch();
            }}
          />
        )}
        {organization ? (
          <p className="invitation-return">
            <a href={pageHref("overview", organization.id)}>
              {translate("openOrganization")}
            </a>
          </p>
        ) : manualEntry ? (
          <form
            noValidate
            onSubmit={(event) => {
              event.preventDefault();
              // Whitespace-only input is validation, never a lookup request.
              if (!input.trim()) {
                setInputInvalid(true);
                inputRef.current?.focus();
                return;
              }
              // Keep invitation credentials in memory; never persist pasted links in browser storage.
              try {
                const url = new URL(input, window.location.origin);
                setToken(url.searchParams.get("token") ?? input.trim());
              } catch {
                setToken(input.trim());
              }
            }}
          >
            <label htmlFor={inputId}>{translate("invitationLinkOrCode")}</label>
            <input
              id={inputId}
              ref={inputRef}
              autoFocus
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={input}
              aria-invalid={inputInvalid || undefined}
              aria-describedby={
                inputInvalid ? `${helperId} ${errorId}` : helperId
              }
              onChange={(event) => {
                setInput(event.target.value);
                setInputInvalid(false);
              }}
            />
            <p id={helperId} className="account-hint invitation-helper">
              {translate("pasteTheInvitationLinkOrCode")}
            </p>
            {inputInvalid && (
              <p id={errorId} role="alert" className="invitation-field-error">
                {translate("enterTheInvitationLinkOrCode")}
              </p>
            )}
            <button className="account-primary invitation-accept" type="submit">
              {translate("reviewInvitation")}
            </button>
          </form>
        ) : (
          <>
            {loading && (
              <div className="invitation-skeleton" aria-hidden="true">
                <span className="invitation-skeleton-line invitation-skeleton-title" />
                <span className="invitation-skeleton-line" />
                <span className="invitation-skeleton-line" />
                <span className="invitation-skeleton-line" />
              </div>
            )}
            <AccountError
              error={detail.error ?? mine.error}
              retry={() => {
                if (token) void detail.refetch();
                else if (mine.isFetchNextPageError) void mine.fetchNextPage();
                else void mine.refetch();
              }}
            />
            {invitation && (
              <>
                <header className="invitation-organization">
                  <h2>{invitation.organizationName}</h2>
                  {reviewable && (
                    <span className="invitation-badge">
                      {translate("oauthStatePending")}
                    </span>
                  )}
                </header>
                {reviewable && (
                  <p>{translate("reviewYourInvitationBeforeJoining")}</p>
                )}
                <dl className="account-details invitation-details">
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
                {reviewable && (
                  <p className="invitation-note">
                    {translate("joiningDoesNotCopyProviderCredentials")}
                  </p>
                )}
              </>
            )}
            {notFoundSoFar && (
              <p className="invitation-unresolved">
                {translate("thisInvitationHasNotBeenFound")}
              </p>
            )}
            {exhausted && (
              <p role="alert" className="invitation-unresolved">
                {translate("thisInvitationIsNoLongerPendingOr")}
              </p>
            )}
            <AccountError error={accept.error} />
            {notFoundSoFar && (
              <button
                ref={pagingRef}
                type="button"
                className="invitation-paging"
                disabled={mine.isFetching}
                onClick={() => {
                  restorePagingFocus.current = true;
                  void mine.fetchNextPage();
                }}
              >
                {translate("checkMoreInvitations")}
              </button>
            )}
            <button
              type="button"
              className="account-primary invitation-accept"
              disabled={!eligible || accept.isPending}
              onClick={() => {
                if (!eligible || accept.isPending) return;
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
            {reviewable && (
              <p className="account-hint invitation-helper">
                {translate("youJoinOnlyAfterYouAccept")}
              </p>
            )}
          </>
        )}
      </div>
    </AccountSurface>
  );
}

/**
 * Failures that mean the caller may no longer be authorized to see the
 * reviewed invitation. Retained cached details are dropped for these so a
 * stale payload is never presented as current authority.
 */
function revokesInvitationContext(error: unknown): boolean {
  if (!error) return false;
  const rpc = ConnectError.from(error);
  return (
    rpc.code === Code.Unauthenticated ||
    rpc.code === Code.PermissionDenied ||
    rpc.code === Code.NotFound ||
    rpc.findDetails(ErrorDetailSchema)[0]?.reason ===
      ErrorReason.INVITATION_EMAIL_MISMATCH
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
