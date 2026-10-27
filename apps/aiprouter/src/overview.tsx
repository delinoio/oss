// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { pageHref, type Page } from "./routes";
import { useEffect, useRef, useState } from "react";
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
// Mirrors the CLI profile-name rule in cmds/aiprouter/types.go. Do not trim or normalize input before testing.
const profilePattern = /^[a-z][a-z0-9-]{0,47}$/;
const defaultProfile = "default";
export const isValidConnectProfile = (value: string) =>
  profilePattern.test(value);
export function ConnectPage({ organization }: PageProps) {
  useLocale();
  const [profile, setProfile] = useState(defaultProfile);
  const profileInput = useRef<HTMLInputElement>(null);
  const safeProfile = isValidConnectProfile(profile);
  // Invalid text is never interpolated into a displayed or copied command.
  const commandProfile = safeProfile ? profile : defaultProfile;
  const origin = window.location.origin;
  const command = `aiprouter connect --server '${origin}' --profile '${commandProfile}'`;
  const configureCommand = `aiprouter configure --profile '${commandProfile}' --client CLIENT_ID`;
  const disconnectCommand = `aiprouter disconnect --profile '${commandProfile}'`;
  // Focus the single critical input once when the page is shown, never on keystrokes or scope refreshes.
  useEffect(() => {
    profileInput.current?.focus();
  }, []);
  return (
    <div className="connect-page">
      <PageHeader
        title={translate("connectAClient")}
        description={translate("connectToWithYourOwnRouting", {
          value0: organization.name,
        })}
      />
      <dl className="connect-context">
        <div>
          <dt>{translate("connectContextServer")}</dt>
          <dd>{origin}</dd>
        </div>
        <div>
          <dt>{translate("connectContextOrganization")}</dt>
          <dd>{organization.name}</dd>
        </div>
      </dl>
      <div className="connect-layout">
        <Card className="connect-step connect-step-one">
          <h2>
            <span className="connect-step-number">1</span>
            {translate("connectStartSecureConnectionTitle")}
          </h2>
          <Field
            label={translate("profileName")}
            hint={
              <>
                {translate("startWithALowercaseLetterUseLowercase")}
                {!safeProfile && (
                  <> {translate("connectProfileFallbackHint")}</>
                )}
              </>
            }
            error={
              safeProfile
                ? undefined
                : translate("enterAValidProfileNameBeforeCopying")
            }
          >
            <input
              ref={profileInput}
              value={profile}
              onChange={(e) => setProfile(e.target.value)}
            />
          </Field>
          <pre>
            <code>{command}</code>
          </pre>
          {safeProfile && (
            <div className="connect-copy">
              <CopyButton
                value={command}
                label={translate("copyConnectCommand")}
                variant="primary"
              />
            </div>
          )}
          <p>
            {translate("connectRunCommandInstruction", {
              value0: organization.name,
            })}
          </p>
          <p>{translate("connectCredentialStoreNote")}</p>
        </Card>
        <Card className="connect-step connect-step-two">
          <h2>
            <span className="connect-step-number">2</span>
            {translate("connectChooseConfigureTitle")}
          </h2>
          <pre>
            <code>aiprouter clients</code>
          </pre>
          <p>{translate("connectListClientsInstruction")}</p>
          <pre>
            <code>{configureCommand}</code>
          </pre>
          <div className="notice info">
            <h3>{translate("connectReviewBeforeApplyingTitle")}</h3>
            <p>{translate("theCliPreviewsSettingsChangesAndAsks")}</p>
          </div>
          <p>{translate("clientsThatNeedALoopbackRelayShow")}</p>
        </Card>
        <Card className="connect-recovery">
          <h2>{translate("recoverSafely")}</h2>
          <section>
            <h3>{translate("connectCredentialStoreUnavailableTitle")}</h3>
            <p>{translate("connectCredentialStoreUnavailableBody")}</p>
          </section>
          <section>
            <h3>{translate("connectRevocationIncompleteTitle")}</h3>
            <p>{translate("connectRevocationIncompleteBody")}</p>
            <a
              className="connect-link"
              href={pageHref("keys", organization.id)}
            >
              {translate("connectOpenApiKeys")}
            </a>
          </section>
          <section>
            <h3>{translate("connectRestorationConflictsTitle")}</h3>
            <p>{translate("connectRestorationConflictsBody")}</p>
          </section>
          <pre>
            <code>{disconnectCommand}</code>
          </pre>
        </Card>
      </div>
    </div>
  );
}
