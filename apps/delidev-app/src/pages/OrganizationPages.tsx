import {
  useInfiniteQuery,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query";
import { createClient, type Transport } from "@connectrpc/connect";
import {
  BillingService,
  CatalogService,
  InvitationStatus,
  LedgerOperation,
  OrganizationRole,
  OrganizationService,
  SubscriptionStatus,
  TeamService,
  TeamRole,
  type LedgerEntry,
  type Organization,
  type OrganizationInvitation,
  type Team,
} from "@delinoio/delibase-connect";
import {
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type FormEvent,
} from "react";
import { useNavigate } from "react-router-dom";

import { usePublicTransport } from "../api/ApiContext";
import { CatalogCard } from "../components/CatalogCard";
import { Dialog } from "../components/Dialog";
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
  const { callerRole, organization, transport } = useOrganization();
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
        error:
          error instanceof Error
            ? error.message
            : "The invitation could not be created.",
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
      setRevokeError(
        error instanceof Error
          ? error.message
          : "The invitation could not be revoked.",
      );
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
  const loadedTeamRows =
    teams.data?.pages.flatMap((page) => page.teams) ?? [];
  const teamRows = loadedTeamRows.filter(
    (team) => !deletedTeamIds.has(team.teamId?.value ?? ""),
  );
  const canManage = canManageOrganization(callerRole);
  const refreshTeams = async () => {
    await teams.refetch();
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
      {canManage ? (
        <CreateTeamForm
          onUpdated={refreshTeams}
          online={online}
          teams={teamRows}
        />
      ) : null}
      {teams.isPending ? <LoadingState label="Loading teams" /> : null}
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
                </div>
                {canManage && !team.protectedGeneral ? (
                  <TeamActions
                    allTeamsLoaded={!teams.hasNextPage}
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
    </>
  );
}

function CreateTeamForm({
  online,
  onUpdated,
  teams,
}: {
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
        onError: (error) => setFormError(error.message),
        onSuccess: () => {
          idempotencyKey.current = undefined;
          setName("");
          setParentTeamId("");
          setMessage("Team created.");
          void onUpdated();
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
            <option value="">Top level</option>
            {teams.filter(canCreateChildTeam).map((team) => (
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
        disabled={!online || createTeam.isPending}
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
  allTeamsLoaded,
  online,
  onDeleted,
  onUpdated,
  team,
  teams,
}: {
  allTeamsLoaded: boolean;
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
  const [formError, setFormError] = useState("");
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
    updateTeam.isPending || moveTeam.isPending || deleteTeam.isPending;
  const parentOptions = teams.filter((candidate) =>
    canUseTeamAsParent(team, candidate, teams),
  );

  const showDialog = () => {
    deletionKey.current = undefined;
    setName(team.name);
    setParentTeamId(team.parentTeamId?.value ?? "");
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
        await onUpdated();
      }
      setFormError(
        error instanceof Error ? error.message : "The team could not be updated.",
      );
    }
  };
  const remove = async () => {
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
      void onUpdated();
    } catch (error) {
      setFormError(
        error instanceof Error ? error.message : "The team could not be deleted.",
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
        Manage
      </button>
      {open ? (
        <Dialog
          descriptionId={descriptionId}
          onClose={closeDialog}
          titleId={titleId}
        >
          <h2 id={titleId}>Manage {team.name}</h2>
          <p id={descriptionId}>
            Rename this team, move its subtree, or permanently delete the
            subtree.
          </p>
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
                <option value="">Top level</option>
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
            <div className="dialog-actions">
              <button
                className="button danger"
                disabled={!online || isPending}
                onClick={() => void remove()}
                type="button"
              >
                {deleteTeam.isPending ? "Deleting…" : "Delete subtree"}
              </button>
              <button
                className="button secondary"
                disabled={isPending}
                onClick={closeDialog}
                type="button"
              >
                Cancel
              </button>
              <button
                className="button primary"
                disabled={!online || isPending}
                type="submit"
              >
                {updateTeam.isPending || moveTeam.isPending
                  ? "Saving…"
                  : "Save changes"}
              </button>
            </div>
            {!online ? <OfflineActionHint /> : null}
          </form>
        </Dialog>
      ) : null}
    </>
  );
}

export function BillingPage() {
  useDocumentMetadata("Billing", "View organization balance and subscription.");
  const { callerRole, organization, transport } = useOrganization();
  const online = useOnline();
  const showBillingActions = canManageOrganization(callerRole);
  const summary = useQuery(
    BillingService.method.getBillingSummary,
    { organizationId: organization.organizationId },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );
  const ledger = useInfiniteQuery(
    BillingService.method.listLedgerEntries,
    {
      operation: LedgerOperation.UNSPECIFIED,
      organizationId: organization.organizationId,
      page: { cursor: "", pageSize: 100 },
    },
    {
      enabled: showBillingActions,
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
  const checkout = useMutation(
    BillingService.method.createSubscriptionCheckout,
    { transport },
  );
  const portal = useMutation(
    BillingService.method.createBillingPortalSession,
    { transport },
  );
  const checkoutIdempotencyKey = useRef<
    { key: string } | undefined
  >(undefined);
  const editableOverageLimit = summary.data?.summary
    ? getEditableOverageLimit(
        summary.data.summary.overageLimitConfigured,
        summary.data.summary.monthlyOverageLimit?.value,
      )
    : undefined;

  const openCheckout = () => {
    checkoutIdempotencyKey.current ??= createIdempotencyKey();
    checkout.mutate(
      {
        cancelUrl: window.location.href,
        idempotency: checkoutIdempotencyKey.current,
        organizationId: organization.organizationId,
        successUrl: window.location.href,
      },
      {
        onSuccess: (data) => {
          if (data.checkoutUrl) window.location.assign(data.checkoutUrl);
        },
      },
    );
  };
  const openPortal = () => {
    portal.mutate(
      {
        idempotency: createIdempotencyKey(),
        organizationId: organization.organizationId,
        returnUrl: window.location.href,
      },
      {
        onSuccess: (data) => {
          if (data.portalUrl) window.location.assign(data.portalUrl);
        },
      },
    );
  };

  return (
    <>
      <OrganizationPageHeading
        description="Credits roll forward. New overage is off until an owner or admin sets a limit."
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
          <div className="stat-grid">
            <article>
              <span>Available credit</span>
              <strong>
                {formatOptionalUsdMicros(
                  summary.data.summary.availableCredit?.value,
                )}
              </strong>
            </article>
            <article>
              <span>Held credit</span>
              <strong>
                {formatOptionalUsdMicros(
                  summary.data.summary.heldCredit?.value,
                )}
              </strong>
            </article>
            <article>
              <span>Monthly overage limit</span>
              <strong>
                {formatOverageLimit(
                  summary.data.summary.overageLimitConfigured,
                  summary.data.summary.monthlyOverageLimit?.value,
                )}
              </strong>
            </article>
          </div>
          {showBillingActions ? (
            <>
              <section className="content-card billing-plan">
                <div>
                  <span className="eyebrow">Monthly plan</span>
                  <h2>
                    {formatEnumLabel(
                      SubscriptionStatus[
                        summary.data.summary.subscriptionStatus
                      ] ?? summary.data.summary.subscriptionStatus,
                    )}
                  </h2>
                  <p>$10 monthly includes $10 of credits that never expire.</p>
                </div>
                <div className="button-row">
                  <button
                    className="button primary"
                    disabled={!online || checkout.isPending}
                    onClick={openCheckout}
                    type="button"
                  >
                    Start subscription
                  </button>
                  <button
                    className="button secondary"
                    disabled={!online || portal.isPending}
                    onClick={openPortal}
                    type="button"
                  >
                    Manage billing
                  </button>
                </div>
                {checkout.error || portal.error ? (
                  <p className="inline-error" role="alert">
                    {(checkout.error ?? portal.error)?.message}
                  </p>
                ) : null}
                {!online ? <OfflineActionHint /> : null}
              </section>
              {editableOverageLimit === undefined ? (
                <ErrorState
                  error={
                    new Error(
                      "The configured limit was missing from the billing summary.",
                    )
                  }
                  onRetry={() => void summary.refetch()}
                  title="Overage limit unavailable"
                />
              ) : (
                <OverageLimitForm
                  initialLimit={editableOverageLimit}
                  onUpdated={() => void summary.refetch()}
                />
              )}
            </>
          ) : (
            <p className="muted">
              An organization owner or admin can change subscription and
              overage settings.
            </p>
          )}
        </>
      ) : null}
      {showBillingActions ? (
        <BillingLedger
          entries={
            ledger.data?.pages.flatMap((page) => page.entries) ?? []
          }
          error={ledger.error}
          hasData={Boolean(ledger.data)}
          hasNextPage={ledger.hasNextPage}
          isFetchNextPageError={ledger.isFetchNextPageError}
          isFetchingNextPage={ledger.isFetchingNextPage}
          isPending={ledger.isPending}
          onLoadMore={() => void ledger.fetchNextPage()}
          onRetry={() => void ledger.refetch()}
        />
      ) : null}
    </>
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
              {error.message}
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

function OverageLimitForm({
  initialLimit,
  onUpdated,
}: {
  initialLimit: bigint;
  onUpdated: () => void;
}) {
  const { organization, transport } = useOrganization();
  const online = useOnline();
  const [monthlyLimit, setMonthlyLimit] = useState(() =>
    formatUsdMicrosInput(initialLimit),
  );
  const [message, setMessage] = useState("");
  const [formError, setFormError] = useState("");
  const update = useMutation(BillingService.method.updateOverageLimit, {
    transport,
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMessage("");
    setFormError("");
    const micros = parseUsdMicros(monthlyLimit.trim());
    if (micros === undefined) {
      setFormError(
        "Enter a non-negative USD amount with up to six decimals.",
      );
      return;
    }
    update.mutate(
      {
        idempotency: createIdempotencyKey(),
        monthlyLimit: { value: micros },
        organizationId: organization.organizationId,
      },
      {
        onError: (error) => setFormError(error.message),
        onSuccess: () => {
          setMessage("Monthly overage limit updated.");
          onUpdated();
        },
      },
    );
  };

  return (
    <form className="form-card billing-limit-form" onSubmit={submit}>
      <div>
        <span className="eyebrow">Metered usage</span>
        <h2>Monthly overage limit</h2>
        <p className="muted">
          Set zero to block new overage after available credits are used.
        </p>
      </div>
      <label>
        Limit in USD
        <input
          // This is the single critical input in the overage form.
          // eslint-disable-next-line jsx-a11y/no-autofocus
          autoFocus
          inputMode="decimal"
          min="0"
          onChange={(event) => setMonthlyLimit(event.target.value)}
          required
          step="0.000001"
          type="number"
          value={monthlyLimit}
        />
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
        disabled={!online || update.isPending}
        type="submit"
      >
        {update.isPending ? "Updating…" : "Update overage limit"}
      </button>
      {!online ? <OfflineActionHint /> : null}
    </form>
  );
}

export function UsagePage() {
  useDocumentMetadata("Usage", "View organization usage records.");
  const { organization, transport } = useOrganization();
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
        description="Usage visibility follows your organization and effective team role."
        title="Usage"
      />
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
          <div className="table-card">
            <table>
              <caption className="sr-only">Usage records</caption>
              <thead>
                <tr>
                  <th scope="col">Team</th>
                  <th scope="col">Units</th>
                  <th scope="col">Cost</th>
                  <th scope="col">Reference</th>
                </tr>
              </thead>
              <tbody>
                {usageRows.map((record) => (
                  <tr key={record.usageRecordId?.value}>
                    <td>{record.teamNameSnapshot}</td>
                    <td>{formatUsageUnits(record.units?.value)}</td>
                    <td>{formatUsageCost(record.totalCost?.value)}</td>
                    <td>{record.clientReference || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {usage.isFetchNextPageError ? (
            <p className="inline-error" role="alert">
              {usage.error.message}
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
    </>
  );
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
  const navigate = useNavigate();
  const online = useOnline();
  const [name, setName] = useState(organization.name);
  const [slug, setSlug] = useState(organization.slug);
  const [message, setMessage] = useState("");
  const [formError, setFormError] = useState("");
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
        const response = await updateSlug.mutateAsync({
          idempotency: createIdempotencyKey(),
          organizationId: uuid(organization.organizationId?.value),
          slug: normalizedSlug,
        });
        navigate(
          `/o/${response.organization?.slug ?? normalizedSlug}/settings`,
          { replace: true },
        );
        return;
      }
      if (normalizedName !== organization.name) {
        await refreshOrganization();
      }
      setMessage(
        normalizedName === organization.name
          ? "No organization changes to save."
          : "Organization settings updated.",
      );
    } catch (error) {
      const mutationError =
        error instanceof Error
          ? error.message
          : "Organization settings could not be updated.";
      if (nameUpdated) {
        try {
          await refreshOrganization();
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
                onChange={(event) => setSlug(event.target.value)}
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
    </>
  );
}
