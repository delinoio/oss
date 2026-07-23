import {
  useInfiniteQuery,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query";
import {
  BillingService,
  CatalogService,
  OrganizationRole,
  OrganizationService,
  SubscriptionStatus,
  TeamService,
} from "@delinoio/delibase-connect";
import { useState, type CSSProperties, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";

import { usePublicTransport } from "../api/ApiContext";
import { CatalogCard } from "../components/CatalogCard";
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

function formatUsdMicrosInput(value = 0n): string {
  const whole = value / 1_000_000n;
  const fraction = (value % 1_000_000n)
    .toString()
    .padStart(6, "0")
    .replace(/0+$/, "");
  return fraction ? `${whole}.${fraction}` : whole.toString();
}

export function canManageBilling(role: OrganizationRole): boolean {
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
  const catalog = useQuery(
    CatalogService.method.listCatalogApps,
    { page: { pageSize: 50 } },
    {
      gcTime: 15 * 60 * 1000,
      networkMode: "always",
      staleTime: 5 * 60 * 1000,
      transport,
    },
  );
  return (
    <>
      <OrganizationPageHeading
        description="Choose a tool for your team. Usage is attributed by team."
        title="Apps"
      />
      {catalog.isPending ? <LoadingState label="Loading apps" /> : null}
      {catalog.isError ? (
        <ErrorState
          error={catalog.error}
          onRetry={() => void catalog.refetch()}
          title="Apps unavailable"
        />
      ) : null}
      {catalog.data?.apps.length === 0 ? (
        <EmptyState
          description="There are no enabled apps in the public catalog."
          title="No apps yet"
        />
      ) : null}
      {catalog.data?.apps.length ? (
        <div className="catalog-grid compact-grid">
          {catalog.data.apps.map((app) => (
            <CatalogCard app={app} key={app.slug} />
          ))}
        </div>
      ) : null}
    </>
  );
}

export function MembersPage() {
  useDocumentMetadata("Members", "Manage organization members and roles.");
  const { organization, transport } = useOrganization();
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
        description="Owners and admins can manage roles and team access."
        title="Members"
      />
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
          description="Invite someone to start collaborating."
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

export function TeamsPage() {
  useDocumentMetadata("Teams", "View nested organization teams.");
  const { organization, transport } = useOrganization();
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
  const teamRows = teams.data?.pages.flatMap((page) => page.teams) ?? [];
  return (
    <>
      <OrganizationPageHeading
        description="Access granted to a parent team flows down to its descendants."
        title="Teams"
      />
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
                <div>
                  <strong>{team.name}</strong>
                  <small>
                    Level {team.depth + 1}
                    {team.protectedGeneral ? " · Protected" : ""}
                  </small>
                </div>
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

export function BillingPage() {
  useDocumentMetadata("Billing", "View organization balance and subscription.");
  const { callerRole, organization, transport } = useOrganization();
  const online = useOnline();
  const showBillingActions = canManageBilling(callerRole);
  const summary = useQuery(
    BillingService.method.getBillingSummary,
    { organizationId: organization.organizationId },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );
  const checkout = useMutation(
    BillingService.method.createSubscriptionCheckout,
    { transport },
  );
  const portal = useMutation(
    BillingService.method.createBillingPortalSession,
    { transport },
  );

  const openCheckout = () => {
    checkout.mutate(
      {
        cancelUrl: window.location.href,
        idempotency: createIdempotencyKey(),
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
                {formatUsdMicros(
                  summary.data.summary.availableCredit?.value,
                )}
              </strong>
            </article>
            <article>
              <span>Held credit</span>
              <strong>
                {formatUsdMicros(summary.data.summary.heldCredit?.value)}
              </strong>
            </article>
            <article>
              <span>Monthly overage limit</span>
              <strong>
                {summary.data.summary.overageLimitConfigured
                  ? formatUsdMicros(
                      summary.data.summary.monthlyOverageLimit?.value,
                    )
                  : "Not set"}
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
              <OverageLimitForm
                initialLimit={
                  summary.data.summary.overageLimitConfigured
                    ? summary.data.summary.monthlyOverageLimit?.value
                    : 0n
                }
                onUpdated={() => void summary.refetch()}
              />
            </>
          ) : (
            <p className="muted">
              An organization owner or admin can change subscription and
              overage settings.
            </p>
          )}
        </>
      ) : null}
    </>
  );
}

function OverageLimitForm({
  initialLimit,
  onUpdated,
}: {
  initialLimit?: bigint;
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
                    <td>{record.units?.value.toString() ?? "0"}</td>
                    <td>{formatUsdMicros(record.totalCost?.value)}</td>
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

export function OrganizationSettingsPage() {
  useDocumentMetadata("Organization settings", "Update organization settings.");
  const { organization, transport } = useOrganization();
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

    try {
      if (normalizedName !== organization.name) {
        await updateName.mutateAsync({
          idempotency: createIdempotencyKey(),
          name: normalizedName,
          organizationId: uuid(organization.organizationId?.value),
        });
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
      setMessage(
        normalizedName === organization.name
          ? "No organization changes to save."
          : "Organization settings updated.",
      );
    } catch (error) {
      setFormError(
        error instanceof Error
          ? error.message
          : "Organization settings could not be updated.",
      );
    }
  };
  const isPending = updateName.isPending || updateSlug.isPending;

  return (
    <>
      <OrganizationPageHeading
        description="Owners and admins can update organization details."
        title="Settings"
      />
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
    </>
  );
}
