import {
  useInfiniteQuery,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query";
import { createClient, type Transport } from "@connectrpc/connect";
import {
  BillingPeriodStatus,
  BillingService,
  CatalogService,
  InvitationStatus,
  LedgerOperation,
  OrganizationRole,
  OrganizationService,
  SubscriptionStatus,
  TeamAccessSource,
  TeamService,
  TeamRole,
  UsageRecordStatus,
  type BillingSummary,
  type EffectiveTeamAccess,
  type LedgerEntry,
  type Organization,
  type OrganizationInvitation,
  type OrganizationMember,
  type Team,
  type TeamMembership,
  type UsageRecord,
} from "@delinoio/delibase-connect";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type FormEvent,
} from "react";
import { useNavigate } from "react-router-dom";

import { usePublicTransport } from "../api/ApiContext";
import { describeDelibaseError } from "../api/errors";
import { CatalogCard } from "../components/CatalogCard";
import { Dialog } from "../components/Dialog";
import { useAccountState } from "../components/ProtectedRoute";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  OfflineActionHint,
} from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";
import { useOnline } from "../hooks/useOnline";
import {
  createIdempotencyKey,
  formatEnumLabel,
  formatUsdMicros,
} from "../utils/format";
import {
  hostedBillingReturnUrl,
  navigateToPolarHostedPage,
} from "../utils/hostedBilling";
import { useOrganization } from "./OrganizationShell";

function uuid(value: string | undefined) {
  return value ? { value } : undefined;
}

const slugPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const maxSignedInt64 = 9_223_372_036_854_775_807n;
const maximumTeamLevels = 5;

export function parseUsdMicros(value: string): bigint | undefined {
  if (!/^\d+(?:\.\d{1,6})?$/.test(value)) return undefined;
  const parts = value.split(".");
  const whole = parts[0]!;
  const fraction = parts[1] ?? "";
  const micros =
    BigInt(whole) * 1_000_000n +
    BigInt(fraction.padEnd(6, "0"));
  return micros <= maxSignedInt64 ? micros : undefined;
}

function formatUsdMicrosInput(value: bigint): string {
  const whole = value / 1_000_000n;
  const fraction = (value % 1_000_000n)
    .toString()
    .padStart(6, "0")
    .replace(/0+$/, "");
  return fraction ? `${whole}.${fraction}` : whole.toString();
}

export function getEditableOverageLimit(
  configured: boolean,
  value: bigint | undefined,
): bigint | undefined {
  return configured ? value : 0n;
}

export function formatOptionalUsdMicros(value: bigint | undefined): string {
  return value === undefined ? "Unavailable" : formatUsdMicros(value);
}

export function formatOverageLimit(
  configured: boolean,
  value: bigint | undefined,
): string {
  return configured ? formatOptionalUsdMicros(value) : "Not set";
}

export function canManageOrganization(role: OrganizationRole): boolean {
  return (
    role === OrganizationRole.OWNER ||
    role === OrganizationRole.ADMIN
  );
}

function OrganizationPageHeading({
  description,
  title,
}: {
  description: string;
  title: string;
}) {
  return (
    <header className="organization-page-heading">
      <h1>{title}</h1>
      <p>{description}</p>
    </header>
  );
}

export function OrganizationAppsPage() {
  useDocumentMetadata("Organization apps", "Browse apps for your organization.");
  const transport = usePublicTransport();
  const catalog = useInfiniteQuery(
    CatalogService.method.listCatalogApps,
    { page: { cursor: "", pageSize: 50 } },
    {
      gcTime: 15 * 60 * 1000,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 50 } : undefined;
      },
      networkMode: "always",
      pageParamKey: "page",
      staleTime: 5 * 60 * 1000,
      transport,
    },
  );
  const apps = catalog.data?.pages.flatMap((page) => page.apps) ?? [];
  return (
    <>
      <OrganizationPageHeading
        description="Choose a tool for your team. Usage is attributed by team."
        title="Apps"
      />
      {catalog.isPending ? <LoadingState label="Loading apps" /> : null}
      {catalog.isError && !catalog.data ? (
        <ErrorState
          error={catalog.error}
          onRetry={() => void catalog.refetch()}
          title="Apps unavailable"
        />
      ) : null}
      {catalog.data && apps.length === 0 ? (
        <EmptyState
          description="There are no enabled apps in the public catalog."
          title="No apps yet"
        />
      ) : null}
      {apps.length ? (
        <>
          <div className="catalog-grid compact-grid">
            {apps.map((app) => (
              <CatalogCard app={app} key={app.slug} />
            ))}
          </div>
          {catalog.isFetchNextPageError ? (
            <p className="inline-error" role="alert">
              {catalog.error.message}
            </p>
          ) : null}
          {catalog.hasNextPage ? (
            <div className="pagination-actions">
              <button
                className="button secondary"
                disabled={catalog.isFetchingNextPage}
                onClick={() => void catalog.fetchNextPage()}
                type="button"
              >
                {catalog.isFetchingNextPage
                  ? "Loading more…"
                  : "Load more apps"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </>
  );
}

export function MembersPage() {
  useDocumentMetadata(
    "Members",
    "View organization members and manage invitations.",
  );
  const {
    callerRole,
    organization,
    refreshOrganization,
    transport,
  } = useOrganization();
  const { accountState, refreshAccountState } = useAccountState();
  const members = useInfiniteQuery(
    OrganizationService.method.listOrganizationMembers,
    {
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const memberRows =
    members.data?.pages.flatMap((page) => page.members) ?? [];
  return (
    <>
      <OrganizationPageHeading
        description="View organization members, roles, and active invitations."
        title="Members"
      />
      <p className="muted">
        Organizations may have multiple Owners. The server blocks any role
        change, removal, departure, or account deletion that would remove the
        last Owner.
      </p>
      {canManageOrganization(callerRole) ? (
        <OrganizationInvitationManagement
          organization={organization}
          transport={transport}
        />
      ) : null}
      {members.isPending ? <LoadingState label="Loading members" /> : null}
      {members.isError && !members.data ? (
        <ErrorState
          error={members.error}
          onRetry={() => void members.refetch()}
          title="Members unavailable"
        />
      ) : null}
      {memberRows.length === 0 && members.data ? (
        <EmptyState
          description="No additional organization members are listed."
          title="No members found"
        />
      ) : null}
      {memberRows.length ? (
        <>
          <div className="table-card">
            <table>
              <caption className="sr-only">Organization members</caption>
              <thead>
                <tr>
                  <th scope="col">Member</th>
                  <th scope="col">Role</th>
                  <th scope="col">Joined</th>
                  {canManageOrganization(callerRole) ? (
                    <th scope="col">Actions</th>
                  ) : null}
                </tr>
              </thead>
              <tbody>
                {memberRows.map((member) => (
                  <tr key={member.accountId?.value}>
                    <td>
                      <span className="avatar" aria-hidden="true">
                        {member.displayName.slice(0, 1)}
                      </span>
                      <strong>{member.displayName}</strong>
                    </td>
                    <td>
                      <span className="badge">
                        {formatEnumLabel(
                          OrganizationRole[member.role] ?? member.role,
                        )}
                      </span>
                    </td>
                    <td>
                      {member.joinedAt
                        ? new Date(
                            Number(member.joinedAt.seconds) * 1000,
                          ).toLocaleDateString("en-US")
                        : "—"}
                    </td>
                    {canManageOrganization(callerRole) ? (
                      <td>
                        <MemberActions
                          callerAccountId={
                            accountState.account?.accountId?.value ?? ""
                          }
                          callerRole={callerRole}
                          member={member}
                          onUpdated={async () => {
                            await Promise.all([
                              members.refetch({ throwOnError: true }),
                              refreshAccountState(),
                              refreshOrganization(),
                            ]);
                          }}
                          organization={organization}
                          transport={transport}
                        />
                      </td>
                    ) : null}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {members.isFetchNextPageError ? (
            <p className="inline-error" role="alert">
              {members.error.message}
            </p>
          ) : null}
          {members.hasNextPage ? (
            <div className="pagination-actions">
              <button
                className="button secondary"
                disabled={members.isFetchingNextPage}
                onClick={() => void members.fetchNextPage()}
                type="button"
              >
                {members.isFetchingNextPage
                  ? "Loading more…"
                  : "Load more members"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </>
  );
}

export function canManageMember(
  callerRole: OrganizationRole,
  targetRole: OrganizationRole,
): boolean {
  return (
    callerRole === OrganizationRole.OWNER ||
    (callerRole === OrganizationRole.ADMIN &&
      targetRole !== OrganizationRole.OWNER)
  );
}

function MemberActions({
  callerAccountId,
  callerRole,
  member,
  onUpdated,
  organization,
  transport,
}: {
  callerAccountId: string;
  callerRole: OrganizationRole;
  member: OrganizationMember;
  onUpdated: () => Promise<void>;
  organization: Organization;
  transport: Transport;
}) {
  const { refreshAccountState } = useAccountState();
  const navigate = useNavigate();
  const online = useOnline();
  const [open, setOpen] = useState(false);
  const [role, setRole] = useState(member.role);
  const [formError, setFormError] = useState("");
  const updateKey = useRef<{ key: string } | undefined>(undefined);
  const removalKey = useRef<{ key: string } | undefined>(undefined);
  const updateRole = useMutation(
    OrganizationService.method.updateOrganizationMemberRole,
    { transport },
  );
  const removeMember = useMutation(
    OrganizationService.method.removeOrganizationMember,
    { transport },
  );
  const leaveOrganization = useMutation(
    OrganizationService.method.leaveOrganization,
    { transport },
  );
  const accountId = member.accountId?.value ?? "";
  const isCurrentMember = accountId === callerAccountId;
  const isPending =
    updateRole.isPending ||
    removeMember.isPending ||
    leaveOrganization.isPending;
  const close = () => {
    if (isPending) return;
    updateKey.current = undefined;
    removalKey.current = undefined;
    setOpen(false);
  };

  if (!canManageMember(callerRole, member.role)) {
    return <span className="muted">Owner-managed</span>;
  }

  const saveRole = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError("");
    if (role === member.role) {
      setFormError("Choose a different organization role.");
      return;
    }
    updateKey.current ??= createIdempotencyKey();
    try {
      await updateRole.mutateAsync({
        accountId: member.accountId,
        idempotency: updateKey.current,
        organizationId: organization.organizationId,
        role,
      });
      await onUpdated();
      updateKey.current = undefined;
      setOpen(false);
    } catch (error) {
      setFormError(describeDelibaseError(error));
    }
  };

  const remove = async () => {
    setFormError("");
    removalKey.current ??= createIdempotencyKey();
    try {
      if (isCurrentMember) {
        await leaveOrganization.mutateAsync({
          idempotency: removalKey.current,
          organizationId: organization.organizationId,
        });
        removalKey.current = undefined;
        await refreshAccountState().catch(() => undefined);
        navigate("/account", { replace: true });
        return;
      }
      await removeMember.mutateAsync({
        accountId: member.accountId,
        idempotency: removalKey.current,
        organizationId: organization.organizationId,
      });
      await onUpdated();
      removalKey.current = undefined;
      setOpen(false);
    } catch (error) {
      setFormError(describeDelibaseError(error));
    }
  };

  return (
    <>
      <button
        className="button secondary compact-button"
        onClick={() => {
          setRole(member.role);
          setFormError("");
          setOpen(true);
        }}
        type="button"
      >
        Manage {member.displayName}
      </button>
      {open ? (
        <Dialog
          descriptionId={`member-description-${accountId}`}
          onClose={close}
          titleId={`member-title-${accountId}`}
        >
          <h2 id={`member-title-${accountId}`}>
            Manage {member.displayName}
          </h2>
          <p id={`member-description-${accountId}`}>
            Owners can manage every role. Admins cannot promote, demote, or
            remove an Owner.
          </p>
          <form onSubmit={(event) => void saveRole(event)}>
            <label>
              Organization role
              <select
                onChange={(event) => {
                  updateKey.current = undefined;
                  setRole(Number(event.target.value) as OrganizationRole);
                }}
                value={role}
              >
                {callerRole === OrganizationRole.OWNER ? (
                  <option value={OrganizationRole.OWNER}>Owner</option>
                ) : null}
                <option value={OrganizationRole.ADMIN}>Admin</option>
                <option value={OrganizationRole.MEMBER}>Member</option>
              </select>
            </label>
            {formError ? (
              <p className="inline-error" role="alert">
                {formError}
              </p>
            ) : null}
            <div className="dialog-actions">
              <button
                className="button danger"
                disabled={!online || isPending}
                onClick={() => void remove()}
                type="button"
              >
                {isCurrentMember
                  ? leaveOrganization.isPending
                    ? "Leaving…"
                    : "Leave organization"
                  : removeMember.isPending
                    ? "Removing…"
                    : "Remove member"}
              </button>
              <button
                className="button secondary"
                disabled={isPending}
                onClick={close}
                type="button"
              >
                Cancel
              </button>
              <button
                className="button primary"
                disabled={!online || isPending || role === member.role}
                type="submit"
              >
                {updateRole.isPending ? "Saving role…" : "Save role"}
              </button>
            </div>
            {!online ? <OfflineActionHint /> : null}
          </form>
        </Dialog>
      ) : null}
    </>
  );
}

type InvitationCreationState =
  | { status: "idle" }
  | { status: "pending" }
  | { error: string; status: "error" }
  | { invitationUrl: string; status: "success" };

export function OrganizationInvitationManagement({
  organization,
  transport,
}: {
  organization: Organization;
  transport: Transport;
}) {
  const online = useOnline();
  const client = useMemo(
    () => createClient(OrganizationService, transport),
    [transport],
  );
  const [organizationRole, setOrganizationRole] = useState(
    OrganizationRole.MEMBER,
  );
  const [teamId, setTeamId] = useState("");
  const [teamRole, setTeamRole] = useState(TeamRole.MEMBER);
  const [creation, setCreation] = useState<InvitationCreationState>({
    status: "idle",
  });
  const [revokingInvitationId, setRevokingInvitationId] = useState("");
  const [revokeError, setRevokeError] = useState("");
  const revocationKeys = useRef(new Map<string, { key: string }>());
  const teams = useInfiniteQuery(
    TeamService.method.listTeams,
    {
      includeDescendants: true,
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const invitations = useInfiniteQuery(
    OrganizationService.method.listOrganizationInvitations,
    {
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
      status: InvitationStatus.ACTIVE,
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const revokeInvitation = useMutation(
    OrganizationService.method.revokeOrganizationInvitation,
    { transport },
  );
  const teamRows = teams.data?.pages.flatMap((page) => page.teams) ?? [];
  const invitationRows =
    invitations.data?.pages.flatMap((page) => page.invitations) ?? [];
  const teamsById = new Map(
    teamRows.flatMap((team) =>
      team.teamId?.value ? [[team.teamId.value, team] as const] : [],
    ),
  );
  const isMemberInvitation = organizationRole === OrganizationRole.MEMBER;
  const resetCreatedLink = () => setCreation({ status: "idle" });

  const submitInvitation = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (isMemberInvitation && !teamId) {
      setCreation({
        error: "Choose a team for this Member invitation.",
        status: "error",
      });
      return;
    }
    setCreation({ status: "pending" });
    try {
      // The creation response contains the only copy of a bearer token, so it
      // intentionally bypasses React Query and remains in component memory.
      const response = await client.createOrganizationInvitation({
        organizationId: organization.organizationId,
        organizationRole,
        teamId: isMemberInvitation ? uuid(teamId) : undefined,
        teamRole: isMemberInvitation ? teamRole : TeamRole.UNSPECIFIED,
      });
      const token = response.bearerToken?.token;
      if (!token) {
        throw new Error("The invitation was created without a bearer link.");
      }
      setCreation({
        invitationUrl: `${window.location.origin}/invite/${encodeURIComponent(token)}`,
        status: "success",
      });
      void invitations.refetch();
    } catch (error) {
      setCreation({
        error: describeDelibaseError(error),
        status: "error",
      });
    }
  };

  const revoke = async (invitation: OrganizationInvitation) => {
    const invitationId = invitation.invitationId?.value;
    if (!invitationId) return;
    const idempotency =
      revocationKeys.current.get(invitationId) ?? createIdempotencyKey();
    revocationKeys.current.set(invitationId, idempotency);
    setRevokeError("");
    setRevokingInvitationId(invitationId);
    try {
      await revokeInvitation.mutateAsync({
        idempotency,
        invitationId: invitation.invitationId,
        organizationId: organization.organizationId,
      });
      const refreshedInvitations = await invitations.refetch();
      const invitationStillActive = refreshedInvitations.data?.pages.some(
        (page) =>
          page.invitations.some(
            (item) => item.invitationId?.value === invitationId,
          ),
      );
      if (refreshedInvitations.isSuccess && !invitationStillActive) {
        revocationKeys.current.delete(invitationId);
      }
    } catch (error) {
      setRevokeError(describeDelibaseError(error));
    } finally {
      setRevokingInvitationId("");
    }
  };

  return (
    <section className="invitation-management" aria-labelledby="invite-heading">
      <form className="form-card" onSubmit={submitInvitation}>
        <div>
          <span className="eyebrow">Invitation management</span>
          <h2 id="invite-heading">Create an invitation</h2>
          <p>
            Invitation links are reusable until they expire or are revoked.
            Share each bearer link only with its intended recipients.
          </p>
        </div>
        <div className="invitation-form-fields">
          <label>
            Organization role
            <select
              onChange={(event) => {
                resetCreatedLink();
                setOrganizationRole(
                  Number(event.target.value) as OrganizationRole,
                );
              }}
              value={organizationRole}
            >
              <option value={OrganizationRole.MEMBER}>Member</option>
              <option value={OrganizationRole.ADMIN}>Admin</option>
            </select>
          </label>
          {isMemberInvitation ? (
            <>
              <label>
                Team
                <select
                  onChange={(event) => {
                    resetCreatedLink();
                    setTeamId(event.target.value);
                  }}
                  required
                  value={teamId}
                >
                  <option value="">Choose a team</option>
                  {teamRows.map((team) => (
                    <option key={team.teamId?.value} value={team.teamId?.value}>
                      {team.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Team role
                <select
                  onChange={(event) => {
                    resetCreatedLink();
                    setTeamRole(Number(event.target.value) as TeamRole);
                  }}
                  value={teamRole}
                >
                  <option value={TeamRole.MEMBER}>Member</option>
                  <option value={TeamRole.ADMIN}>Admin</option>
                </select>
              </label>
            </>
          ) : null}
        </div>
        {teams.isError ? (
          <p className="inline-error" role="alert">
            {teams.error.message}
          </p>
        ) : null}
        {teams.hasNextPage ? (
          <button
            className="button secondary"
            disabled={teams.isFetchingNextPage}
            onClick={() => void teams.fetchNextPage()}
            type="button"
          >
            {teams.isFetchingNextPage ? "Loading teams…" : "Load more teams"}
          </button>
        ) : null}
        {creation.status === "error" ? (
          <p className="inline-error" role="alert">
            {creation.error}
          </p>
        ) : null}
        {creation.status === "success" ? (
          <label>
            Invitation link
            <input
              onFocus={(event) => event.currentTarget.select()}
              readOnly
              value={creation.invitationUrl}
            />
            <span className="field-help">
              This secret link is shown only for this in-memory session.
            </span>
          </label>
        ) : null}
        <button
          className="button primary"
          disabled={!online || creation.status === "pending"}
          type="submit"
        >
          {creation.status === "pending"
            ? "Creating invitation…"
            : "Create invitation"}
        </button>
        {!online ? <OfflineActionHint /> : null}
      </form>

      <h2>Active invitations</h2>
      {invitations.isPending ? (
        <LoadingState label="Loading invitations" />
      ) : null}
      {invitations.isError && !invitations.data ? (
        <ErrorState
          error={invitations.error}
          onRetry={() => void invitations.refetch()}
          title="Invitations unavailable"
        />
      ) : null}
      {invitationRows.length === 0 && invitations.data ? (
        <p className="content-card">There are no active invitations.</p>
      ) : null}
      {invitationRows.length ? (
        <div className="table-card">
          <table>
            <caption className="sr-only">Active organization invitations</caption>
            <thead>
              <tr>
                <th scope="col">Organization role</th>
                <th scope="col">Team assignment</th>
                <th scope="col">Expires</th>
                <th scope="col">Action</th>
              </tr>
            </thead>
            <tbody>
              {invitationRows.map((invitation) => {
                const invitationId = invitation.invitationId?.value ?? "";
                const team = teamsById.get(invitation.teamId?.value ?? "");
                return (
                  <tr key={invitationId}>
                    <td>
                      {formatEnumLabel(
                        OrganizationRole[invitation.organizationRole] ??
                          invitation.organizationRole,
                      )}
                    </td>
                    <td>
                      {invitation.teamId
                        ? `${team?.name ?? invitation.teamId.value} · ${formatEnumLabel(
                            TeamRole[invitation.teamRole] ?? invitation.teamRole,
                          )}`
                        : "All teams"}
                    </td>
                    <td>
                      {invitation.expiresAt
                        ? new Date(
                            Number(invitation.expiresAt.seconds) * 1000,
                          ).toLocaleDateString("en-US")
                        : "Unavailable"}
                    </td>
                    <td>
                      <button
                        className="button danger compact-button"
                        disabled={
                          !online || Boolean(revokingInvitationId)
                        }
                        onClick={() => void revoke(invitation)}
                        type="button"
                      >
                        {revokingInvitationId === invitationId
                          ? "Revoking…"
                          : "Revoke"}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      {revokeError ? (
        <p className="inline-error" role="alert">
          {revokeError}
        </p>
      ) : null}
      {invitations.isFetchNextPageError ? (
        <p className="inline-error" role="alert">
          {invitations.error.message}
        </p>
      ) : null}
      {invitations.hasNextPage ? (
        <div className="pagination-actions">
          <button
            className="button secondary"
            disabled={invitations.isFetchingNextPage}
            onClick={() => void invitations.fetchNextPage()}
            type="button"
          >
            {invitations.isFetchingNextPage
              ? "Loading more…"
              : "Load more invitations"}
          </button>
        </div>
      ) : null}
    </section>
  );
}

export function TeamsPage() {
  useDocumentMetadata("Teams", "Manage nested organization teams.");
  const { callerRole, organization, transport } = useOrganization();
  const { accountState } = useAccountState();
  const online = useOnline();
  const [deletedTeamIds, setDeletedTeamIds] = useState<Set<string>>(
    () => new Set(),
  );
  const teams = useInfiniteQuery(
    TeamService.method.listTeams,
    {
      includeDescendants: true,
      organizationId: uuid(organization.organizationId?.value),
      page: { cursor: "", pageSize: 100 },
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const organizationManager = canManageOrganization(callerRole);
  const effectiveAccess = useInfiniteQuery(
    TeamService.method.listEffectiveTeamAccess,
    {
      accountId: accountState.account?.accountId,
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
    },
    {
      enabled:
        !organizationManager &&
        Boolean(accountState.account?.accountId?.value),
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const loadedTeamRows =
    teams.data?.pages.flatMap((page) => page.teams) ?? [];
  const teamRows = loadedTeamRows.filter(
    (team) => !deletedTeamIds.has(team.teamId?.value ?? ""),
  );
  const effectiveAccessRows =
    effectiveAccess.data?.pages.flatMap((page) => page.access) ?? [];
  const adminTeamIds = new Set(
    effectiveAccessRows.flatMap((access) =>
      access.effectiveRole === TeamRole.ADMIN && access.teamId?.value
        ? [access.teamId.value]
        : [],
    ),
  );
  const canCreateTeam = organizationManager || adminTeamIds.size > 0;
  const refreshTeams = async () => {
    if (organizationManager) {
      await teams.refetch({ throwOnError: true });
      return;
    }
    await Promise.all([
      teams.refetch({ throwOnError: true }),
      effectiveAccess.refetch({ throwOnError: true }),
    ]);
  };
  const hideDeletedSubtree = (teamId: string) => {
    const subtreeIds = getTeamSubtreeIds(teamId, loadedTeamRows);
    setDeletedTeamIds((current) => new Set([...current, ...subtreeIds]));
  };
  return (
    <>
      <OrganizationPageHeading
        description="Create and organize teams. Access granted to a parent flows down to its descendants."
        title="Teams"
      />
      {canCreateTeam ? (
        <CreateTeamForm
          allowTopLevel={organizationManager}
          manageableParentIds={
            organizationManager ? undefined : adminTeamIds
          }
          onUpdated={refreshTeams}
          online={online}
          teams={teamRows}
        />
      ) : null}
      {teams.isPending ? <LoadingState label="Loading teams" /> : null}
      {!organizationManager && teams.data && effectiveAccess.isPending ? (
        <LoadingState label="Loading team capabilities" />
      ) : null}
      {teams.isError && !teams.data ? (
        <ErrorState
          error={teams.error}
          onRetry={() => void teams.refetch()}
          title="Teams unavailable"
        />
      ) : null}
      {teamRows.length === 0 && teams.data ? (
        <EmptyState
          description="Every organization starts with a protected General team."
          title="No teams found"
        />
      ) : null}
      {teamRows.length ? (
        <>
          <ul className="team-tree" aria-label="Team hierarchy">
            {teamRows.map((team) => (
              <li
                key={team.teamId?.value}
                style={{ "--team-depth": team.depth } as CSSProperties}
              >
                <span className="team-icon" aria-hidden="true">
                  {team.protectedGeneral ? "G" : "T"}
                </span>
                <div className="team-summary">
                  <strong>{team.name}</strong>
                  <small>
                    Level {team.depth + 1}
                    {team.protectedGeneral ? " · Protected" : ""}
                  </small>
                  {!organizationManager ? (
                    <TeamAccessLabel
                      access={effectiveAccessRows.find(
                        (item) =>
                          item.teamId?.value === team.teamId?.value,
                      )}
                    />
                  ) : (
                    <small>Team Admin · organization role</small>
                  )}
                </div>
                {(organizationManager ||
                  adminTeamIds.has(team.teamId?.value ?? "")) ? (
                  <TeamActions
                    allowTopLevel={organizationManager}
                    allTeamsLoaded={!teams.hasNextPage}
                    manageableParentIds={
                      organizationManager ? undefined : adminTeamIds
                    }
                    onDeleted={hideDeletedSubtree}
                    onUpdated={refreshTeams}
                    online={online}
                    team={team}
                    teams={teamRows}
                  />
                ) : null}
              </li>
            ))}
          </ul>
          {teams.isFetchNextPageError ? (
            <p className="inline-error" role="alert">
              {teams.error.message}
            </p>
          ) : null}
          {teams.hasNextPage ? (
            <div className="pagination-actions">
              <button
                className="button secondary"
                disabled={teams.isFetchingNextPage}
                onClick={() => void teams.fetchNextPage()}
                type="button"
              >
                {teams.isFetchingNextPage
                  ? "Loading more…"
                  : "Load more teams"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
      {effectiveAccess.isError ? (
        <ErrorState
          error={effectiveAccess.error}
          onRetry={() => void effectiveAccess.refetch()}
          title="Team capabilities unavailable"
        />
      ) : null}
      {effectiveAccess.hasNextPage ? (
        <div className="pagination-actions">
          <button
            className="button secondary"
            disabled={effectiveAccess.isFetchingNextPage}
            onClick={() => void effectiveAccess.fetchNextPage()}
            type="button"
          >
            {effectiveAccess.isFetchingNextPage
              ? "Loading capabilities…"
              : "Load more team capabilities"}
          </button>
        </div>
      ) : null}
    </>
  );
}

function TeamAccessLabel({
  access,
}: {
  access: EffectiveTeamAccess | undefined;
}) {
  if (!access) {
    return <small>Access details unavailable</small>;
  }
  const source = formatEnumLabel(
    TeamAccessSource[access.source] ?? access.source,
  );
  const role = formatEnumLabel(
    TeamRole[access.effectiveRole] ?? access.effectiveRole,
  );
  return (
    <small>
      Team {role} · {source}
    </small>
  );
}

function CreateTeamForm({
  allowTopLevel,
  manageableParentIds,
  online,
  onUpdated,
  teams,
}: {
  allowTopLevel: boolean;
  manageableParentIds?: Set<string>;
  online: boolean;
  onUpdated: () => Promise<void>;
  teams: Team[];
}) {
  const { organization, transport } = useOrganization();
  const [name, setName] = useState("");
  const [parentTeamId, setParentTeamId] = useState("");
  const [message, setMessage] = useState("");
  const [formError, setFormError] = useState("");
  const idempotencyKey = useRef<{ key: string } | undefined>(undefined);
  const createTeam = useMutation(TeamService.method.createTeam, { transport });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMessage("");
    setFormError("");
    const normalizedName = name.trim();
    if (!normalizedName) {
      setFormError("Enter a team name.");
      return;
    }
    idempotencyKey.current ??= createIdempotencyKey();
    createTeam.mutate(
      {
        idempotency: idempotencyKey.current,
        name: normalizedName,
        organizationId: organization.organizationId,
        parentTeamId: uuid(parentTeamId),
      },
      {
        onError: (error) =>
          setFormError(describeDelibaseError(error)),
        onSuccess: async () => {
          try {
            await onUpdated();
            idempotencyKey.current = undefined;
            setName("");
            setParentTeamId("");
            setMessage("Team created.");
          } catch (error) {
            setFormError(describeDelibaseError(error));
          }
        },
      },
    );
  };

  return (
    <form className="form-card team-create-form" onSubmit={submit}>
      <div>
        <span className="eyebrow">Team hierarchy</span>
        <h2>Create a team</h2>
      </div>
      <div className="team-form-fields">
        <label>
          Team name
          <input
            // This is the single critical input in the create-team form.
            // eslint-disable-next-line jsx-a11y/no-autofocus
            autoFocus
            maxLength={120}
            onChange={(event) => {
              idempotencyKey.current = undefined;
              setName(event.target.value);
            }}
            required
            value={name}
          />
        </label>
        <label>
          Parent team
          <select
            onChange={(event) => {
              idempotencyKey.current = undefined;
              setParentTeamId(event.target.value);
            }}
            value={parentTeamId}
          >
            {allowTopLevel ? <option value="">Top level</option> : null}
            {!allowTopLevel && !parentTeamId ? (
              <option value="">Choose a Team Admin parent</option>
            ) : null}
            {teams
              .filter(canCreateChildTeam)
              .filter(
                (team) =>
                  !manageableParentIds ||
                  manageableParentIds.has(team.teamId?.value ?? ""),
              )
              .map((team) => (
              <option key={team.teamId?.value} value={team.teamId?.value}>
                {team.name}
              </option>
              ))}
          </select>
        </label>
      </div>
      {formError ? (
        <p className="inline-error" role="alert">
          {formError}
        </p>
      ) : null}
      {message ? (
        <p className="inline-success" role="status">
          {message}
        </p>
      ) : null}
      <button
        className="button primary"
        disabled={
          !online ||
          createTeam.isPending ||
          (!allowTopLevel && !parentTeamId)
        }
        type="submit"
      >
        {createTeam.isPending ? "Creating team…" : "Create team"}
      </button>
      {!online ? <OfflineActionHint /> : null}
    </form>
  );
}

export function canCreateChildTeam(team: Team): boolean {
  return team.depth < maximumTeamLevels - 1;
}

export function canUseTeamAsParent(
  team: Team,
  candidate: Team,
  teams: Team[],
): boolean {
  const teamId = team.teamId?.value;
  let candidateId = candidate.teamId?.value;
  if (!teamId || !candidateId || teamId === candidateId) {
    return false;
  }
  const teamsById = new Map(
    teams.flatMap((item) =>
      item.teamId?.value ? [[item.teamId.value, item] as const] : [],
    ),
  );
  let subtreeHeight = 0;
  for (const item of teams) {
    let parentId = item.parentTeamId?.value;
    let relativeDepth = 0;
    const descendantPath = new Set<string>();
    while (parentId && !descendantPath.has(parentId)) {
      relativeDepth += 1;
      if (parentId === teamId) {
        subtreeHeight = Math.max(subtreeHeight, relativeDepth);
        break;
      }
      descendantPath.add(parentId);
      parentId = teamsById.get(parentId)?.parentTeamId?.value;
    }
  }
  const visited = new Set<string>();
  while (candidateId && !visited.has(candidateId)) {
    if (candidateId === teamId) {
      return false;
    }
    visited.add(candidateId);
    candidateId = teamsById.get(candidateId)?.parentTeamId?.value;
  }
  return candidate.depth + 1 + subtreeHeight < maximumTeamLevels;
}

export function getTeamSubtreeIds(teamId: string, teams: Team[]): Set<string> {
  const subtreeIds = new Set([teamId]);
  let foundDescendant = true;
  while (foundDescendant) {
    foundDescendant = false;
    for (const team of teams) {
      const id = team.teamId?.value;
      const parentId = team.parentTeamId?.value;
      if (
        id &&
        parentId &&
        subtreeIds.has(parentId) &&
        !subtreeIds.has(id)
      ) {
        subtreeIds.add(id);
        foundDescendant = true;
      }
    }
  }
  return subtreeIds;
}

function TeamActions({
  allowTopLevel,
  allTeamsLoaded,
  manageableParentIds,
  online,
  onDeleted,
  onUpdated,
  team,
  teams,
}: {
  allowTopLevel: boolean;
  allTeamsLoaded: boolean;
  manageableParentIds?: Set<string>;
  online: boolean;
  onDeleted: (teamId: string) => void;
  onUpdated: () => Promise<void>;
  team: Team;
  teams: Team[];
}) {
  const { organization, transport } = useOrganization();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(team.name);
  const [parentTeamId, setParentTeamId] = useState(
    team.parentTeamId?.value ?? "",
  );
  const [deleteConfirmed, setDeleteConfirmed] = useState(false);
  const [formError, setFormError] = useState("");
  const [membershipPending, setMembershipPending] = useState(false);
  const deletionKey = useRef<{ key: string } | undefined>(undefined);
  const updateTeam = useMutation(TeamService.method.updateTeam, { transport });
  const moveTeam = useMutation(TeamService.method.moveTeam, { transport });
  const deleteTeam = useMutation(TeamService.method.deleteTeamSubtree, {
    transport,
  });
  const teamId = team.teamId?.value ?? "";
  const titleId = `manage-team-${teamId}`;
  const descriptionId = `manage-team-description-${teamId}`;
  const isPending =
    updateTeam.isPending ||
    moveTeam.isPending ||
    deleteTeam.isPending ||
    membershipPending;
  const parentOptions = teams.filter((candidate) =>
    canUseTeamAsParent(team, candidate, teams),
  ).filter(
    (candidate) =>
      !manageableParentIds ||
      manageableParentIds.has(candidate.teamId?.value ?? ""),
  );

  const showDialog = () => {
    deletionKey.current = undefined;
    setName(team.name);
    setParentTeamId(team.parentTeamId?.value ?? "");
    setDeleteConfirmed(false);
    setFormError("");
    setOpen(true);
  };
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError("");
    const normalizedName = name.trim();
    if (!normalizedName) {
      setFormError("Enter a team name.");
      return;
    }
    const currentParentTeamId = team.parentTeamId?.value ?? "";
    const nameChanged = normalizedName !== team.name;
    const parentChanged = parentTeamId !== currentParentTeamId;
    if (!nameChanged && !parentChanged) {
      setFormError("Change the team name or parent before saving.");
      return;
    }
    let mutationSucceeded = false;
    try {
      if (nameChanged) {
        await updateTeam.mutateAsync({
          idempotency: createIdempotencyKey(),
          name: normalizedName,
          organizationId: organization.organizationId,
          teamId: uuid(teamId),
        });
        mutationSucceeded = true;
      }
      if (parentChanged) {
        await moveTeam.mutateAsync({
          idempotency: createIdempotencyKey(),
          newParentTeamId: uuid(parentTeamId),
          organizationId: organization.organizationId,
          teamId: uuid(teamId),
        });
        mutationSucceeded = true;
      }
      await onUpdated();
      setOpen(false);
    } catch (error) {
      if (mutationSucceeded) {
        await onUpdated().catch(() => undefined);
      }
      setFormError(
        describeDelibaseError(error),
      );
    }
  };
  const remove = async () => {
    if (!deleteConfirmed) {
      setFormError(
        "Confirm that you understand the full team subtree will be deleted.",
      );
      return;
    }
    setFormError("");
    deletionKey.current ??= createIdempotencyKey();
    try {
      await deleteTeam.mutateAsync({
        confirmSubtree: true,
        idempotency: deletionKey.current,
        organizationId: organization.organizationId,
        teamId: uuid(teamId),
      });
      onDeleted(teamId);
      deletionKey.current = undefined;
      setOpen(false);
      void onUpdated().catch(() => undefined);
    } catch (error) {
      setFormError(
        describeDelibaseError(error),
      );
    }
  };
  const closeDialog = () => {
    if (isPending) return;
    deletionKey.current = undefined;
    setOpen(false);
  };

  return (
    <>
      <button
        className="button secondary compact-button"
        onClick={showDialog}
        type="button"
      >
        Manage {team.name}
      </button>
      {open ? (
        <Dialog
          descriptionId={descriptionId}
          onClose={closeDialog}
          titleId={titleId}
        >
          <h2 id={titleId}>Manage {team.name}</h2>
          <p id={descriptionId}>
            {team.protectedGeneral
              ? "Manage direct membership. General is protected from rename, move, and deletion."
              : "Manage direct membership, rename or move the team, or permanently delete its subtree."}
          </p>
          {!team.protectedGeneral ? (
            <form className="team-dialog-form" onSubmit={submit}>
              <label>
                Team name
                <input
                  maxLength={120}
                  onChange={(event) => setName(event.target.value)}
                  required
                  value={name}
                />
              </label>
              <label>
                Parent team
                <select
                  disabled={!allTeamsLoaded}
                  onChange={(event) => setParentTeamId(event.target.value)}
                  value={parentTeamId}
                >
                  {allowTopLevel ? <option value="">Top level</option> : null}
                  {!allowTopLevel && !team.parentTeamId?.value ? (
                    <option disabled value="">
                      Top level (unchanged)
                    </option>
                  ) : null}
                  {parentOptions.map((candidate) => (
                    <option
                      key={candidate.teamId?.value}
                      value={candidate.teamId?.value}
                    >
                      {candidate.name}
                    </option>
                  ))}
                </select>
                {!allTeamsLoaded ? (
                  <span className="field-help">
                    Load all team pages before moving this subtree.
                  </span>
                ) : null}
              </label>
              {formError ? (
                <p className="inline-error" role="alert">
                  {formError}
                </p>
              ) : null}
              <label className="confirmation-check">
                <input
                  checked={deleteConfirmed}
                  onChange={(event) =>
                    setDeleteConfirmed(event.target.checked)
                  }
                  type="checkbox"
                />
                I understand that deleting this team also deletes every
                descendant team and direct membership.
              </label>
              <div className="dialog-actions">
                <button
                  className="button danger"
                  disabled={!online || isPending || !deleteConfirmed}
                  onClick={() => void remove()}
                  type="button"
                >
                  {deleteTeam.isPending ? "Deleting…" : "Delete subtree"}
                </button>
                <button
                  className="button primary"
                  disabled={!online || isPending}
                  type="submit"
                >
                  {updateTeam.isPending || moveTeam.isPending
                    ? "Saving…"
                    : "Save team"}
                </button>
              </div>
              {!online ? <OfflineActionHint /> : null}
            </form>
          ) : null}
          <TeamMembershipManagement
            online={online}
            onPendingChange={setMembershipPending}
            onUpdated={onUpdated}
            team={team}
            transport={transport}
          />
          {team.protectedGeneral && formError ? (
            <p className="inline-error" role="alert">
              {formError}
            </p>
          ) : null}
          <div className="dialog-actions">
            <button
              className="button secondary"
              disabled={isPending}
              onClick={closeDialog}
              type="button"
            >
              Close
            </button>
          </div>
        </Dialog>
      ) : null}
    </>
  );
}

function TeamMembershipManagement({
  online,
  onPendingChange,
  onUpdated,
  team,
  transport,
}: {
  online: boolean;
  onPendingChange: (pending: boolean) => void;
  onUpdated: () => Promise<void>;
  team: Team;
  transport: Transport;
}) {
  const { organization } = useOrganization();
  const [accountId, setAccountId] = useState("");
  const [role, setRole] = useState(TeamRole.MEMBER);
  const [formError, setFormError] = useState("");
  const [message, setMessage] = useState("");
  const setKey = useRef<{ key: string } | undefined>(undefined);
  const removalKeys = useRef(new Map<string, { key: string }>());
  const memberships = useInfiniteQuery(
    TeamService.method.listTeamMemberships,
    {
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
      teamId: team.teamId,
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const members = useInfiniteQuery(
    OrganizationService.method.listOrganizationMembers,
    {
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const setMembership = useMutation(TeamService.method.setTeamMembership, {
    transport,
  });
  const removeMembership = useMutation(
    TeamService.method.removeTeamMembership,
    { transport },
  );
  const directMemberships =
    memberships.data?.pages.flatMap((page) => page.memberships) ?? [];
  const organizationMembers =
    members.data?.pages.flatMap((page) => page.members) ?? [];
  const isPending =
    setMembership.isPending || removeMembership.isPending;

  useEffect(() => {
    onPendingChange(isPending);
  }, [isPending, onPendingChange]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError("");
    setMessage("");
    if (!accountId) {
      setFormError("Choose an organization member.");
      return;
    }
    setKey.current ??= createIdempotencyKey();
    try {
      await setMembership.mutateAsync({
        accountId: uuid(accountId),
        idempotency: setKey.current,
        organizationId: organization.organizationId,
        role,
        teamId: team.teamId,
      });
      await Promise.all([
        memberships.refetch({ throwOnError: true }),
        onUpdated(),
      ]);
      setKey.current = undefined;
      setAccountId("");
      setMessage("Direct team membership updated.");
    } catch (error) {
      setFormError(describeDelibaseError(error));
    }
  };

  const remove = async (membership: TeamMembership) => {
    const targetAccountId = membership.accountId?.value;
    if (!targetAccountId) return;
    setFormError("");
    setMessage("");
    const idempotency =
      removalKeys.current.get(targetAccountId) ?? createIdempotencyKey();
    removalKeys.current.set(targetAccountId, idempotency);
    try {
      await removeMembership.mutateAsync({
        accountId: membership.accountId,
        idempotency,
        organizationId: organization.organizationId,
        teamId: team.teamId,
      });
      await Promise.all([
        memberships.refetch({ throwOnError: true }),
        onUpdated(),
      ]);
      removalKeys.current.delete(targetAccountId);
      setMessage("Direct team membership removed.");
    } catch (error) {
      setFormError(describeDelibaseError(error));
    }
  };

  return (
    <section
      className="team-membership-management"
      aria-labelledby={`team-memberships-${team.teamId?.value}`}
    >
      <h3 id={`team-memberships-${team.teamId?.value}`}>
        Direct team membership
      </h3>
      <p className="muted">
        Parent Team Admin and Member access is inherited downward. Removing a
        direct membership does not remove access inherited from an ancestor.
      </p>
      <form className="team-membership-form" onSubmit={(event) => void submit(event)}>
        <label>
          Organization member
          <select
            onChange={(event) => {
              setKey.current = undefined;
              setAccountId(event.target.value);
            }}
            required
            value={accountId}
          >
            <option value="">Choose a member</option>
            {organizationMembers.map((member) => (
              <option
                key={member.accountId?.value}
                value={member.accountId?.value}
              >
                {member.displayName}
              </option>
            ))}
          </select>
        </label>
        <label>
          Team role
          <select
            onChange={(event) => {
              setKey.current = undefined;
              setRole(Number(event.target.value) as TeamRole);
            }}
            value={role}
          >
            <option value={TeamRole.MEMBER}>Team Member</option>
            <option value={TeamRole.ADMIN}>Team Admin</option>
          </select>
        </label>
        <button
          className="button primary"
          disabled={!online || isPending || !accountId}
          type="submit"
        >
          {setMembership.isPending ? "Updating access…" : "Set team access"}
        </button>
      </form>
      {members.isError || memberships.isError ? (
        <ErrorState
          error={members.error ?? memberships.error}
          onRetry={() => {
            void members.refetch();
            void memberships.refetch();
          }}
          title="Team membership unavailable"
        />
      ) : null}
      {members.hasNextPage ? (
        <button
          className="button secondary compact-button"
          disabled={members.isFetchingNextPage}
          onClick={() => void members.fetchNextPage()}
          type="button"
        >
          {members.isFetchingNextPage
            ? "Loading members…"
            : "Load more organization members"}
        </button>
      ) : null}
      {directMemberships.length ? (
        <ul className="direct-membership-list">
          {directMemberships.map((membership) => (
            <li key={membership.accountId?.value}>
              <span>
                <strong>{membership.displayName}</strong>
                <small>
                  Team{" "}
                  {formatEnumLabel(
                    TeamRole[membership.role] ?? membership.role,
                  )}
                </small>
              </span>
              <button
                className="button danger compact-button"
                disabled={!online || isPending}
                onClick={() => void remove(membership)}
                type="button"
              >
                Remove direct access
              </button>
            </li>
          ))}
        </ul>
      ) : memberships.data ? (
        <p className="muted">No direct memberships are listed.</p>
      ) : memberships.isPending ? (
        <LoadingState label="Loading direct memberships" />
      ) : null}
      {memberships.hasNextPage ? (
        <button
          className="button secondary compact-button"
          disabled={memberships.isFetchingNextPage}
          onClick={() => void memberships.fetchNextPage()}
          type="button"
        >
          {memberships.isFetchingNextPage
            ? "Loading access…"
            : "Load more direct memberships"}
        </button>
      ) : null}
      {formError ? (
        <p className="inline-error" role="alert">
          {formError}
        </p>
      ) : null}
      {message ? (
        <p className="inline-success" role="status">
          {message}
        </p>
      ) : null}
      {!online ? <OfflineActionHint /> : null}
    </section>
  );
}

export function BillingPage() {
  useDocumentMetadata("Billing", "View organization balance and subscription.");
  const { callerRole, organization, transport } = useOrganization();
  const canManageBilling = canManageOrganization(callerRole);
  const summary = useQuery(
    BillingService.method.getBillingSummary,
    { organizationId: organization.organizationId },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );

  return (
    <>
      <OrganizationPageHeading
        description={
          canManageBilling
            ? "Review the shared balance, subscription, billing period, and metered usage policy."
            : "See the shared credit available for app usage in this organization."
        }
        title="Billing"
      />
      {summary.isPending ? <LoadingState label="Loading billing" /> : null}
      {summary.isError ? (
        <ErrorState
          error={summary.error}
          onRetry={() => void summary.refetch()}
          title="Billing unavailable"
        />
      ) : null}
      {summary.data?.summary ? (
        <>
          <div
            className={`stat-grid ${canManageBilling ? "billing-stat-grid" : "member-balance-grid"}`}
          >
            <article>
              <span>Shared available credit</span>
              <strong>
                {formatOptionalUsdMicros(
                  summary.data.summary.availableCredit?.value,
                )}
              </strong>
            </article>
            {canManageBilling ? (
              <>
                <BillingStat
                  label="Held credit"
                  value={formatOptionalUsdMicros(
                    summary.data.summary.heldCredit?.value,
                  )}
                />
                <BillingStat
                  label="Committed overage"
                  value={formatOptionalUsdMicros(
                    summary.data.summary.committedOverage?.value,
                  )}
                />
                <BillingStat
                  label="Held overage"
                  value={formatOptionalUsdMicros(
                    summary.data.summary.heldOverage?.value,
                  )}
                />
              </>
            ) : null}
          </div>
          {canManageBilling ? (
            <BillingAdministration
              onRefreshSummary={() => void summary.refetch()}
              summary={summary.data.summary}
            />
          ) : (
            <section className="content-card privacy-boundary">
              <h2>Your billing access</h2>
              <p>
                This balance is shared by the organization. Your usage page
                includes only your own usage and usage in teams you can
                effectively access.
              </p>
              <p>
                Invoices, the full credit ledger, subscription and payment
                state, checkout, the billing portal, and overage settings are
                available only to organization Owners and Admins.
              </p>
            </section>
          )}
        </>
      ) : null}
    </>
  );
}

function BillingStat({ label, value }: { label: string; value: string }) {
  return (
    <article>
      <span>{label}</span>
      <strong>{value}</strong>
    </article>
  );
}

function BillingAdministration({
  onRefreshSummary,
  summary,
}: {
  onRefreshSummary: () => void;
  summary: BillingSummary;
}) {
  const { organization, transport } = useOrganization();
  const ledger = useInfiniteQuery(
    BillingService.method.listLedgerEntries,
    {
      operation: LedgerOperation.UNSPECIFIED,
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const editableLimit = getEditableOverageLimit(
    summary.overageLimitConfigured,
    summary.monthlyOverageLimit?.value,
  );

  return (
    <>
      <BillingSubscriptionCard summary={summary} />
      <BillingPeriodCard summary={summary} />
      {editableLimit === undefined ? (
        <ErrorState
          error={
            new Error(
              "The configured limit was missing from the billing summary.",
            )
          }
          onRetry={onRefreshSummary}
          title="Overage limit unavailable"
        />
      ) : (
        <OverageLimitControl
          initialLimit={editableLimit}
          onUpdated={onRefreshSummary}
          summary={summary}
        />
      )}
      <BillingPolicy />
      <BillingLedger
        entries={ledger.data?.pages.flatMap((page) => page.entries) ?? []}
        error={ledger.error}
        hasData={Boolean(ledger.data)}
        hasNextPage={ledger.hasNextPage}
        isFetchNextPageError={ledger.isFetchNextPageError}
        isFetchingNextPage={ledger.isFetchingNextPage}
        isPending={ledger.isPending}
        onLoadMore={() => void ledger.fetchNextPage()}
        onRetry={() => void ledger.refetch()}
      />
    </>
  );
}

function subscriptionStateMessage(status: SubscriptionStatus): string {
  switch (status) {
    case SubscriptionStatus.ACTIVE:
      return "The current paid cycle can grant credit and allow configured overage.";
    case SubscriptionStatus.CHECKOUT_PENDING:
      return "A hosted checkout is already open. Finish it in the existing Polar page or wait for it to expire.";
    case SubscriptionStatus.PAST_DUE:
      return "Payment is past due. Existing credit remains usable, but no new credit or overage is available until payment recovers.";
    case SubscriptionStatus.CANCELED:
      return "The subscription is canceled. Existing credit remains usable; new grants and overage are stopped.";
    case SubscriptionStatus.REVOKED:
      return "The subscription is revoked. Existing credit remains usable; new grants and overage are stopped.";
    case SubscriptionStatus.NONE:
      return "There is no subscription. Existing credit, if any, remains usable and overage stays disabled.";
    default:
      return "Subscription state is unavailable. Refresh before changing billing.";
  }
}

function BillingSubscriptionCard({ summary }: { summary: BillingSummary }) {
  const { organization, transport } = useOrganization();
  const online = useOnline();
  const checkout = useMutation(
    BillingService.method.createSubscriptionCheckout,
    { gcTime: 0, transport },
  );
  const portal = useMutation(
    BillingService.method.createBillingPortalSession,
    { gcTime: 0, transport },
  );
  const checkoutKey = useRef<{ key: string } | undefined>(undefined);
  const portalKey = useRef<{ key: string } | undefined>(undefined);
  const [navigationError, setNavigationError] = useState("");
  const canStartCheckout =
    summary.subscriptionStatus === SubscriptionStatus.NONE ||
    summary.subscriptionStatus === SubscriptionStatus.CANCELED ||
    summary.subscriptionStatus === SubscriptionStatus.REVOKED;
  const canOpenPortal =
    summary.subscriptionStatus !== SubscriptionStatus.NONE &&
    summary.subscriptionStatus !== SubscriptionStatus.UNSPECIFIED &&
    summary.subscriptionStatus !== SubscriptionStatus.CHECKOUT_PENDING;

  const openCheckout = () => {
    if (!online) return;
    portal.reset();
    setNavigationError("");
    checkoutKey.current ??= createIdempotencyKey();
    const returnUrl = hostedBillingReturnUrl(window.location.href);
    checkout.mutate(
      {
        cancelUrl: returnUrl,
        idempotency: checkoutKey.current,
        organizationId: organization.organizationId,
        successUrl: returnUrl,
      },
      {
        onSuccess: (data) => {
          if (navigateToPolarHostedPage(data.checkoutUrl)) {
            checkoutKey.current = undefined;
          } else {
            setNavigationError(
              "Checkout did not return a valid Polar-hosted HTTPS page. Retry or contact support.",
            );
          }
        },
      },
    );
  };

  const openPortal = () => {
    if (!online) return;
    checkout.reset();
    setNavigationError("");
    portalKey.current ??= createIdempotencyKey();
    const returnUrl = hostedBillingReturnUrl(window.location.href);
    portal.mutate(
      {
        idempotency: portalKey.current,
        organizationId: organization.organizationId,
        returnUrl,
      },
      {
        onSuccess: (data) => {
          portalKey.current = undefined;
          if (!navigateToPolarHostedPage(data.portalUrl)) {
            setNavigationError(
              "The portal did not return a valid Polar-hosted HTTPS page. Retry or contact support.",
            );
          }
        },
      },
    );
  };
  const requestError = checkout.error ?? portal.error;

  return (
    <section className="content-card billing-plan">
      <div className="billing-plan-copy">
        <span className="eyebrow">Monthly plan</span>
        <h2>
          {formatEnumLabel(
            SubscriptionStatus[summary.subscriptionStatus] ??
              summary.subscriptionStatus,
          )}
        </h2>
        <p>
          <strong>$10 monthly</strong> grants exactly $10.00
          (10,000,000 USD micro-units) after each successful payment. Unused
          credit rolls over without expiration.
        </p>
        <p className="billing-state-message">
          {subscriptionStateMessage(summary.subscriptionStatus)}
        </p>
      </div>
      <div className="billing-actions">
        <div className="button-row">
          {canStartCheckout ? (
            <button
              className="button primary"
              disabled={!online || checkout.isPending || portal.isPending}
              onClick={openCheckout}
              type="button"
            >
              {checkout.isPending ? "Opening Polar…" : "Start subscription"}
            </button>
          ) : null}
          {canOpenPortal ? (
            <button
              className="button secondary"
              disabled={!online || checkout.isPending || portal.isPending}
              onClick={openPortal}
              type="button"
            >
              {portal.isPending
                ? "Opening Polar…"
                : "Invoices and payment"}
            </button>
          ) : null}
        </div>
        <small>
          Checkout, card collection, invoices, receipts, cancellation, and
          payment recovery stay on Polar-hosted pages. DeliDev never handles
          card details.
        </small>
        {requestError || navigationError ? (
          <p className="inline-error" role="alert">
            {navigationError || describeDelibaseError(requestError)}
          </p>
        ) : null}
        {!online ? <OfflineActionHint /> : null}
      </div>
    </section>
  );
}

type TimestampValue = {
  nanos: number;
  seconds: bigint;
};

function formatTimestamp(
  value: TimestampValue | undefined,
  options: Intl.DateTimeFormatOptions,
): string {
  if (!value) return "Unavailable";
  return new Date(
    Number(value.seconds) * 1000 + value.nanos / 1_000_000,
  ).toLocaleString("en-US", options);
}

function BillingPeriodCard({ summary }: { summary: BillingSummary }) {
  const period = summary.currentPeriod;
  const overageUsed =
    summary.committedOverage && summary.heldOverage
      ? summary.committedOverage.value + summary.heldOverage.value
      : undefined;
  return (
    <section
      aria-labelledby="billing-period-heading"
      className="content-card billing-period-card"
    >
      <div>
        <span className="eyebrow">Billing period</span>
        <h2 id="billing-period-heading">
          {period
            ? formatEnumLabel(
                BillingPeriodStatus[period.status] ?? period.status,
              )
            : "No open period"}
        </h2>
        <p>
          {period
            ? `${formatTimestamp(period.startsAt, { dateStyle: "medium" })} – ${formatTimestamp(period.endsAt, { dateStyle: "medium" })}`
            : "Overage requires a current active Polar billing period. Rollover credit can still be used without one."}
        </p>
      </div>
      <dl className="billing-period-values">
        <div>
          <dt>Committed + held overage</dt>
          <dd>{formatOptionalUsdMicros(overageUsed)}</dd>
        </div>
        <div>
          <dt>Monthly limit</dt>
          <dd>
            {formatOverageLimit(
              summary.overageLimitConfigured,
              summary.monthlyOverageLimit?.value,
            )}
          </dd>
        </div>
        <div>
          <dt>New overage</dt>
          <dd>{summary.newOverageAllowed ? "Allowed" : "Blocked"}</dd>
        </div>
      </dl>
    </section>
  );
}

function OverageLimitControl({
  initialLimit,
  onUpdated,
  summary,
}: {
  initialLimit: bigint;
  onUpdated: () => void;
  summary: BillingSummary;
}) {
  const [dialogOpen, setDialogOpen] = useState(false);
  const [message, setMessage] = useState("");
  const used =
    (summary.committedOverage?.value ?? 0n) +
    (summary.heldOverage?.value ?? 0n);
  return (
    <section
      aria-labelledby="overage-limit-title"
      className="content-card overage-limit-card"
    >
      <div>
        <span className="eyebrow">Metered usage</span>
        <h2 id="overage-limit-title">Monthly overage limit</h2>
        <p>
          Current limit:{" "}
          {formatOverageLimit(
            summary.overageLimitConfigured,
            summary.monthlyOverageLimit?.value,
          )}; {formatUsdMicros(used)} committed or held this period.
        </p>
        <p className="muted">
          Overage defaults to zero until explicitly set. Lowering the limit
          below committed and held overage never reverses usage; it blocks new
          reservations until usage is below the limit or a new period begins.
        </p>
      </div>
      <button
        className="button secondary"
        onClick={() => {
          setMessage("");
          setDialogOpen(true);
        }}
        type="button"
      >
        Change monthly limit
      </button>
      {message ? (
        <p className="inline-success" role="status">
          {message}
        </p>
      ) : null}
      {dialogOpen ? (
        <OverageLimitDialog
          initialLimit={initialLimit}
          onClose={() => setDialogOpen(false)}
          onUpdated={() => {
            setMessage("Monthly overage limit updated.");
            setDialogOpen(false);
            onUpdated();
          }}
        />
      ) : null}
    </section>
  );
}

function OverageLimitDialog({
  initialLimit,
  onClose,
  onUpdated,
}: {
  initialLimit: bigint;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const { organization, transport } = useOrganization();
  const online = useOnline();
  const [monthlyLimit, setMonthlyLimit] = useState(() =>
    formatUsdMicrosInput(initialLimit),
  );
  const [formError, setFormError] = useState("");
  const pendingKey = useRef<
    { input: bigint; key: { key: string } } | undefined
  >(undefined);
  const update = useMutation(BillingService.method.updateOverageLimit, {
    gcTime: 0,
    transport,
  });
  const close = () => {
    if (update.isPending) return;
    onClose();
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!online) return;
    setFormError("");
    const micros = parseUsdMicros(monthlyLimit.trim());
    if (micros === undefined) {
      setFormError(
        "Enter a non-negative USD amount with up to six decimals.",
      );
      return;
    }
    if (pendingKey.current?.input !== micros) {
      pendingKey.current = { input: micros, key: createIdempotencyKey() };
    }
    update.mutate(
      {
        idempotency: pendingKey.current.key,
        monthlyLimit: { value: micros },
        organizationId: organization.organizationId,
      },
      {
        onError: (error) => setFormError(describeDelibaseError(error)),
        onSuccess: () => {
          pendingKey.current = undefined;
          onUpdated();
        },
      },
    );
  };

  return (
    <Dialog
      descriptionId="overage-limit-description"
      onClose={close}
      titleId="overage-limit-dialog-title"
    >
      <h2 id="overage-limit-dialog-title">Change monthly overage limit</h2>
      <p id="overage-limit-description">
        Enter the maximum overage allowed in each Polar billing period. Set
        zero to block all new overage.
      </p>
      <form className="overage-dialog-form" onSubmit={submit}>
        <label htmlFor="monthly-overage-limit">
          Limit in USD
          <input
            aria-describedby={
              formError
                ? "monthly-overage-help monthly-overage-error"
                : "monthly-overage-help"
            }
            aria-invalid={Boolean(formError)}
            // Dialog focuses this critical input after capturing the opener,
            // preserving focus restoration when the modal closes.
            data-dialog-autofocus
            id="monthly-overage-limit"
            inputMode="decimal"
            min="0"
            onChange={(event) => {
              setMonthlyLimit(event.target.value);
              setFormError("");
            }}
            required
            step="0.000001"
            type="number"
            value={monthlyLimit}
          />
        </label>
        <small className="field-hint" id="monthly-overage-help">
          Up to six decimal places; stored exactly as USD micro-units.
        </small>
        {formError ? (
          <p
            className="inline-error"
            id="monthly-overage-error"
            role="alert"
          >
            {formError}
          </p>
        ) : null}
        {!online ? <OfflineActionHint /> : null}
        <div className="dialog-actions">
          <button
            className="button secondary"
            disabled={update.isPending}
            onClick={close}
            type="button"
          >
            Cancel
          </button>
          <button
            className="button primary"
            disabled={!online || update.isPending}
            type="submit"
          >
            {update.isPending ? "Updating…" : "Update limit"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

function BillingPolicy() {
  return (
    <section
      aria-labelledby="billing-policy-title"
      className="content-card billing-policy"
    >
      <span className="eyebrow">How billing behaves</span>
      <h2 id="billing-policy-title">Credits, overage, and reliability</h2>
      <ul className="policy-grid">
        <li>
          <strong>Refunds and chargebacks</strong>
          <span>
            The matching net credit grant is reversed. Credit already consumed
            becomes overage in the applicable period, counts against its
            limit, and can block new reservations.
          </span>
        </li>
        <li>
          <strong>Cancellation or past due</strong>
          <span>
            Existing credit stays usable. New credit grants and all new
            overage stop until billing returns to active.
          </span>
        </li>
        <li>
          <strong>Polar outage</strong>
          <span>
            Delibase continues authorizing within local credit and configured
            overage, then queues overage usage for eventual Polar delivery.
            Checkout creation fails without changing local billing.
          </span>
        </li>
        <li>
          <strong>Reservations</strong>
          <span>
            Apps reserve a maximum amount server-to-server. Expired or
            released holds return to the shared balance; late commits and
            commits above reserved units fail with stable error reasons.
          </span>
        </li>
      </ul>
      <p className="muted">
        DeliDev only displays delibase results and billing settings. Usage
        charging is never initiated in the browser.
      </p>
    </section>
  );
}

function BillingLedger({
  entries,
  error,
  hasData,
  hasNextPage,
  isFetchNextPageError,
  isFetchingNextPage,
  isPending,
  onLoadMore,
  onRetry,
}: {
  entries: LedgerEntry[];
  error: Error | null;
  hasData: boolean;
  hasNextPage: boolean;
  isFetchNextPageError: boolean;
  isFetchingNextPage: boolean;
  isPending: boolean;
  onLoadMore: () => void;
  onRetry: () => void;
}) {
  return (
    <section className="billing-ledger" aria-labelledby="billing-ledger-title">
      <div className="card-heading">
        <div>
          <span className="eyebrow">Audit trail</span>
          <h2 id="billing-ledger-title">Credit ledger</h2>
        </div>
      </div>
      {isPending ? <LoadingState label="Loading ledger" /> : null}
      {error && !hasData ? (
        <ErrorState
          error={error}
          onRetry={onRetry}
          title="Ledger unavailable"
        />
      ) : null}
      {entries.length === 0 && hasData ? (
        <EmptyState
          description="Credit grants, holds, charges, and releases will appear here."
          title="No ledger entries yet"
        />
      ) : null}
      {entries.length ? (
        <>
          <div className="table-card">
            <table>
              <caption className="sr-only">Organization credit ledger</caption>
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col">Operation</th>
                  <th scope="col">Amount</th>
                  <th scope="col">Balance after</th>
                  <th scope="col">Team</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.ledgerEntryId?.value}>
                    <td>
                      {entry.createdAt
                        ? new Date(
                            Number(entry.createdAt.seconds) * 1000 +
                              entry.createdAt.nanos / 1_000_000,
                          ).toLocaleString("en-US", {
                            dateStyle: "medium",
                            timeStyle: "short",
                          })
                        : "—"}
                    </td>
                    <td>
                      {formatEnumLabel(
                        LedgerOperation[entry.operation] ?? entry.operation,
                      )}
                    </td>
                    <td>
                      {formatOptionalUsdMicros(entry.amount?.value)}
                    </td>
                    <td>
                      {formatOptionalUsdMicros(entry.balanceAfter?.value)}
                    </td>
                    <td>{entry.teamNameSnapshot || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {isFetchNextPageError && error ? (
            <p className="inline-error" role="alert">
              {describeDelibaseError(error)}
            </p>
          ) : null}
          {hasNextPage ? (
            <div className="pagination-actions">
              <button
                className="button secondary"
                disabled={isFetchingNextPage}
                onClick={onLoadMore}
                type="button"
              >
                {isFetchingNextPage
                  ? "Loading more…"
                  : "Load more ledger entries"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </section>
  );
}

export function UsagePage() {
  useDocumentMetadata("Usage", "View organization usage records.");
  const { callerRole, organization, transport } = useOrganization();
  const { accountState } = useAccountState();
  const canViewOrganizationUsage = canManageOrganization(callerRole);
  const summary = useQuery(
    BillingService.method.getBillingSummary,
    { organizationId: organization.organizationId },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );
  const usage = useInfiniteQuery(
    BillingService.method.listUsageRecords,
    {
      organizationId: uuid(organization.organizationId?.value),
      page: { cursor: "", pageSize: 100 },
    },
    {
      gcTime: 0,
      getNextPageParam: (lastPage) => {
        const cursor = lastPage.page?.nextCursor;
        return cursor ? { cursor, pageSize: 100 } : undefined;
      },
      pageParamKey: "page",
      retry: false,
      staleTime: 0,
      transport,
    },
  );
  const usageRows =
    usage.data?.pages.flatMap((page) => page.records) ?? [];
  return (
    <>
      <OrganizationPageHeading
        description={
          canViewOrganizationUsage
            ? "Review organization-wide settled usage and its team, actor, service, meter, credit, and overage attribution."
            : "Review your usage and usage in teams you can effectively access."
        }
        title="Usage"
      />
      {summary.isPending ? <LoadingState label="Loading shared balance" /> : null}
      {summary.isError ? (
        <ErrorState
          error={summary.error}
          onRetry={() => void summary.refetch()}
          title="Balance unavailable"
        />
      ) : null}
      {summary.data?.summary ? (
        <>
          <div className="stat-grid usage-summary-grid">
            <BillingStat
              label="Shared available credit"
              value={formatOptionalUsdMicros(
                summary.data.summary.availableCredit?.value,
              )}
            />
            {canViewOrganizationUsage ? (
              <>
                <BillingStat
                  label="Committed overage"
                  value={formatOptionalUsdMicros(
                    summary.data.summary.committedOverage?.value,
                  )}
                />
                <BillingStat
                  label="Held overage"
                  value={formatOptionalUsdMicros(
                    summary.data.summary.heldOverage?.value,
                  )}
                />
              </>
            ) : null}
          </div>
          {canViewOrganizationUsage ? (
            <UsagePeriodContext summary={summary.data.summary} />
          ) : (
            <p className="muted usage-visibility-note">
              Delibase applies your personal and effective-team visibility on
              every page of results. Other teams and organization-wide billing
              data are not returned to Members.
            </p>
          )}
        </>
      ) : null}
      {usage.isPending ? <LoadingState label="Loading usage" /> : null}
      {usage.isError && !usage.data ? (
        <ErrorState
          error={usage.error}
          onRetry={() => void usage.refetch()}
          title="Usage unavailable"
        />
      ) : null}
      {usageRows.length === 0 && usage.data ? (
        <EmptyState
          description="Usage will appear here after a mini-app service settles it."
          title="No usage yet"
        />
      ) : null}
      {usageRows.length ? (
        <>
          {usageRows.some(
            (record) =>
              record.status === UsageRecordStatus.POLAR_PENDING,
          ) ? (
            <p className="outage-state" role="status">
              Some overage usage is queued for Polar. Local balances and limits
              remain authoritative while delivery is retried; no usage is lost
              or charged from this browser.
            </p>
          ) : null}
          <div className="table-card">
            <table>
              <caption className="sr-only">Usage records</caption>
              <thead>
                <tr>
                  <th scope="col">Committed</th>
                  <th scope="col">Team</th>
                  <th scope="col">Attribution</th>
                  <th scope="col">Meter</th>
                  <th scope="col">Units</th>
                  <th scope="col">Unit price</th>
                  <th scope="col">Total</th>
                  <th scope="col">Credit</th>
                  <th scope="col">Overage</th>
                  <th scope="col">Status</th>
                  <th scope="col">Reference</th>
                </tr>
              </thead>
              <tbody>
                {usageRows.map((record) => (
                  <tr key={record.usageRecordId?.value}>
                    <td>
                      {formatTimestamp(record.committedAt, {
                        dateStyle: "medium",
                        timeStyle: "short",
                      })}
                    </td>
                    <td>{record.teamNameSnapshot}</td>
                    <td>
                      <UsageAttribution
                        callerAccountId={
                          accountState.account?.accountId?.value ?? ""
                        }
                        record={record}
                      />
                    </td>
                    <td>
                      <code>{shortIdentifier(record.meterId?.value)}</code>
                    </td>
                    <td>{formatUsageUnits(record.units?.value)}</td>
                    <td>
                      {formatUsageCost(record.usdMicrosPerUnit?.value)}
                    </td>
                    <td>{formatUsageCost(record.totalCost?.value)}</td>
                    <td>{formatUsageCost(record.creditApplied?.value)}</td>
                    <td>{formatUsageCost(record.overageApplied?.value)}</td>
                    <td>{formatUsageStatus(record.status)}</td>
                    <td>{record.clientReference || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {usage.isFetchNextPageError ? (
            <p className="inline-error" role="alert">
              {describeDelibaseError(usage.error)}
            </p>
          ) : null}
          {usage.hasNextPage ? (
            <div className="pagination-actions">
              <button
                className="button secondary"
                disabled={usage.isFetchingNextPage}
                onClick={() => void usage.fetchNextPage()}
                type="button"
              >
                {usage.isFetchingNextPage
                  ? "Loading more…"
                  : "Load more usage"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
      <section className="content-card usage-reservation-note">
        <h2>How usage reaches this page</h2>
        <p>
          Mini-app services reserve, commit, or release usage through
          authenticated server-to-server requests. Reservations hold shared
          credit and overage atomically, expire at the catalog-defined time,
          and reject late or above-reservation commits with canonical stable
          error details. This page is read-only and never performs charging.
        </p>
      </section>
    </>
  );
}

function UsagePeriodContext({ summary }: { summary: BillingSummary }) {
  const period = summary.currentPeriod;
  return (
    <section
      aria-labelledby="usage-period-title"
      className="content-card usage-period-context"
    >
      <div>
        <span className="eyebrow">Current billing period</span>
        <h2 id="usage-period-title">
          {period
            ? `${formatTimestamp(period.startsAt, { dateStyle: "medium" })} – ${formatTimestamp(period.endsAt, { dateStyle: "medium" })}`
            : "No open period"}
        </h2>
      </div>
      <p>
        Usage history below is paginated across periods. Held and committed
        overage count only against the current Polar period; rollover credit
        remains available across period boundaries.
      </p>
    </section>
  );
}

function shortIdentifier(value: string | undefined): string {
  if (!value) return "Unavailable";
  return value.length > 12 ? `${value.slice(0, 8)}…` : value;
}

function UsageAttribution({
  callerAccountId,
  record,
}: {
  callerAccountId: string;
  record: UsageRecord;
}) {
  const accountId = record.userAccountId?.value;
  const serviceId = record.serviceIdentityId?.value;
  return (
    <span className="usage-attribution">
      <span>
        {accountId === callerAccountId
          ? "You"
          : `Account ${shortIdentifier(accountId)}`}
      </span>
      <small>Service {shortIdentifier(serviceId)}</small>
    </span>
  );
}

function formatUsageStatus(value: UsageRecordStatus): string {
  switch (value) {
    case UsageRecordStatus.POLAR_PENDING:
      return "Queued for Polar";
    case UsageRecordStatus.POLAR_REPORTED:
      return "Reported to Polar";
    case UsageRecordStatus.COMMITTED:
      return "Committed";
    default:
      return "Unavailable";
  }
}

export function formatUsageUnits(value: bigint | undefined): string {
  return value === undefined ? "Unavailable" : value.toString();
}

export function formatUsageCost(value: bigint | undefined): string {
  return value === undefined ? "Unavailable" : formatUsdMicros(value);
}

export function OrganizationSettingsPage() {
  useDocumentMetadata("Organization settings", "Update organization settings.");
  const {
    callerRole,
    organization,
    refreshOrganization,
    transport,
  } = useOrganization();
  const { refreshAccountState } = useAccountState();
  const navigate = useNavigate();
  const online = useOnline();
  const [name, setName] = useState(organization.name);
  const [slug, setSlug] = useState(organization.slug);
  const [message, setMessage] = useState("");
  const [formError, setFormError] = useState("");
  const updateSlugKey = useRef<{ key: string } | undefined>(undefined);
  const updateName = useMutation(
    OrganizationService.method.updateOrganization,
    { transport },
  );
  const updateSlug = useMutation(
    OrganizationService.method.updateOrganizationSlug,
    { transport },
  );

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMessage("");
    setFormError("");
    const normalizedName = name.trim();
    const normalizedSlug = slug.trim().toLowerCase();
    if (!normalizedName) {
      setFormError("Enter an organization name.");
      return;
    }
    if (!slugPattern.test(normalizedSlug)) {
      setFormError(
        "Use lowercase letters, numbers, and single hyphens for the slug.",
      );
      return;
    }

    let nameUpdated = false;
    try {
      if (normalizedName !== organization.name) {
        await updateName.mutateAsync({
          idempotency: createIdempotencyKey(),
          name: normalizedName,
          organizationId: uuid(organization.organizationId?.value),
        });
        nameUpdated = true;
      }
      if (normalizedSlug !== organization.slug) {
        updateSlugKey.current ??= createIdempotencyKey();
        const response = await updateSlug.mutateAsync({
          idempotency: updateSlugKey.current,
          organizationId: uuid(organization.organizationId?.value),
          slug: normalizedSlug,
        });
        await refreshAccountState();
        navigate(
          `/o/${response.organization?.slug ?? normalizedSlug}/settings`,
          { replace: true },
        );
        updateSlugKey.current = undefined;
        return;
      }
      if (normalizedName !== organization.name) {
        await Promise.all([
          refreshAccountState(),
          refreshOrganization(),
        ]);
      }
      setMessage(
        normalizedName === organization.name
          ? "No organization changes to save."
          : "Organization settings updated.",
      );
    } catch (error) {
      const mutationError = describeDelibaseError(error);
      if (nameUpdated) {
        try {
          await Promise.all([
            refreshAccountState(),
            refreshOrganization(),
          ]);
        } catch {
          setFormError(
            `${mutationError} The organization name was saved, but current organization data could not be refreshed.`,
          );
          return;
        }
      }
      setFormError(mutationError);
    }
  };
  const isPending = updateName.isPending || updateSlug.isPending;
  const canManage = canManageOrganization(callerRole);

  return (
    <>
      <OrganizationPageHeading
        description="Owners and admins can update organization details."
        title="Settings"
      />
      {canManage ? (
        <form className="form-card" onSubmit={submit}>
          <label>
            Organization name
            <input
              autoComplete="organization"
              maxLength={120}
              onChange={(event) => setName(event.target.value)}
              required
              value={name}
            />
          </label>
          <label>
            Organization URL
            <span className="slug-input">
              <span aria-hidden="true">deli.dev/o/</span>
              <input
                aria-describedby="organization-slug-help"
                autoCapitalize="none"
                autoComplete="off"
                maxLength={63}
                onChange={(event) => {
                  updateSlugKey.current = undefined;
                  setSlug(event.target.value);
                }}
                pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
                required
                spellCheck={false}
                value={slug}
              />
            </span>
            <span className="field-hint" id="organization-slug-help">
              Old links continue to redirect after a slug change.
            </span>
          </label>
          {formError ? (
            <p className="inline-error" role="alert">
              {formError}
            </p>
          ) : null}
          {message ? (
            <p className="inline-success" role="status">
              {message}
            </p>
          ) : null}
          <button
            className="button primary"
            disabled={!online || isPending || !name.trim() || !slug.trim()}
            type="submit"
          >
            {isPending ? "Saving…" : "Save changes"}
          </button>
          {!online ? <OfflineActionHint /> : null}
        </form>
      ) : (
        <p className="muted">
          An organization owner or admin can update organization details.
        </p>
      )}
      <OrganizationLifecycleActions
        callerRole={callerRole}
        onOrganizationRemoved={async () => {
          await refreshAccountState().catch(() => undefined);
          navigate("/account", { replace: true });
        }}
        organization={organization}
        transport={transport}
      />
    </>
  );
}

type OrganizationLifecycleDialog = "delete" | "leave";

function OrganizationLifecycleActions({
  callerRole,
  onOrganizationRemoved,
  organization,
  transport,
}: {
  callerRole: OrganizationRole;
  onOrganizationRemoved: () => Promise<void>;
  organization: Organization;
  transport: Transport;
}) {
  const online = useOnline();
  const [dialog, setDialog] = useState<
    OrganizationLifecycleDialog | undefined
  >(undefined);
  const [confirmation, setConfirmation] = useState("");
  const [formError, setFormError] = useState("");
  const leaveKey = useRef<{ key: string } | undefined>(undefined);
  const deleteKey = useRef<{ key: string } | undefined>(undefined);
  const leave = useMutation(OrganizationService.method.leaveOrganization, {
    transport,
  });
  const remove = useMutation(OrganizationService.method.deleteOrganization, {
    transport,
  });
  const isPending = leave.isPending || remove.isPending;
  const close = () => {
    if (isPending) return;
    leaveKey.current = undefined;
    deleteKey.current = undefined;
    setDialog(undefined);
    setConfirmation("");
    setFormError("");
  };
  const leaveOrganization = async () => {
    setFormError("");
    leaveKey.current ??= createIdempotencyKey();
    try {
      await leave.mutateAsync({
        idempotency: leaveKey.current,
        organizationId: organization.organizationId,
      });
      leaveKey.current = undefined;
      await onOrganizationRemoved();
    } catch (error) {
      setFormError(describeDelibaseError(error));
    }
  };
  const deleteOrganization = async () => {
    setFormError("");
    if (confirmation !== organization.name) {
      setFormError(`Enter ${organization.name} to confirm deletion.`);
      return;
    }
    deleteKey.current ??= createIdempotencyKey();
    try {
      await remove.mutateAsync({
        confirm: true,
        idempotency: deleteKey.current,
        organizationId: organization.organizationId,
      });
      deleteKey.current = undefined;
      await onOrganizationRemoved();
    } catch (error) {
      setFormError(describeDelibaseError(error));
    }
  };

  return (
    <section className="content-card danger-zone">
      <div>
        <h2>Organization access</h2>
        <p>
          Leaving is blocked for the last Owner and while you have active
          usage reservations.
        </p>
      </div>
      <div className="button-row">
        <button
          className="button secondary"
          disabled={!online}
          onClick={() => setDialog("leave")}
          type="button"
        >
          Leave organization
        </button>
        {callerRole === OrganizationRole.OWNER ? (
          <button
            className="button danger"
            disabled={!online}
            onClick={() => setDialog("delete")}
            type="button"
          >
            Delete organization
          </button>
        ) : null}
      </div>
      {callerRole === OrganizationRole.ADMIN ? (
        <p className="field-hint">
          Admins can update settings but cannot delete the organization.
        </p>
      ) : null}
      {!online ? <OfflineActionHint /> : null}
      {dialog === "leave" ? (
        <Dialog
          descriptionId="leave-organization-description"
          onClose={close}
          titleId="leave-organization-title"
        >
          <h2 id="leave-organization-title">
            Leave {organization.name}?
          </h2>
          <p id="leave-organization-description">
            Your organization and direct team memberships will be removed.
            Access inherited through this organization ends immediately.
          </p>
          {formError ? (
            <p className="inline-error" role="alert">
              {formError}
            </p>
          ) : null}
          <div className="dialog-actions">
            <button
              className="button secondary"
              disabled={isPending}
              onClick={close}
              type="button"
            >
              Keep membership
            </button>
            <button
              className="button danger"
              disabled={!online || isPending}
              onClick={() => void leaveOrganization()}
              type="button"
            >
              {leave.isPending ? "Leaving…" : "Leave organization"}
            </button>
          </div>
        </Dialog>
      ) : null}
      {dialog === "delete" ? (
        <Dialog
          descriptionId="delete-organization-description"
          onClose={close}
          titleId="delete-organization-title"
        >
          <h2 id="delete-organization-title">
            Delete {organization.name}?
          </h2>
          <p id="delete-organization-description">
            This immediately removes operational access, forfeits remaining
            credits, and queues subscription cancellation. Active usage
            reservations block deletion.
          </p>
          <label>
            Enter {organization.name} to confirm
            <input
              autoComplete="off"
              onChange={(event) => setConfirmation(event.target.value)}
              value={confirmation}
            />
          </label>
          {formError ? (
            <p className="inline-error" role="alert">
              {formError}
            </p>
          ) : null}
          <div className="dialog-actions">
            <button
              className="button secondary"
              disabled={isPending}
              onClick={close}
              type="button"
            >
              Keep organization
            </button>
            <button
              className="button danger"
              disabled={
                !online ||
                isPending ||
                confirmation !== organization.name
              }
              onClick={() => void deleteOrganization()}
              type="button"
            >
              {remove.isPending
                ? "Deleting organization…"
                : "Delete organization"}
            </button>
          </div>
        </Dialog>
      ) : null}
    </section>
  );
}
