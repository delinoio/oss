// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, getLocale } from "../i18n";
import { useContext, useEffect, useId, useState } from "react";
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
  ConfirmFocus,
  ConfirmTone,
  Dialog,
  DialogSize,
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
  const { organizationId, role, organizationName } = useContext(AdminAuthority);
  const transport = useTransport();
  const [token, setToken] = useState("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Member>();
  const searchHelperId = useId();
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
  // Page-local filter: case-insensitive substring over the loaded page only.
  // The query is intentionally not trimmed or reinterpreted.
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
      {organizationName && (
        <p className="admin-quiet admin-members-context">
          {translate("organizationMembersIn", { value0: organizationName })}
        </p>
      )}
      {/* While the removal dialog is open it owns the error and pending
          announcements, so the same message is not announced twice. */}
      {!selected && action.feedback}
      <Card className="admin-members-card">
        <label>
          {translate("searchThisPage")}
          <input
            type="search"
            placeholder={translate("searchNameOrEmail")}
            aria-describedby={searchHelperId}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <p id={searchHelperId} className="admin-helper">
          {translate("searchAppliesOnlyToTheLoaded")}
        </p>
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
            {members.length > 0 && (
              <div className="admin-members-columns" aria-hidden="true">
                <span>{translate("member")}</span>
                <span>{translate("role")}</span>
                <span>{translate("message_69318b0c6a")}</span>
                <span>{translate("actions")}</span>
              </div>
            )}
            <div className="admin-records admin-members-list">
              {members.map((member) => {
                const identity = member.displayName || member.email;
                const canChangeRole =
                  role === Role.OWNER && member.role !== Role.OWNER;
                const canDelete = canRemove(role, member.role);
                const roleLabel =
                  member.role === Role.ADMINISTRATOR
                    ? translate("makeMember")
                    : translate("makeAdministrator");
                return (
                  <article
                    className="admin-record admin-member"
                    key={member.userId}
                  >
                    <div className="admin-member-identity">
                      <h2>{identity}</h2>
                      {/* The email is the heading when no display name exists,
                          so it is not repeated beneath it. */}
                      {member.displayName && <p>{member.email}</p>}
                    </div>
                    <div className="admin-member-role">
                      <span className="admin-member-label">
                        {translate("role")}
                      </span>
                      <span className="admin-badge">
                        {label(Role[member.role])}
                      </span>
                    </div>
                    <div className="admin-member-joined">
                      <span className="admin-member-label">
                        {translate("message_69318b0c6a")}
                      </span>
                      <span>{date(member.joinedAt)}</span>
                    </div>
                    <div
                      className="admin-actions admin-member-actions"
                      role={canChangeRole || canDelete ? "group" : undefined}
                      aria-label={
                        canChangeRole || canDelete
                          ? translate("actionsFor", { value0: identity })
                          : undefined
                      }
                    >
                      {canChangeRole && (
                        <button
                          disabled={action.pending}
                          aria-label={translate("message_6a776812d7", {
                            value0: roleLabel,
                            value1: identity,
                          })}
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
                          {roleLabel}
                        </button>
                      )}
                      {canDelete && (
                        <button
                          className="admin-danger-outline"
                          onClick={() => setSelected(member)}
                        >
                          {translate("remove_3028bf", { value0: identity })}
                        </button>
                      )}
                    </div>
                  </article>
                );
              })}
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
          helper={translate("theNameMustMatchExactly")}
          size={DialogSize.Large}
          pendingStatus
          headingFallback
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
          <div className="admin-member-summary">
            <strong>{selected.displayName || selected.email}</strong>
            {selected.displayName && <span>{selected.email}</span>}
          </div>
          <div className="admin-consequences">
            <p>
              {translate("removeTheirOrganizationKeysStopWorkingImmediately", {
                value0: selected.displayName || selected.email,
              })}
            </p>
            <p>
              {translate("usageRemainsSubjectToRetentionAlreadyDispatched")}
            </p>
          </div>
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
export enum InvitationTiming {
  NextAttempt = "next-attempt",
  StoredAttempt = "stored-attempt",
  None = "none",
}
// Decides how a returned nextAttemptAt may be presented. The server serializes
// nextAttemptAt for every lifecycle (for example eight days ahead after a
// successful send), so its presence alone never proves a scheduled delivery.
export function invitationTiming(invitation: Invitation, now: number) {
  if (!invitation.nextAttemptAt) return InvitationTiming.None;
  const expiresAt = milliseconds(invitation.expiresAt);
  const deliverable =
    invitation.state === LifecycleState.PENDING &&
    (invitation.deliveryState === DeliveryState.QUEUED ||
      invitation.deliveryState === DeliveryState.RETRY_PENDING) &&
    expiresAt !== undefined &&
    expiresAt > now;
  return deliverable
    ? InvitationTiming.NextAttempt
    : InvitationTiming.StoredAttempt;
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
  const inviteHelperId = useId();
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
      <p className="admin-quiet">{translate("resendsAreAtLeastOneMinute")}</p>
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
              const timing = invitationTiming(invitation, now);
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
                <article
                  className="admin-record admin-invitation"
                  key={invitation.id}
                >
                  <div className="admin-invitation-identity">
                    <h2>{invitation.email}</h2>
                    <p>{translate("roleMember")}</p>
                  </div>
                  <div className="admin-invitation-status">
                    <span className="admin-badge">{status}</span>
                  </div>
                  <div className="admin-invitation-dates">
                    <p>
                      {translate("expires_8c3e7e", {
                        value0: date(invitation.expiresAt),
                      })}
                    </p>
                    <p>
                      {invitation.createdAt
                        ? translate("message_230b931bd9", {
                            value0: date(invitation.createdAt),
                          })
                        : translate("createdNotReported")}
                    </p>
                  </div>
                  <div className="admin-invitation-diagnostics">
                    {status === translate("deliveryFailed") && (
                      <p className="admin-danger-text">
                        {translate("theLinkIsInvalidResendToCreate")}
                      </p>
                    )}
                    {timing === InvitationTiming.NextAttempt && (
                      <p>
                        {translate("nextDeliveryAttemptDeliveryCanBeDeferred", {
                          value0: date(invitation.nextAttemptAt),
                        })}
                      </p>
                    )}
                    {timing === InvitationTiming.StoredAttempt && (
                      <p>
                        {translate("storedAttemptTimeThisTimestampDoes", {
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
          <p className="admin-quiet">
            {translate("sentMeansTheEmailWasSubmitted")}
          </p>
        </Card>
      )}
      {open && (
        <Dialog
          title={translate("inviteMember")}
          size={DialogSize.Compact}
          headingFallback
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
            <p>{translate("inviteSomeoneToThisOrganizationAs")}</p>
            <label>
              {translate("emailAddress")}
              <input
                type="email"
                required
                placeholder={translate("nameExampleTest")}
                aria-describedby={inviteHelperId}
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </label>
            <p id={inviteHelperId} className="admin-helper">
              {translate("theyMustSignInWithThis")}
            </p>
            <p>{translate("message_f2ebd8cccf")}</p>
            <p className="admin-helper">
              {translate("promotionToAdministratorIsManagedSeparately")}
            </p>
            <p className="admin-quiet">
              {translate("invitationLinksExpireAfterSevenDays")}
            </p>
            <ErrorNotice error={action.error} />
            <p role="status" className="admin-dialog-status">
              {action.pending ? translate("message_238b2274b1") : ""}
            </p>
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
          helper={translate("enterTheEmailAddressExactlyAs")}
          initialFocus={ConfirmFocus.Input}
          size={DialogSize.Compact}
          confirmLabel={translate("confirm")}
          pendingStatus
          headingFallback
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
            <strong>{cancel.email}</strong>
          </p>
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
  // The draft is local state: refetches, failed saves and owner dialogs never
  // replace it, and a save never overwrites text typed while it was in flight.
  const [name, setName] = useState(organization.name);
  const [deleting, setDeleting] = useState(false);
  const errorId = useId();
  const dirty = useDirtyForm();
  useEffect(() => {
    dirty(name !== organization.name);
    return () => dirty(false);
  }, [dirty, name, organization.name]);
  const saveAction = useAction();
  const deleteAction = useAction();
  const update = useMutation(OrganizationService.method.updateOrganization);
  const remove = useMutation(OrganizationService.method.deleteOrganization);
  return (
    <div className="admin-settings">
      <PageHeader
        title={translate("organizationSettings")}
        description={translate("manage", { value0: organization.name })}
      />
      <Card className="admin-settings-card">
        <h2>{translate("organizationName")}</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void saveAction.run(() =>
              update.mutateAsync({ organizationId, name }),
            );
          }}
        >
          <label>
            {translate("name")}
            <input
              autoFocus
              required
              value={name}
              aria-invalid={saveAction.error ? true : undefined}
              aria-describedby={saveAction.error ? errorId : undefined}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <div id={errorId}>{saveAction.feedback}</div>
          <button
            className="admin-primary"
            disabled={
              saveAction.pending || !name.trim() || name === organization.name
            }
          >
            {translate("saveName")}
          </button>
        </form>
      </Card>
      {role === Role.OWNER && (
        <>
          <Ownership organizationName={organization.name} />
          <Card className="admin-settings-card admin-danger-card">
            <h2>{translate("deleteOrganization")}</h2>
            <p>{translate("message_85d815948f")}</p>
            <p>
              {translate("deletionRemovesCredentialsSettingsTemporaryState")}
            </p>
            {deleteAction.feedback}
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
          helper={translate("theNameMustMatchExactly")}
          initialFocus={ConfirmFocus.Input}
          size={DialogSize.Wide}
          pendingStatus
          pending={deleteAction.pending}
          onClose={() => setDeleting(false)}
          action={() =>
            void deleteAction.run(
              () => remove.mutateAsync({ organizationId }),
              {
                owner: true,
                done: () => setDeleting(false),
                success: translate("organizationDeleted"),
              },
            )
          }
        >
          <p>
            <strong>
              {translate("permanentlyDelete", { value0: organization.name })}
            </strong>
          </p>
          <p>
            {translate("thisPermanentlyDeletesSCredentialsSettingsTemporary", {
              value0: organization.name,
            })}
          </p>
          <p>{translate("newRequestsStopImmediatelyResponsesAlreadySent")}</p>
          <ErrorNotice error={deleteAction.error} />
        </Confirm>
      )}
    </div>
  );
}
function Ownership({ organizationName }: { organizationName: string }) {
  useLocale();
  const { organizationId } = useContext(AdminAuthority);
  const [nominee, setNominee] = useState("");
  const [token, setToken] = useState("");
  const [confirm, setConfirm] = useState(false);
  // The transfer shown when Cancel transfer was clicked. Retaining it keeps a
  // later replacement nomination from being canceled by accident.
  const [cancelTarget, setCancelTarget] = useState<{
    id: string;
    nominee: string;
  }>();
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
  // NotFound means there is no transfer. Any other failure means the state is
  // unknown, which must never be presented as an empty state.
  const notFound =
    !!query.error && ConnectError.from(query.error).code === Code.NotFound;
  const unknown = !!query.error && !notFound;
  const transfer = notFound ? undefined : query.data?.transfer;
  const undetermined = query.isPending || unknown;
  const pending = transfer?.state === LifecycleState.PENDING;
  const expired =
    !!transfer &&
    (transfer.state === LifecycleState.EXPIRED ||
      (milliseconds(transfer.expiresAt) ?? Infinity) <= now);
  const replacing = pending && !expired;
  const person = members.data?.members.find(
    (m) => m.userId === transfer?.toUserId,
  );
  const nomineeName = person?.displayName || transfer?.toUserId || "";
  const eligible =
    members.data?.members.filter((m) => m.role !== Role.OWNER) ?? [];
  const selected = eligible.find((m) => m.userId === nominee);
  const selectedName = selected ? selected.displayName || selected.email : "";
  const nominationTitle = replacing
    ? translate("replaceNomination")
    : translate("nominateOwner");
  return (
    <Card className="admin-settings-card">
      <h2>{translate("ownershipTransfer")}</h2>
      {action.feedback}
      {query.isPending ? (
        <Loading label={translate("loadingOwnershipTransfer")} />
      ) : unknown ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : transfer ? (
        <div className="admin-status-block">
          <dl>
            <div>
              <dt>{translate("message_8d8d0be9ef")}</dt>
              <dd>{nomineeName}</dd>
            </div>
            <div>
              <dt>{translate("status")}</dt>
              <dd>
                {expired
                  ? translate("message_fa90c49b8b")
                  : pending
                    ? translate("message_1dc7bfd9c5")
                    : transfer.state === LifecycleState.UNSPECIFIED ||
                        LifecycleState[transfer.state] === undefined
                      ? translate("unknown")
                      : label(LifecycleState[transfer.state])}
              </dd>
            </div>
            <div>
              <dt>{translate("expires_f6725f")}</dt>
              <dd>{date(transfer.expiresAt)}</dd>
            </div>
          </dl>
          {replacing && (
            <button
              onClick={() =>
                setCancelTarget({ id: transfer.id, nominee: nomineeName })
              }
            >
              {translate("cancelTransfer")}
            </button>
          )}
        </div>
      ) : (
        <p className="admin-status-block">{translate("noPendingTransfer")}</p>
      )}
      <p>{translate("youRemainOwnerUntilTheNomineeAccepts")}</p>
      {members.isPending ? (
        <Loading label={translate("loadingMembers")} />
      ) : (
        <ErrorNotice
          error={members.error}
          retry={() => void members.refetch()}
        />
      )}
      <label>
        {translate("nominateAnExistingMember")}
        <select
          value={nominee}
          disabled={!members.data}
          onChange={(e) => setNominee(e.target.value)}
        >
          <option value="">{translate("chooseAMember")}</option>
          {eligible.map((m) => (
            <option key={m.userId} value={m.userId}>
              {m.displayName || m.email}
            </option>
          ))}
        </select>
      </label>
      {members.data && eligible.length === 0 && (
        <p>{translate("noEligibleMembersOnThisPage")}</p>
      )}
      <Pager
        token={token}
        next={members.data?.page?.nextPageToken}
        onChange={(value) => {
          setToken(value);
          setNominee("");
        }}
      />
      <div className="admin-actions">
        <button
          className="admin-primary"
          disabled={!selected || action.pending || undetermined}
          onClick={() => setConfirm(true)}
        >
          {nominationTitle}
        </button>
      </div>
      {confirm && selected && (
        <Confirm
          title={nominationTitle}
          tone={ConfirmTone.Primary}
          size={DialogSize.Wide}
          pendingStatus
          pending={action.pending}
          onClose={() => setConfirm(false)}
          action={() =>
            void action.run(
              () =>
                nominate.mutateAsync({
                  organizationId,
                  userId: selected.userId,
                }),
              {
                owner: true,
                refetchOnConflict: true,
                done: () => setConfirm(false),
                success: translate("ownershipNominationSent"),
              },
            )
          }
        >
          <p>
            {translate("nominateAsOwnerOf", {
              value0: selectedName,
              value1: organizationName,
            })}
          </p>
          <p>
            {replacing && person
              ? translate("thePreviousRequestForBecomesInvalid", {
                  value0: person.displayName || person.email,
                })
              : translate("anyPreviousRequestBecomesInvalid")}
          </p>
          <p>
            {translate("ownershipChangesOnlyAfterAcceptsBecomes", {
              value0: selectedName,
            })}
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
      {cancelTarget && (
        <Confirm
          title={translate("cancelTransfer")}
          cancelLabel={translate("keepTransfer")}
          size={DialogSize.Standard}
          pendingStatus
          pending={action.pending}
          onClose={() => setCancelTarget(undefined)}
          action={() =>
            void action.run(
              () =>
                remove.mutateAsync({
                  organizationId,
                  transferId: cancelTarget.id,
                }),
              {
                owner: true,
                refetchOnConflict: true,
                done: () => setCancelTarget(undefined),
                success: translate("ownershipTransferCanceled"),
              },
            )
          }
        >
          <p>
            {translate("cancelTheOwnershipTransferTo", {
              value0: cancelTarget.nominee,
            })}
          </p>
          <p>
            {translate("theNomineeWillNoLongerBe", {
              value0: organizationName,
            })}
          </p>
          <ErrorNotice error={action.error} />
        </Confirm>
      )}
    </Card>
  );
}
