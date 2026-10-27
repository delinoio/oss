// SPDX-License-Identifier: Apache-2.0
import { useContext, useEffect, useState } from "react";
import { useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { createClient, Code, ConnectError } from "@connectrpc/connect";
import {
  MembershipService,
  InvitationService,
  OrganizationService,
  OwnershipService,
  Role,
  LifecycleState,
  DeliveryState,
  type Invitation,
  type Member,
  type Organization,
} from "@aiprouter/client";
import { Card, PageHeader } from "../ui";
import { useDirtyForm } from "../api";
import {
  AdminAuthority,
  Confirm,
  Dialog,
  Empty,
  ErrorNotice,
  Loading,
  Pager,
  canRemove,
  date,
  label,
  milliseconds,
  useAction,
  useNow,
} from "./common";

export function Members() {
  const { organizationId, role } = useContext(AdminAuthority);
  const transport = useTransport();
  const [token, setToken] = useState("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Member>();
  const query = useQuery(MembershipService.method.listMembers, {
    organizationId,
    page: { pageSize: 100, pageToken: token },
  });
  const remove = useMutation(MembershipService.method.removeMember);
  const change = useMutation(MembershipService.method.setMemberRole);
  const action = useAction();
  async function checkTarget(member: Member) {
    const current = await createClient(
      MembershipService,
      transport,
    ).listMembers({
      organizationId,
      page: { pageSize: 100, pageToken: token },
    });
    const target = current.members.find((m) => m.userId === member.userId);
    const org = await createClient(
      OrganizationService,
      transport,
    ).getOrganization({ organizationId });
    if (
      !target ||
      !canRemove(org.organization?.role ?? Role.UNSPECIFIED, target.role)
    )
      throw new ConnectError("Permission changed", Code.PermissionDenied);
  }
  const members =
    query.data?.members.filter((m) =>
      `${m.displayName} ${m.email}`
        .toLowerCase()
        .includes(search.toLowerCase()),
    ) ?? [];
  return (
    <>
      <PageHeader
        title="Members"
        description="Manage membership. Personal credentials and request content stay private."
      />
      {action.feedback}
      <Card>
        <label>
          Search this page
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        {search && <button onClick={() => setSearch("")}>Clear filters</button>}
        {query.isPending ? (
          <Loading />
        ) : query.error ? (
          <ErrorNotice error={query.error} retry={() => void query.refetch()} />
        ) : (
          <>
            <div className="admin-records">
              {members.map((member) => (
                <article className="admin-record" key={member.userId}>
                  <div>
                    <h2>{member.displayName || member.email}</h2>
                    <p>{member.email}</p>
                    <span className="admin-badge">
                      {label(Role[member.role])}
                    </span>
                    <p>Joined {date(member.joinedAt)}</p>
                  </div>
                  <div className="admin-actions">
                    {role === Role.OWNER && member.role !== Role.OWNER && (
                      <button
                        disabled={action.pending}
                        onClick={() =>
                          void action.run(
                            async () => {
                              await checkTarget(member);
                              await change.mutateAsync({
                                organizationId,
                                userId: member.userId,
                                role:
                                  member.role === Role.ADMINISTRATOR
                                    ? Role.MEMBER
                                    : Role.ADMINISTRATOR,
                              });
                            },
                            { owner: true },
                          )
                        }
                      >
                        {member.role === Role.ADMINISTRATOR
                          ? "Make member"
                          : "Make administrator"}
                      </button>
                    )}
                    {canRemove(role, member.role) && (
                      <button
                        className="admin-danger-outline"
                        onClick={() => setSelected(member)}
                      >
                        Remove {member.displayName || member.email}
                      </button>
                    )}
                  </div>
                </article>
              ))}
            </div>
            {!members.length && (
              <Empty>
                {search
                  ? "No members match this page. Clear filters or check another page."
                  : "No members available."}
              </Empty>
            )}
            <Pager
              token={token}
              next={query.data?.page?.nextPageToken}
              onChange={setToken}
            />
          </>
        )}
      </Card>
      {selected && (
        <Confirm
          title="Remove member"
          name={selected.displayName || selected.email}
          pending={action.pending}
          onClose={() => setSelected(undefined)}
          action={() =>
            void action.run(
              async () => {
                await checkTarget(selected);
                await remove.mutateAsync({
                  organizationId,
                  userId: selected.userId,
                });
              },
              {
                done: () => setSelected(undefined),
                success: "Member removed.",
              },
            )
          }
        >
          <p>
            Remove {selected.displayName || selected.email}? Their organization
            keys stop working immediately. Personal providers, configuration and
            temporary state are deleted. Rejoining requires configuration again.
          </p>
          <p>
            Usage remains subject to retention. Already-dispatched responses may
            finish.
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </>
  );
}

export function invitationStatus(invitation: Invitation, now: number) {
  if (invitation.state === LifecycleState.ACCEPTED) return "Accepted";
  if (invitation.state === LifecycleState.CANCELED) return "Canceled";
  if (invitation.state === LifecycleState.SUPERSEDED) return "Superseded";
  if (
    invitation.state === LifecycleState.FAILED ||
    invitation.deliveryState === DeliveryState.FAILED
  )
    return "Delivery failed";
  if (
    invitation.state === LifecycleState.EXPIRED ||
    (milliseconds(invitation.expiresAt) ?? Infinity) <= now
  )
    return "Expired";
  if (invitation.deliveryState === DeliveryState.RETRY_PENDING)
    return "Retry scheduled";
  if (invitation.deliveryState === DeliveryState.SENT) return "Sent";
  if (invitation.deliveryState === DeliveryState.QUEUED)
    return "Pending delivery";
  return "Unknown status";
}
export function Invitations() {
  const { organizationId } = useContext(AdminAuthority);
  const [token, setToken] = useState("");
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [cancel, setCancel] = useState<Invitation>();
  const [cooldowns, setCooldowns] = useState<Record<string, number>>({});
  const now = useNow();
  const action = useAction();
  const dirty = useDirtyForm();
  useEffect(() => {
    dirty(open && !!email);
    return () => dirty(false);
  }, [dirty, open, email]);
  const query = useQuery(
    InvitationService.method.listInvitations,
    { organizationId, page: { pageSize: 100, pageToken: token } },
    { refetchInterval: 15000 },
  );
  const create = useMutation(InvitationService.method.createInvitation),
    resend = useMutation(InvitationService.method.resendInvitation),
    remove = useMutation(InvitationService.method.cancelInvitation);
  return (
    <>
      <PageHeader
        title="Invitations"
        description="Invite a member to this organization."
        action={
          <button
            className="admin-primary"
            onClick={() => {
              setEmail("");
              setOpen(true);
            }}
          >
            Invite member
          </button>
        }
      />
      {action.feedback}
      <p>
        Resends are at least one minute apart. The organization is limited to
        100 send attempts per hour. A manual resend invalidates the old link and
        starts a new seven-day invitation.
      </p>
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : (
        <Card>
          {!query.data?.invitations.length && (
            <Empty>No invitations yet. Invite your first member.</Empty>
          )}
          <div className="admin-records">
            {query.data?.invitations.map((invitation) => {
              const status = invitationStatus(invitation, now);
              const available = Math.max(
                milliseconds(invitation.resendAvailableAt) ?? 0,
                cooldowns[invitation.email] ?? 0,
              );
              const waiting = available > now;
              const terminal = [
                LifecycleState.ACCEPTED,
                LifecycleState.CANCELED,
                LifecycleState.SUPERSEDED,
              ].includes(invitation.state);
              return (
                <article className="admin-record" key={invitation.id}>
                  <div>
                    <h2>{invitation.email}</h2>
                    <p>Role: Member</p>
                    <p>Created: Not reported</p>
                    <span className="admin-badge">{status}</span>
                    <p>Expires {date(invitation.expiresAt)}</p>
                    {status === "Delivery failed" && (
                      <p className="admin-danger-text">
                        The link is invalid. Resend to create a new invitation.
                      </p>
                    )}
                    {invitation.nextAttemptAt && (
                      <p>
                        Next delivery attempt: {date(invitation.nextAttemptAt)}.
                        Delivery can be deferred by the hourly limit.
                      </p>
                    )}
                    <p>Send attempts: {invitation.deliveryAttempts}</p>
                    {!available && (
                      <p>
                        Resend timing is not reported. The server still enforces
                        the one-minute cooldown.
                      </p>
                    )}
                    {waiting && (
                      <p id={`cooldown-${invitation.id}`}>
                        Resend available at{" "}
                        {new Date(available).toLocaleTimeString()}. Wait{" "}
                        {Math.ceil((available - now) / 1000)} seconds.
                      </p>
                    )}
                  </div>
                  {!terminal && (
                    <div className="admin-actions">
                      <button
                        disabled={action.pending || waiting}
                        aria-describedby={
                          waiting ? `cooldown-${invitation.id}` : undefined
                        }
                        onClick={() =>
                          void action.run(
                            async () => {
                              await resend.mutateAsync({
                                organizationId,
                                invitationId: invitation.id,
                              });
                              setCooldowns((old) => ({
                                ...old,
                                [invitation.email]: Date.now() + 60000,
                              }));
                            },
                            {
                              success:
                                "New invitation created. The previous link is invalid.",
                            },
                          )
                        }
                      >
                        Resend
                      </button>
                      {invitation.state === LifecycleState.PENDING &&
                        status !== "Expired" && (
                          <button onClick={() => setCancel(invitation)}>
                            Cancel invitation
                          </button>
                        )}
                    </div>
                  )}
                </article>
              );
            })}
          </div>
          <Pager
            next={query.data?.page?.nextPageToken}
            token={token}
            onChange={setToken}
          />
        </Card>
      )}
      {open && (
        <Dialog title="Invite member" onClose={() => setOpen(false)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void action.run(
                async () => {
                  await create.mutateAsync({ organizationId, email });
                  setCooldowns((old) => ({
                    ...old,
                    [email]: Date.now() + 60000,
                  }));
                },
                {
                  done: () => {
                    setOpen(false);
                    setEmail("");
                  },
                  success: "Invitation created. Delivery is pending.",
                },
              );
            }}
          >
            <label>
              Email address
              <input
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </label>
            <p>
              The invited person joins as a member. They must sign in with this
              verified email address.
            </p>
            <ErrorNotice error={action.error} />
            <div className="admin-actions">
              <button type="button" data-cancel onClick={() => setOpen(false)}>
                Cancel
              </button>
              <button className="admin-primary" disabled={action.pending}>
                Send invitation
              </button>
            </div>
          </form>
        </Dialog>
      )}
      {cancel && (
        <Confirm
          title="Cancel invitation"
          name={cancel.email}
          pending={action.pending}
          onClose={() => setCancel(undefined)}
          action={() =>
            void action.run(
              () =>
                remove.mutateAsync({ organizationId, invitationId: cancel.id }),
              {
                done: () => setCancel(undefined),
                success: "Invitation canceled.",
              },
            )
          }
        >
          <p>The invitation link for {cancel.email} will stop working.</p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </>
  );
}

export function Settings({ organization }: { organization: Organization }) {
  const { organizationId, role } = useContext(AdminAuthority);
  const [name, setName] = useState(organization.name);
  const [deleting, setDeleting] = useState(false);
  const dirty = useDirtyForm();
  useEffect(() => {
    dirty(name !== organization.name);
    return () => dirty(false);
  }, [dirty, name, organization.name]);
  const action = useAction();
  const update = useMutation(OrganizationService.method.updateOrganization);
  const remove = useMutation(OrganizationService.method.deleteOrganization);
  return (
    <>
      <PageHeader
        title="Organization settings"
        description={`Manage ${organization.name}.`}
      />
      {action.feedback}
      <Card>
        <h2>Organization name</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void action.run(() => update.mutateAsync({ organizationId, name }));
          }}
        >
          <label>
            Name
            <input
              autoFocus
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <button
            className="admin-primary"
            disabled={
              action.pending || !name.trim() || name === organization.name
            }
          >
            Save name
          </button>
        </form>
      </Card>
      {role === Role.OWNER && (
        <>
          <Ownership />
          <Card>
            <h2>Delete organization</h2>
            <p>
              As the sole owner, transfer ownership or delete this organization
              before leaving.
            </p>
            <button
              className="admin-danger-outline"
              onClick={() => setDeleting(true)}
            >
              Delete organization
            </button>
          </Card>
        </>
      )}
      {deleting && role === Role.OWNER && (
        <Confirm
          title="Delete organization"
          name={organization.name}
          pending={action.pending}
          onClose={() => setDeleting(false)}
          action={() =>
            void action.run(() => remove.mutateAsync({ organizationId }), {
              owner: true,
              done: () => setDeleting(false),
              success: "Organization deleted.",
            })
          }
        >
          <p>
            This permanently deletes {organization.name}’s credentials,
            settings, temporary state, usage and organization audit records.
          </p>
          <p>
            New requests stop immediately. Responses already sent to a provider
            may finish. Existing backups expire under operator policy.
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </>
  );
}
function Ownership() {
  const { organizationId } = useContext(AdminAuthority);
  const [nominee, setNominee] = useState("");
  const [token, setToken] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [cancel, setCancel] = useState(false);
  const now = useNow();
  const action = useAction();
  const members = useQuery(MembershipService.method.listMembers, {
    organizationId,
    page: { pageSize: 100, pageToken: token },
  });
  const query = useQuery(
    OwnershipService.method.getOwnershipTransfer,
    { organizationId },
    { refetchInterval: 15000 },
  );
  const nominate = useMutation(OwnershipService.method.nominateOwner),
    remove = useMutation(OwnershipService.method.cancelOwnershipTransfer);
  const transfer = query.data?.transfer;
  const pending = transfer?.state === LifecycleState.PENDING;
  const expired =
    !!transfer &&
    (transfer.state === LifecycleState.EXPIRED ||
      (milliseconds(transfer.expiresAt) ?? Infinity) <= now);
  const person = members.data?.members.find(
    (m) => m.userId === transfer?.toUserId,
  );
  const selected = members.data?.members.find((m) => m.userId === nominee);
  return (
    <Card>
      <h2>Ownership transfer</h2>
      {action.feedback}
      {query.isPending ? (
        <Loading />
      ) : query.error &&
        ConnectError.from(query.error).code !== Code.NotFound ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : transfer ? (
        <div>
          <p>Nominee: {person?.displayName || transfer.toUserId}</p>
          <p>
            {expired
              ? "Transfer expired"
              : pending
                ? "Awaiting acceptance"
                : label(LifecycleState[transfer.state])}{" "}
            · Expires {date(transfer.expiresAt)}
          </p>
          {pending && !expired && (
            <button onClick={() => setCancel(true)}>Cancel transfer</button>
          )}
        </div>
      ) : (
        <p>No pending transfer.</p>
      )}
      <p>
        You remain owner until the nominee accepts. They become owner and you
        become an administrator. A nomination expires after seven days.
      </p>
      <ErrorNotice error={members.error} />
      <label>
        Nominate an existing member
        <select value={nominee} onChange={(e) => setNominee(e.target.value)}>
          <option value="">Choose a member</option>
          {members.data?.members
            .filter((m) => m.role !== Role.OWNER)
            .map((m) => (
              <option key={m.userId} value={m.userId}>
                {m.displayName || m.email}
              </option>
            ))}
        </select>
      </label>
      <Pager
        token={token}
        next={members.data?.page?.nextPageToken}
        onChange={(value) => {
          setToken(value);
          setNominee("");
        }}
      />
      <button
        disabled={
          !selected ||
          action.pending ||
          !!(
            query.error && ConnectError.from(query.error).code !== Code.NotFound
          )
        }
        onClick={() => setConfirm(true)}
      >
        {pending && !expired ? "Replace nomination" : "Nominate owner"}
      </button>
      {confirm && selected && (
        <Confirm
          title="Nominate owner"
          pending={action.pending}
          onClose={() => setConfirm(false)}
          action={() =>
            void action.run(
              () => nominate.mutateAsync({ organizationId, userId: nominee }),
              {
                owner: true,
                done: () => setConfirm(false),
                success: "Ownership nomination sent.",
              },
            )
          }
        >
          <p>
            Nominate {selected.displayName || selected.email}? Any previous
            request becomes invalid. Ownership changes only after acceptance.
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
      {cancel && transfer && (
        <Confirm
          title="Cancel transfer"
          pending={action.pending}
          onClose={() => setCancel(false)}
          action={() =>
            void action.run(
              () =>
                remove.mutateAsync({ organizationId, transferId: transfer.id }),
              {
                owner: true,
                done: () => setCancel(false),
                success: "Ownership transfer canceled.",
              },
            )
          }
        >
          <p>The nominee will no longer be able to accept this transfer.</p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </Card>
  );
}
