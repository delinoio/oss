import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  BillingService,
  CatalogService,
  OrganizationRole,
  OrganizationService,
  SubscriptionStatus,
  TeamService,
} from "@delinoio/delibase-connect";
import { useState, type CSSProperties, type FormEvent } from "react";

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
  const members = useQuery(
    OrganizationService.method.listOrganizationMembers,
    { organizationId: organization.organizationId, page: { pageSize: 100 } },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );
  return (
    <>
      <OrganizationPageHeading
        description="Owners and admins can manage roles and team access."
        title="Members"
      />
      {members.isPending ? <LoadingState label="Loading members" /> : null}
      {members.isError ? (
        <ErrorState
          error={members.error}
          onRetry={() => void members.refetch()}
          title="Members unavailable"
        />
      ) : null}
      {members.data?.members.length === 0 ? (
        <EmptyState
          description="Invite someone to start collaborating."
          title="No members found"
        />
      ) : null}
      {members.data?.members.length ? (
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
              {members.data.members.map((member) => (
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
      ) : null}
    </>
  );
}

export function TeamsPage() {
  useDocumentMetadata("Teams", "View nested organization teams.");
  const { organization, transport } = useOrganization();
  const teams = useQuery(
    TeamService.method.listTeams,
    {
      includeDescendants: true,
      organizationId: organization.organizationId,
      page: { pageSize: 100 },
    },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );
  return (
    <>
      <OrganizationPageHeading
        description="Access granted to a parent team flows down to its descendants."
        title="Teams"
      />
      {teams.isPending ? <LoadingState label="Loading teams" /> : null}
      {teams.isError ? (
        <ErrorState
          error={teams.error}
          onRetry={() => void teams.refetch()}
          title="Teams unavailable"
        />
      ) : null}
      {teams.data?.teams.length === 0 ? (
        <EmptyState
          description="Every organization starts with a protected General team."
          title="No teams found"
        />
      ) : null}
      {teams.data?.teams.length ? (
        <ul className="team-tree" aria-label="Team hierarchy">
          {teams.data.teams.map((team) => (
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
      ) : null}
    </>
  );
}

export function BillingPage() {
  useDocumentMetadata("Billing", "View organization balance and subscription.");
  const { organization, transport } = useOrganization();
  const online = useOnline();
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
        </>
      ) : null}
    </>
  );
}

export function UsagePage() {
  useDocumentMetadata("Usage", "View organization usage records.");
  const { organization, transport } = useOrganization();
  const usage = useQuery(
    BillingService.method.listUsageRecords,
    { organizationId: organization.organizationId, page: { pageSize: 100 } },
    { gcTime: 0, retry: false, staleTime: 0, transport },
  );
  return (
    <>
      <OrganizationPageHeading
        description="Usage visibility follows your organization and effective team role."
        title="Usage"
      />
      {usage.isPending ? <LoadingState label="Loading usage" /> : null}
      {usage.isError ? (
        <ErrorState
          error={usage.error}
          onRetry={() => void usage.refetch()}
          title="Usage unavailable"
        />
      ) : null}
      {usage.data?.records.length === 0 ? (
        <EmptyState
          description="Usage will appear here after a mini-app service settles it."
          title="No usage yet"
        />
      ) : null}
      {usage.data?.records.length ? (
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
              {usage.data.records.map((record) => (
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
      ) : null}
    </>
  );
}

export function OrganizationSettingsPage() {
  useDocumentMetadata("Organization settings", "Update organization settings.");
  const { organization, transport } = useOrganization();
  const online = useOnline();
  const [name, setName] = useState(organization.name);
  const [message, setMessage] = useState("");
  const update = useMutation(
    OrganizationService.method.updateOrganization,
    { transport },
  );

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMessage("");
    update.mutate(
      {
        idempotency: createIdempotencyKey(),
        name: name.trim(),
        organizationId: uuid(organization.organizationId?.value),
      },
      {
        onError: (error) => setMessage(error.message),
        onSuccess: () => setMessage("Organization name updated."),
      },
    );
  };

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
          Current organization URL
          <input disabled value={`deli.dev/o/${organization.slug}`} />
        </label>
        {message ? (
          <p
            className={update.isError ? "inline-error" : "inline-success"}
            role="status"
          >
            {message}
          </p>
        ) : null}
        <button
          className="button primary"
          disabled={!online || update.isPending || !name.trim()}
          type="submit"
        >
          {update.isPending ? "Saving…" : "Save changes"}
        </button>
        {!online ? <OfflineActionHint /> : null}
      </form>
    </>
  );
}
