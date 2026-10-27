// SPDX-License-Identifier: Apache-2.0
import { pageHref, type Page } from "./routes";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  ProviderService,
  RoutingService,
  KeyService,
  UsageService,
  KeyState,
  ConnectionState,
  KeyKind,
} from "@aiprouter/client";
import { type PageProps, pageSize } from "./api";
import {
  PageHeader,
  Button,
  Card,
  Badge,
  Notice,
  ErrorNotice,
  Loading,
  Field,
  CopyButton,
} from "./ui";
import { SummaryCards, UsageTrend } from "./usage";
import { connectionLabel } from "./providers";
export function OverviewPage({ organization, session }: PageProps) {
  const [timeRange] = useState(() => ({
    start: timestampFromDate(new Date(Date.now() - 7 * 86400000)),
    end: timestampFromDate(new Date()),
  }));
  const providers = useQuery(ProviderService.method.listProviderConnections, {
    organizationId: organization.id,
    page: { pageSize },
  });
  const routes = useQuery(RoutingService.method.listRoutes, {
    organizationId: organization.id,
    page: { pageSize },
  });
  const keys = useQuery(KeyService.method.listKeys, {
    organizationId: organization.id,
    page: { pageSize },
  });
  const usage = useQuery(UsageService.method.getUsageSummary, {
    organizationId: organization.id,
    userId: session.userId,
    timeRange,
  });
  const link = (page: Page) => pageHref(page, organization.id);
  const steps = [
    ["Choose organization", "overview", true],
    [
      "Connect personal providers",
      "providers",
      providers.data?.connections.some(
        (c) => c.state === ConnectionState.READY,
      ),
    ],
    [
      "Configure models/routing",
      "routing",
      routes.data?.routes.some((r) => r.enabled && r.targets.length > 0),
    ],
    [
      "Create API key",
      "keys",
      keys.data?.keys.some((k) => k.state === KeyState.ACTIVE),
    ],
    [
      "Connect a client",
      "connect",
      keys.data?.keys.some((k) => k.kind === KeyKind.CLI && k.lastUsedAt),
    ],
  ] as const;
  return (
    <>
      <PageHeader
        title="Your workspace"
        description={`Your settings in ${organization.name}.`}
        action={
          <a className="button primary" href={link("keys")}>
            Create API key
          </a>
        }
      />
      <Notice>
        <strong>Private to you</strong>
        <br />
        Your providers and routing are separate in every organization.
      </Notice>
      <ErrorNotice
        error={providers.error ?? routes.error ?? keys.error ?? usage.error}
      />
      {providers.isPending ? (
        <Loading />
      ) : (
        <div className="grid">
          {providers.data?.connections.slice(0, 4).map((c) => (
            <Card key={c.id}>
              <div className="record-title">
                <span className="provider-mark" aria-hidden="true">
                  {c.providerId.slice(0, 2).toUpperCase()}
                </span>
                <h2>{c.name}</h2>
              </div>
              <Badge
                tone={c.state === ConnectionState.READY ? "success" : "warning"}
              >
                {connectionLabel[c.state] ?? "Unknown status"}
              </Badge>
              <p>Personal account</p>
              <a href={link("providers")}>Manage connection</a>
            </Card>
          ))}
        </div>
      )}
      <SummaryCards summary={usage.data?.summary} />
      <UsageTrend
        organizationId={organization.id}
        userId={session.userId}
        timeRange={timeRange}
      />
      <Card>
        <h2>Get connected</h2>
        <ol className="checklist">
          {steps.map(([label, page, done], i) => (
            <li key={label}>
              <span
                className={done ? "step done" : "step"}
                aria-label={done ? "Complete" : "Not yet complete"}
              >
                {done ? "✓" : i + 1}
              </span>
              <a href={link(page)}>{label}</a>
            </li>
          ))}
        </ol>
      </Card>
      <Card>
        <div className="record">
          <h2>Your routing</h2>
          <a href={link("routing")}>View all</a>
        </div>
        {routes.data?.routes.length === 0 ? (
          <p>
            No model routes yet. Configure a route to start sending requests.
          </p>
        ) : (
          routes.data?.routes.slice(0, 4).map((r) => (
            <div className="record" key={r.id}>
              <strong>{r.modelId}</strong>
              <span>{r.targets.length} targets</span>
              <Badge>
                {r.enabled ? "Enabled · check connection health" : "Disabled"}
              </Badge>
            </div>
          ))
        )}
      </Card>
    </>
  );
}
export function ConnectPage({ organization }: PageProps) {
  const [profile, setProfile] = useState("default");
  const safeProfile = /^[a-z][a-z0-9-]{0,47}$/.test(profile);
  const origin = window.location.origin;
  const command = `aiprouter connect --server '${origin}' --profile '${safeProfile ? profile : "default"}'`;
  return (
    <>
      <PageHeader
        title="Connect a client"
        description={`Connect to ${organization.name} with your own routing.`}
      />
      <Card>
        <h2>1. Start a secure connection</h2>
        <p>
          Server: <strong>{origin}</strong>
        </p>
        <p>
          Choose <strong>{organization.name}</strong> when approving the device
          in your browser.
        </p>
        <Field
          label="Profile name"
          hint="Start with a lowercase letter. Use lowercase letters, digits, and hyphens; at most 48 characters."
        >
          <input value={profile} onChange={(e) => setProfile(e.target.value)} />
        </Field>
        {!safeProfile && (
          <Notice tone="danger">
            Enter a valid profile name before copying the command.
          </Notice>
        )}
        <pre>
          <code>{command}</code>
        </pre>
        {safeProfile && (
          <CopyButton value={command} label="Copy connect command" />
        )}
        <p>
          Run this command with the installed AIPRouter CLI. Confirm the browser
          code matches your CLI. The CLI stores the resulting key in your OS
          credential store.
        </p>
      </Card>
      <Card>
        <h2>2. Choose and configure your client</h2>
        <pre>
          <code>aiprouter clients</code>
        </pre>
        <p>
          List supported clients, settings formats, and platform availability.
          Then choose the client ID from that list:
        </p>
        <pre>
          <code>{`aiprouter configure --profile '${safeProfile ? profile : "default"}' --client CLIENT_ID`}</code>
        </pre>
        <Notice>
          The CLI previews settings changes and asks before applying them. A
          separate confirmation is required if a client stores the remote key in
          its settings.
        </Notice>
        <p>
          Clients that need a loopback relay show an explicit local-access
          warning. Review it before installing the profile service.
        </p>
      </Card>
      <Card>
        <h2>Recover safely</h2>
        <p>
          If the OS credential store is unavailable, the CLI leaves client
          settings unchanged. Restore access to the OS credential store and
          retry. There is no plaintext credential fallback.
        </p>
        <p>
          If server revocation could not finish, open{" "}
          <a href={pageHref("keys", organization.id)}>API keys</a> and revoke
          the outstanding CLI key.
        </p>
        <p>
          If restoration detects settings conflicts, preserve your settings and
          protected backups. Resolve conflicts manually before retrying
          restoration. Server key revocation and local settings restoration are
          separate outcomes.
        </p>
        <pre>
          <code>{`aiprouter disconnect --profile '${safeProfile ? profile : "default"}'`}</code>
        </pre>
      </Card>
    </>
  );
}
