// SPDX-License-Identifier: Apache-2.0
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
} from "@aiprouter/client";
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
      title={organization ? "Invitation accepted" : "Join an organization"}
      subtitle="Use the account with the invited, verified email address."
    >
      {organization ? (
        <div className="account-card">
          <p role="status">You joined {organization.name}.</p>
          <a href={pageHref("overview", organization.id)}>Open organization</a>
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
                Invitation link or code
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
                Review invitation
              </button>
            </form>
          ) : (
            <>
              <p>Signed in as {session.data?.email ?? "…"}</p>
              {(token ? detail.isPending : mine.isPending) && (
                <p role="status">Loading invitation…</p>
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
                    <dt>Invited email</dt>
                    <dd>{invitation.email}</dd>
                    <dt>Role</dt>
                    <dd>
                      {invitation.role === Role.ADMINISTRATOR
                        ? "Administrator"
                        : invitation.role === Role.MEMBER
                          ? "Member"
                          : "Unknown"}
                    </dd>
                    <dt>Expires</dt>
                    <dd>{dateLabel(invitation.expiresAt)}</dd>
                  </dl>
                  {expired && invitation.state === LifecycleState.PENDING ? (
                    <p role="status">
                      This invitation has expired. Ask an administrator for a
                      new invitation.
                    </p>
                  ) : (
                    invitation.state !== LifecycleState.PENDING && (
                      <p role="status">
                        {invitationStates[invitation.state] ??
                          "Invitation status is unavailable."}
                      </p>
                    )
                  )}
                </>
              )}
              {!token && mine.isSuccess && !invitation && !mine.hasNextPage && (
                <p role="alert">
                  This invitation is no longer pending or is unavailable to your
                  account. Open the original email link for its current status.
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
                  Check more invitations
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
                {accept.isPending ? "Accepting…" : "Accept invitation"}
              </button>
            </>
          )}
        </div>
      )}
    </AccountSurface>
  );
}

const invitationStates: Partial<Record<LifecycleState, string>> = {
  [LifecycleState.ACCEPTED]: "This invitation has already been used.",
  [LifecycleState.CANCELED]: "This invitation was canceled.",
  [LifecycleState.EXPIRED]:
    "This invitation has expired. Ask an administrator for a new invitation.",
  [LifecycleState.SUPERSEDED]:
    "This invitation was replaced. Open the newest invitation email.",
  [LifecycleState.FAILED]:
    "Invitation delivery failed and this link is invalid. Ask an administrator to resend it.",
};

const transferStates: Partial<Record<LifecycleState, string>> = {
  [LifecycleState.ACCEPTED]:
    "This ownership transfer has already been accepted.",
  [LifecycleState.CANCELED]: "This ownership transfer was canceled.",
  [LifecycleState.EXPIRED]: "This ownership transfer has expired.",
  [LifecycleState.SUPERSEDED]:
    "This ownership transfer was replaced by a newer request.",
  [LifecycleState.FAILED]: "This ownership transfer is no longer available.",
};

export function OwnershipAcceptancePage({
  organizationId: suppliedId,
  onAccepted,
}: {
  organizationId?: string;
  onAccepted?: (organization: Organization) => void;
}) {
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
      title="Accept organization ownership"
      subtitle="Review the change before accepting."
    >
      <div className="account-card">
        {!organizationId && (
          <p role="alert">
            This link is missing its organization. Ask the current owner for a
            new link.
          </p>
        )}
        {organizationId &&
          (transfer.isPending ||
            organization.isPending ||
            session.isPending) && (
            <p role="status">Loading ownership transfer…</p>
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
            <p>
              You will become the owner. The previous owner will become an
              administrator. Ownership does not change until you accept.
            </p>
            <p>Expires: {dateLabel(nomination.expiresAt)}</p>
            {replaced ? (
              <p role="alert">
                This request was replaced by a newer ownership transfer. Ask the
                owner for the current link.
              </p>
            ) : expired ? (
              <p role="status">This ownership transfer has expired.</p>
            ) : nomination.state !== LifecycleState.PENDING ? (
              <p role="status">
                {transferStates[nomination.state] ??
                  "Ownership transfer status is unavailable."}
              </p>
            ) : (
              nomination.toUserId !== session.data?.userId && (
                <p role="alert">
                  Only the nominated member can accept this transfer. Sign in
                  with the nominated account.
                </p>
              )
            )}
            <AccountError error={accept.error} />
            {accept.isSuccess ? (
              <p role="status">Ownership accepted. You are now the owner.</p>
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
                {accept.isPending ? "Accepting…" : "Accept ownership"}
              </button>
            )}
            <p>
              <a href={pageHref("overview", organizationId)}>
                Back to organization
              </a>
            </p>
          </>
        )}
        {transfer.isSuccess && !nomination && (
          <p>No pending ownership transfer is available.</p>
        )}
      </div>
    </AccountSurface>
  );
}
