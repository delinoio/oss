// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
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
  useLocale();
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
    [translate("chooseOrganization"), "overview", true],
    [
      translate("connectPersonalProviders"),
      "providers",
      providers.data?.connections.some(
        (c) => c.state === ConnectionState.READY,
      ),
    ],
    [
      translate("configureModelsRouting"),
      "routing",
      routes.data?.routes.some((r) => r.enabled && r.targets.length > 0),
    ],
    [
      translate("createApiKey"),
      "keys",
      keys.data?.keys.some((k) => k.state === KeyState.ACTIVE),
    ],
    [
      translate("connectAClient"),
      "connect",
      keys.data?.keys.some((k) => k.kind === KeyKind.CLI && k.lastUsedAt),
    ],
  ] as const;
  return (
    <>
      <PageHeader
        title={translate("yourWorkspace")}
        description={translate("yourSettingsIn", { value0: organization.name })}
        action={
          <a className="button primary" href={link("keys")}>
            {translate("createApiKey")}
          </a>
        }
      />
      <Notice>
        <strong>{translate("privateToYou")}</strong>
        <br />
        {translate("yourProvidersAndRoutingAreSeparateIn")}
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
                {connectionLabel[c.state] ?? translate("unknownStatus")}
              </Badge>
              <p>{translate("personalAccount")}</p>
              <a href={link("providers")}>{translate("manageConnection")}</a>
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
        <h2>{translate("getConnected")}</h2>
        <ol className="checklist">
          {steps.map(([label, page, done], i) => (
            <li key={page}>
              <span
                className={done ? "step done" : "step"}
                aria-label={
                  done
                    ? translate("message_143b270a32")
                    : translate("notYetComplete")
                }
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
          <h2>{translate("yourRouting")}</h2>
          <a href={link("routing")}>{translate("viewAll")}</a>
        </div>
        {routes.data?.routes.length === 0 ? (
          <p>{translate("noModelRoutesYetConfigureARoute")}</p>
        ) : (
          routes.data?.routes.slice(0, 4).map((r) => (
            <div className="record" key={r.id}>
              <strong>{r.modelId}</strong>
              <span>{translate("targets", { value0: r.targets.length })}</span>
              <Badge>
                {r.enabled
                  ? translate("enabledCheckConnectionHealth")
                  : translate("disabled")}
              </Badge>
            </div>
          ))
        )}
      </Card>
    </>
  );
}
export function ConnectPage({ organization }: PageProps) {
  useLocale();
  const [profile, setProfile] = useState("default");
  const safeProfile = /^[a-z][a-z0-9-]{0,47}$/.test(profile);
  const origin = window.location.origin;
  const command = `aiprouter connect --server '${origin}' --profile '${safeProfile ? profile : "default"}'`;
  return (
    <>
      <PageHeader
        title={translate("connectAClient")}
        description={translate("connectToWithYourOwnRouting", {
          value0: organization.name,
        })}
      />
      <Card>
        <h2>{translate("message1StartASecureConnection")}</h2>
        <p>
          {translate("server")}
          <strong>{origin}</strong>
        </p>
        <p>
          {translate("choose")}
          <strong>{organization.name}</strong>{" "}
          {translate("whenApprovingTheDeviceInYourBrowser")}
        </p>
        <Field
          label={translate("profileName")}
          hint={translate("startWithALowercaseLetterUseLowercase")}
        >
          <input value={profile} onChange={(e) => setProfile(e.target.value)} />
        </Field>
        {!safeProfile && (
          <Notice tone="danger">
            {translate("enterAValidProfileNameBeforeCopying")}
          </Notice>
        )}
        <pre>
          <code>{command}</code>
        </pre>
        {safeProfile && (
          <CopyButton value={command} label={translate("copyConnectCommand")} />
        )}
        <p>{translate("runThisCommandWithTheInstalledAiprouter")}</p>
      </Card>
      <Card>
        <h2>{translate("message2ChooseAndConfigureYourClient")}</h2>
        <pre>
          <code>aiprouter clients</code>
        </pre>
        <p>{translate("listSupportedClientsSettingsFormatsAndPlatform")}</p>
        <pre>
          <code>{`aiprouter configure --profile '${safeProfile ? profile : "default"}' --client CLIENT_ID`}</code>
        </pre>
        <Notice>{translate("theCliPreviewsSettingsChangesAndAsks")}</Notice>
        <p>{translate("clientsThatNeedALoopbackRelayShow")}</p>
      </Card>
      <Card>
        <h2>{translate("recoverSafely")}</h2>
        <p>{translate("ifTheOsCredentialStoreIsUnavailable")}</p>
        <p>
          {translate("ifServerRevocationCouldNotFinishOpen")}
          <a href={pageHref("keys", organization.id)}>
            {translate("apiKeys")}
          </a>{" "}
          {translate("andRevokeTheOutstandingCliKey")}
        </p>
        <p>{translate("ifRestorationDetectsSettingsConflictsPreserveYour")}</p>
        <pre>
          <code>{`aiprouter disconnect --profile '${safeProfile ? profile : "default"}'`}</code>
        </pre>
      </Card>
    </>
  );
}
