// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, getLocale } from "../i18n";
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
  useLocale();
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
      throw new ConnectError(
        translate("permissionChanged"),
        Code.PermissionDenied,
      );
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
        title={translate("members")}
        description={translate(
          "manageMembershipPersonalCredentialsAndRequestContent",
        )}
      />
      {action.feedback}
      <Card>
        <label>
          {translate("searchThisPage")}
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        {search && (
          <button onClick={() => setSearch("")}>
            {translate("clearFilters")}
          </button>
        )}
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
                    <p>
                      {translate("joined", { value0: date(member.joinedAt) })}
                    </p>
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
                          ? translate("makeMember")
                          : translate("makeAdministrator")}
                      </button>
                    )}
                    {canRemove(role, member.role) && (
                      <button
                        className="admin-danger-outline"
                        onClick={() => setSelected(member)}
                      >
                        {translate("remove_3028bf", {
                          value0: member.displayName || member.email,
                        })}
                      </button>
                    )}
                  </div>
                </article>
              ))}
            </div>
            {!members.length && (
              <Empty>
                {search
                  ? translate("noMembersMatchThisPageClearFilters")
                  : translate("noMembersAvailable")}
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
          title={translate("removeMember")}
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
                success: translate("memberRemoved"),
              },
            )
          }
        >
          <p>
            {translate("removeTheirOrganizationKeysStopWorkingImmediately", {
              value0: selected.displayName || selected.email,
            })}
          </p>
          <p>{translate("usageRemainsSubjectToRetentionAlreadyDispatched")}</p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </>
  );
}

export function invitationStatus(invitation: Invitation, now: number) {
  if (invitation.state === LifecycleState.ACCEPTED)
    return translate("accepted");
  if (invitation.state === LifecycleState.CANCELED)
    return translate("canceled");
  if (invitation.state === LifecycleState.SUPERSEDED)
    return translate("superseded");
  if (
    invitation.state === LifecycleState.FAILED ||
    invitation.deliveryState === DeliveryState.FAILED
  )
    return translate("deliveryFailed");
  if (
    invitation.state === LifecycleState.EXPIRED ||
    (milliseconds(invitation.expiresAt) ?? Infinity) <= now
  )
    return translate("expired");
  if (invitation.deliveryState === DeliveryState.RETRY_PENDING)
    return translate("retryScheduled");
  if (invitation.deliveryState === DeliveryState.SENT) return translate("sent");
  if (invitation.deliveryState === DeliveryState.QUEUED)
    return translate("pendingDelivery");
  return translate("unknownStatus");
}
export function Invitations() {
  useLocale();
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
        title={translate("invitations")}
        description={translate("inviteAMemberToThisOrganization")}
        action={
          <button
            className="admin-primary"
            onClick={() => {
              setEmail("");
              setOpen(true);
            }}
          >
            {translate("inviteMember")}
          </button>
        }
      />
      {action.feedback}
      <p>{translate("resendsAreAtLeastOneMinuteApart")}</p>
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : (
        <Card>
          {!query.data?.invitations.length && (
            <Empty>{translate("noInvitationsYetInviteYourFirstMember")}</Empty>
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
                    <p>{translate("roleMember")}</p>
                    <p>{translate("createdNotReported")}</p>
                    <span className="admin-badge">{status}</span>
                    <p>
                      {translate("expires_8c3e7e", {
                        value0: date(invitation.expiresAt),
                      })}
                    </p>
                    {status === translate("deliveryFailed") && (
                      <p className="admin-danger-text">
                        {translate("theLinkIsInvalidResendToCreate")}
                      </p>
                    )}
                    {invitation.nextAttemptAt && (
                      <p>
                        {translate("nextDeliveryAttemptDeliveryCanBeDeferred", {
                          value0: date(invitation.nextAttemptAt),
                        })}
                      </p>
                    )}
                    <p>
                      {translate("sendAttempts", {
                        value0: invitation.deliveryAttempts,
                      })}
                    </p>
                    {!available && (
                      <p>{translate("resendTimingIsNotReportedTheServer")}</p>
                    )}
                    {waiting && (
                      <p id={`cooldown-${invitation.id}`}>
                        {translate("resendAvailableAtWaitSeconds", {
                          value0: new Date(available).toLocaleTimeString(
                            getLocale(),
                          ),
                          value1: Math.ceil((available - now) / 1000),
                        })}
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
                              success: translate(
                                "newInvitationCreatedThePreviousLinkIs",
                              ),
                            },
                          )
                        }
                      >
                        {translate("resend")}
                      </button>
                      {invitation.state === LifecycleState.PENDING &&
                        status !== translate("expired") && (
                          <button onClick={() => setCancel(invitation)}>
                            {translate("cancelInvitation")}
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
        <Dialog
          title={translate("inviteMember")}
          onClose={() => setOpen(false)}
        >
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
                  success: translate("invitationCreatedDeliveryIsPending"),
                },
              );
            }}
          >
            <label>
              {translate("emailAddress")}
              <input
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </label>
            <p>{translate("theInvitedPersonJoinsAsAMember")}</p>
            <ErrorNotice error={action.error} />
            <div className="admin-actions">
              <button type="button" data-cancel onClick={() => setOpen(false)}>
                {translate("cancel")}
              </button>
              <button className="admin-primary" disabled={action.pending}>
                {translate("sendInvitation")}
              </button>
            </div>
          </form>
        </Dialog>
      )}
      {cancel && (
        <Confirm
          title={translate("cancelInvitation")}
          name={cancel.email}
          pending={action.pending}
          onClose={() => setCancel(undefined)}
          action={() =>
            void action.run(
              () =>
                remove.mutateAsync({ organizationId, invitationId: cancel.id }),
              {
                done: () => setCancel(undefined),
                success: translate("invitationCanceled"),
              },
            )
          }
        >
          <p>
            {translate("theInvitationLinkForWillStopWorking", {
              value0: cancel.email,
            })}
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </>
  );
}

export function Settings({ organization }: { organization: Organization }) {
  useLocale();
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
        title={translate("organizationSettings")}
        description={translate("manage", { value0: organization.name })}
      />
      {action.feedback}
      <Card>
        <h2>{translate("organizationName")}</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void action.run(() => update.mutateAsync({ organizationId, name }));
          }}
        >
          <label>
            {translate("name")}
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
            {translate("saveName")}
          </button>
        </form>
      </Card>
      {role === Role.OWNER && (
        <>
          <Ownership />
          <Card>
            <h2>{translate("deleteOrganization")}</h2>
            <p>{translate("asTheSoleOwnerTransferOwnershipOr")}</p>
            <button
              className="admin-danger-outline"
              onClick={() => setDeleting(true)}
            >
              {translate("deleteOrganization")}
            </button>
          </Card>
        </>
      )}
      {deleting && role === Role.OWNER && (
        <Confirm
          title={translate("deleteOrganization")}
          name={organization.name}
          pending={action.pending}
          onClose={() => setDeleting(false)}
          action={() =>
            void action.run(() => remove.mutateAsync({ organizationId }), {
              owner: true,
              done: () => setDeleting(false),
              success: translate("organizationDeleted"),
            })
          }
        >
          <p>
            {translate("thisPermanentlyDeletesSCredentialsSettingsTemporary", {
              value0: organization.name,
            })}
          </p>
          <p>{translate("newRequestsStopImmediatelyResponsesAlreadySent")}</p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </>
  );
}
function Ownership() {
  useLocale();
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
      <h2>{translate("ownershipTransfer")}</h2>
      {action.feedback}
      {query.isPending ? (
        <Loading />
      ) : query.error &&
        ConnectError.from(query.error).code !== Code.NotFound ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : transfer ? (
        <div>
          <p>
            {translate("nominee", {
              value0: person?.displayName || transfer.toUserId,
            })}
          </p>
          <p>
            {translate("expires_5f50e1", {
              value0: expired
                ? translate("message_fa90c49b8b")
                : pending
                  ? translate("message_1dc7bfd9c5")
                  : label(LifecycleState[transfer.state]),
              value1: date(transfer.expiresAt),
            })}
          </p>
          {pending && !expired && (
            <button onClick={() => setCancel(true)}>
              {translate("cancelTransfer")}
            </button>
          )}
        </div>
      ) : (
        <p>{translate("noPendingTransfer")}</p>
      )}
      <p>{translate("youRemainOwnerUntilTheNomineeAccepts")}</p>
      <ErrorNotice error={members.error} />
      <label>
        {translate("nominateAnExistingMember")}
        <select value={nominee} onChange={(e) => setNominee(e.target.value)}>
          <option value="">{translate("chooseAMember")}</option>
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
        {pending && !expired
          ? translate("replaceNomination")
          : translate("nominateOwner")}
      </button>
      {confirm && selected && (
        <Confirm
          title={translate("nominateOwner")}
          pending={action.pending}
          onClose={() => setConfirm(false)}
          action={() =>
            void action.run(
              () => nominate.mutateAsync({ organizationId, userId: nominee }),
              {
                owner: true,
                done: () => setConfirm(false),
                success: translate("ownershipNominationSent"),
              },
            )
          }
        >
          <p>
            {translate("nominateAnyPreviousRequestBecomesInvalidOwnership", {
              value0: selected.displayName || selected.email,
            })}
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
      {cancel && transfer && (
        <Confirm
          title={translate("cancelTransfer")}
          pending={action.pending}
          onClose={() => setCancel(false)}
          action={() =>
            void action.run(
              () =>
                remove.mutateAsync({ organizationId, transferId: transfer.id }),
              {
                owner: true,
                done: () => setCancel(false),
                success: translate("ownershipTransferCanceled"),
              },
            )
          }
        >
          <p>{translate("theNomineeWillNoLongerBeAble")}</p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </Card>
  );
}
