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
} from "@tairouter/client";
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
/** Setup progress of one checklist step; unresolved queries never settle it. */
enum StepStatus {
  Complete = "complete",
  Incomplete = "incomplete",
  Checking = "checking",
  Unavailable = "unavailable",
}
const stepStatusMessage = {
  [StepStatus.Complete]: "message_143b270a32",
  [StepStatus.Incomplete]: "notYetComplete",
  [StepStatus.Checking]: "checking",
  [StepStatus.Unavailable]: "statusUnavailable",
} as const;
function stepStatus(
  query: { isPending: boolean; isError: boolean },
  isDone: () => boolean,
) {
  if (query.isError) return StepStatus.Unavailable;
  if (query.isPending) return StepStatus.Checking;
  return isDone() ? StepStatus.Complete : StepStatus.Incomplete;
}
const PREVIEW_LIMIT = 4;
function RegionError({
  message,
  error,
  retry,
}: {
  message: string;
  error: unknown;
  retry: () => void;
}) {
  useLocale();
  if (!error) return null;
  return (
    <>
      <p className="overview-region-error">{message}</p>
      <ErrorNotice error={error} retry={retry} />
    </>
  );
}
export function OverviewPage({ organization, session }: PageProps) {
  useLocale();
  // Rolling seven-day window, fixed for the life of the page.
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
  const steps: [string, Page, StepStatus][] = [
    [translate("chooseOrganization"), "overview", StepStatus.Complete],
    [
      translate("connectPersonalProviders"),
      "providers",
      stepStatus(providers, () =>
        (providers.data?.connections ?? []).some(
          (c) => c.state === ConnectionState.READY,
        ),
      ),
    ],
    [
      translate("configureModelsRouting"),
      "routing",
      stepStatus(routes, () =>
        (routes.data?.routes ?? []).some(
          (r) => r.enabled && r.targets.length > 0,
        ),
      ),
    ],
    [
      translate("createApiKey"),
      "keys",
      stepStatus(keys, () =>
        (keys.data?.keys ?? []).some((k) => k.state === KeyState.ACTIVE),
      ),
    ],
    [
      translate("connectAClient"),
      "connect",
      stepStatus(keys, () =>
        (keys.data?.keys ?? []).some(
          (k) => k.kind === KeyKind.CLI && !!k.lastUsedAt,
        ),
      ),
    ],
  ];
  const connections = providers.data?.connections ?? [];
  const routeList = routes.data?.routes ?? [];
  return (
    <div className="overview">
      <PageHeader
        title={translate("yourWorkspace")}
        description={translate("yourSettingsIn", { value0: organization.name })}
        action={
          <a className="button primary" href={link("keys")}>
            {translate("createApiKey")}
          </a>
        }
      />
      <p className="overview-note">
        <strong>{translate("privateToYou")}</strong> ·{" "}
        {translate("yourProvidersAndRoutingAreSeparateIn")}
      </p>
      <section className="overview-usage" aria-labelledby="overview-usage">
        <h2 id="overview-usage">{translate("last7Days")}</h2>
        <p className="overview-caption">
          {translate("yourRequestsOverTheLastSeven")}
        </p>
        <RegionError
          message={translate("couldNotLoadUsageSummary")}
          error={usage.error}
          retry={() => void usage.refetch()}
        />
        {!usage.isError && (
          <SummaryCards
            summary={usage.data?.summary}
            loading={usage.isPending}
            updating={usage.isFetching && !usage.isPending}
          />
        )}
      </section>
      <div className="overview-primary">
        <UsageTrend
          key={`${organization.id}:${session.userId}`}
          className="overview-activity"
          title={translate("requestActivity")}
          description={translate("dailyRecordedRequestsInUtcMissing")}
          action={<a href={link("usage")}>{translate("viewUsage")}</a>}
          emptyHint={
            <>
              {translate("requestsAppearAfterAClientUsesYour")}{" "}
              <a href={link("connect")}>{translate("connectAClient")}</a>
            </>
          }
          organizationId={organization.id}
          userId={session.userId}
          timeRange={timeRange}
        />
        <Card className="overview-connections">
          <h2>{translate("personalConnections")}</h2>
          <RegionError
            message={translate("couldNotLoadPersonalConnections")}
            error={providers.error}
            retry={() => void providers.refetch()}
          />
          {providers.isPending ? (
            <Loading label={translate("loadingPersonalConnections")} />
          ) : providers.isError ? null : connections.length === 0 ? (
            <p>
              {translate("noPersonalConnectionsYet")}{" "}
              <a href={link("providers")}>{translate("connectAProvider")}</a>
            </p>
          ) : (
            <>
              <ul className="overview-list">
                {connections.slice(0, PREVIEW_LIMIT).map((c) => (
                  <li key={c.id}>
                    <span className="provider-mark" aria-hidden="true">
                      {c.providerId.slice(0, 2).toUpperCase()}
                    </span>
                    <div className="overview-list-body">
                      <h3>{c.name}</h3>
                      <p>
                        {translate("personalAccount_938eac", {
                          value0: c.providerId,
                        })}
                      </p>
                      <Badge
                        tone={
                          c.state === ConnectionState.READY
                            ? "success"
                            : "warning"
                        }
                      >
                        {connectionLabel[c.state] ?? translate("unknownStatus")}
                      </Badge>
                    </div>
                    <a
                      className="overview-link"
                      href={link("providers")}
                      aria-label={translate("manageConnection_fde57e", {
                        value0: c.name,
                      })}
                    >
                      {translate("manageConnection")}
                    </a>
                  </li>
                ))}
              </ul>
              <a className="overview-link" href={link("providers")}>
                {translate("manageProviders")}
              </a>
            </>
          )}
        </Card>
      </div>
      <div className="overview-secondary">
        <Card className="overview-checklist-card">
          <h2>{translate("getConnected")}</h2>
          <RegionError
            message={translate("couldNotLoadApiKeys")}
            error={keys.error}
            retry={() => void keys.refetch()}
          />
          {keys.isPending && <Loading label={translate("checkingApiKeys")} />}
          <ol className="overview-checklist">
            {steps.map(([label, page, status], i) => (
              <li key={page}>
                <span
                  className={
                    status === StepStatus.Complete ? "step done" : "step"
                  }
                  aria-hidden="true"
                >
                  {status === StepStatus.Complete ? "✓" : i + 1}
                </span>
                <a className="overview-link" href={link(page)}>
                  {label}
                </a>
                <span className="step-status">
                  {translate(stepStatusMessage[status])}
                </span>
              </li>
            ))}
          </ol>
        </Card>
        <Card className="overview-routing">
          <div className="overview-card-heading">
            <h2>{translate("yourRouting")}</h2>
            <a className="overview-link" href={link("routing")}>
              {translate("viewAll")}
            </a>
          </div>
          <RegionError
            message={translate("couldNotLoadRoutes")}
            error={routes.error}
            retry={() => void routes.refetch()}
          />
          {routes.isPending ? (
            <Loading label={translate("loadingRoutes")} />
          ) : routes.isError ? null : routeList.length === 0 ? (
            <p>
              {translate("noModelRoutesYetConfigureARoute")}{" "}
              <a href={link("routing")}>{translate("configureRouting")}</a>
            </p>
          ) : (
            <ul className="overview-list">
              {routeList.slice(0, PREVIEW_LIMIT).map((r) => (
                <li key={r.id}>
                  <div className="overview-list-body">
                    <h3>{r.modelId}</h3>
                    <p>
                      {translate(
                        r.targets.length === 1 ? "target_8418d3" : "targets",
                        { value0: r.targets.length },
                      )}
                    </p>
                  </div>
                  <Badge>
                    {r.enabled
                      ? translate("enabledCheckConnectionHealth")
                      : translate("disabled")}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}
// Mirrors the CLI profile-name rule in cmds/tairouter/types.go. Do not trim or normalize input before testing.
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
  const command = `tairouter connect --server '${origin}' --profile '${commandProfile}'`;
  const configureCommand = `tairouter configure --profile '${commandProfile}' --client CLIENT_ID`;
  const disconnectCommand = `tairouter disconnect --profile '${commandProfile}'`;
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
            <code>tairouter clients</code>
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
