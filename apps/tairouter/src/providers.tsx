// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber, enumLabel } from "./i18n";
import { pageHref } from "./routes";
import { useEffect, useId, useRef, useState } from "react";
import { HostedHelpers } from "./hosted";
import {
  ConfigurationFields,
  configurationDraft,
  configurationValues,
} from "./provider-configuration";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import {
  ProviderService,
  PoolService,
  RoutingService,
  ProviderAuthKind,
  ProviderOAuthMethod,
  type BeginProviderOAuthRequest,
  ConnectionState,
  OAuthState,
  type ProviderConnection,
  type ProviderOAuth,
  type ProviderDefinition,
} from "@tairouter/client";
import { useApi, useRefresh, type PageProps, pageSize } from "./api";
import { useAction } from "./actions";
import { usePersonalResources } from "./resources";
import { DisconnectDialog } from "./provider-disconnect";
import {
  PageHeader,
  Button,
  Card,
  Field,
  Dialog,
  ErrorNotice,
  Loading,
  EmptyState,
  Badge,
  Notice,
  formatDate,
  Pagination,
} from "./ui";
export const connectionLabel: Record<number, string> = {
  get [ConnectionState.UNSPECIFIED]() {
    return translate("unknownStatus");
  },
  get [ConnectionState.READY]() {
    return translate("connected");
  },
  get [ConnectionState.AUTHORIZATION_REQUIRED]() {
    return translate("reconnectRequired");
  },
  get [ConnectionState.DISABLED]() {
    return translate("notConnected");
  },
  get [ConnectionState.COOLDOWN]() {
    return translate("coolingDown");
  },
  get [ConnectionState.ERROR]() {
    return translate("connectionFailed");
  },
};
export const authLabel: Record<number, string> = {
  get [ProviderAuthKind.API_KEY]() {
    return translate("apiKey");
  },
  [ProviderAuthKind.OAUTH]: "OAuth",
  get [ProviderAuthKind.NO_AUTH]() {
    return translate("noAuthentication");
  },
};
export function ProvidersPage({ organization }: PageProps) {
  useLocale();
  const api = useApi();
  const action = useAction();
  const [token, setToken] = useState("");
  const [search, setSearch] = useState("");
  const [edit, setEdit] = useState<ProviderConnection | true>();
  const [remove, setRemove] = useState<ProviderConnection>();
  // Connection whose deletion was last submitted; scopes the error and pending display.
  const [attempted, setAttempted] = useState<string>();
  // Timestamp of a confirmed disconnect whose focus target may disappear with the list row.
  const [restoreFocus, setRestoreFocus] = useState<number>();
  const listTarget = useRef<HTMLDivElement>(null);
  const [models, setModels] = useState<ProviderConnection>();
  const [oauth, setOauth] = useState<ProviderOAuth>();
  const [oauthMethod, setOauthMethod] = useState<ProviderOAuthMethod>();
  const [oauthSetup, setOauthSetup] = useState<{
    providerId: string;
    name: string;
    connectionId?: string;
  }>();
  const query = useQuery(ProviderService.method.listProviderConnections, {
    organizationId: organization.id,
    page: { pageSize, pageToken: token },
  });
  const catalog = useQuery(ProviderService.method.listProviderCatalog, {});
  const { pools, routes } = usePersonalResources(organization.id);
  // An organization switch must not carry a scoped confirmation into another organization.
  useEffect(() => {
    setRemove(undefined);
    setAttempted(undefined);
  }, [organization.id]);
  useEffect(() => {
    if (restoreFocus === undefined) return;
    const active = document.activeElement;
    if (!active || active === document.body || !active.isConnected) {
      listTarget.current?.focus();
      setRestoreFocus(undefined);
    } else if (
      query.fetchStatus === "idle" &&
      query.dataUpdatedAt >= restoreFocus
    ) {
      setRestoreFocus(undefined);
    }
  }, [restoreFocus, query.data, query.fetchStatus, query.dataUpdatedAt]);
  const records = query.data?.connections.filter((c) =>
    `${c.name} ${c.providerId}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <>
      <PageHeader
        title={translate("yourProviders")}
        description={translate("onlyYoursIn", { value0: organization.name })}
        action={
          <Button variant="primary" onClick={() => setEdit(true)}>
            {translate("connectProvider")}
          </Button>
        }
      />
      <Notice>{translate("privateToYouYourProvidersAndRouting")}</Notice>
      <div className="actions">
        <a className="button" href={pageHref("pools", organization.id)}>
          {translate("manageAccountPools")}
        </a>
      </div>
      <HostedHelpers
        organizationId={organization.id}
        organizationName={organization.name}
      />
      {action.feedback}
      <Field label={translate("searchYourConnections")}>
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </Field>
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      <div
        ref={listTarget}
        className="providers-list"
        role="group"
        aria-label={translate("yourProviders")}
        tabIndex={-1}
      >
        {query.isPending ? (
          <Loading />
        ) : records?.length === 0 ? (
          <EmptyState
            title={
              search
                ? translate("noMatchingConnections")
                : translate("connectYourFirstProvider")
            }
            action={
              search ? (
                <Button onClick={() => setSearch("")}>
                  {translate("clearFilters")}
                </Button>
              ) : undefined
            }
          >
            {translate("onlyYouCanUseTheseProviderAccounts")}
          </EmptyState>
        ) : (
          <div className="grid">
            {records?.map((c) => (
              <Card key={c.id}>
                <div className="record-title">
                  <span className="provider-mark" aria-hidden="true">
                    {c.providerId.slice(0, 2).toUpperCase()}
                  </span>
                  <div>
                    <h2>{c.name}</h2>
                    <p>
                      {catalog.data?.providers.find(
                        (p) => p.id === c.providerId,
                      )?.displayName ?? c.providerId}
                    </p>
                  </div>
                </div>
                <Badge
                  tone={
                    c.state === ConnectionState.READY
                      ? "success"
                      : c.state === ConnectionState.ERROR
                        ? "danger"
                        : "warning"
                  }
                >
                  {connectionLabel[c.state] ?? translate("unknownStatus")}
                </Badge>
                <p>
                  {translate("personalAccount_938eac", {
                    value0: authLabel[c.authKind],
                  })}
                </p>
                <p>{translate("added", { value0: formatDate(c.createdAt) })}</p>
                {c.cooldownUntil && (
                  <p>
                    {translate("coolingDownUntil", {
                      value0: formatDate(c.cooldownUntil),
                    })}
                  </p>
                )}
                <div className="actions">
                  <Button onClick={() => setEdit(c)}>
                    {translate("editConnection")}
                  </Button>
                  <Button onClick={() => setModels(c)}>
                    {translate("viewModels")}
                  </Button>
                  {c.authKind === ProviderAuthKind.OAUTH && (
                    <Button
                      disabled={action.pending}
                      onClick={() =>
                        setOauthSetup({
                          providerId: c.providerId,
                          name: c.name,
                          connectionId: c.id,
                        })
                      }
                    >
                      {translate("reconnect")}
                    </Button>
                  )}
                  <Button variant="danger" onClick={() => setRemove(c)}>
                    {translate("disconnect")}
                  </Button>
                </div>
              </Card>
            ))}
          </div>
        )}
      </div>
      <Pagination
        next={query.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
      />
      {edit && (
        <ConnectionEditor
          connection={edit === true ? undefined : edit}
          organizationName={organization.name}
          providers={catalog.data?.providers ?? []}
          catalogStatus={
            catalog.isPending
              ? CatalogStatus.Loading
              : catalog.isError && !catalog.data
                ? CatalogStatus.Unavailable
                : CatalogStatus.Ready
          }
          catalogError={catalog.error}
          onRetryCatalog={() => void catalog.refetch()}
          error={action.error}
          pending={action.pending}
          onClose={() => setEdit(undefined)}
          onSave={(values) => {
            if (edit === true && values.authKind === ProviderAuthKind.OAUTH) {
              setOauthSetup({
                providerId: values.providerId,
                name: values.name,
              });
              setEdit(undefined);
              return;
            }
            void action.run(
              async () => {
                if (edit !== true) {
                  await api.provider.updateProviderConnection({
                    organizationId: organization.id,
                    connectionId: edit.id,
                    resetConfiguration: values.resetConfiguration,
                    name: values.name,
                    baseUrl: values.baseUrl,
                    apiKey: values.secret || undefined,
                    configuration: values.configuration,
                    customHeaders: values.customHeaders,
                  });
                  return undefined;
                }
                await api.provider.createProviderConnection({
                  organizationId: organization.id,
                  providerId: values.providerId,
                  name: values.name,
                  baseUrl: values.baseUrl,
                  authKind: values.authKind,
                  apiKey: values.secret || undefined,
                  configuration: values.configuration,
                  customHeaders: values.customHeaders,
                });
                return undefined;
              },
              (authorization) => {
                setEdit(undefined);
                setOauth(authorization);
              },
              translate("connectionSaved"),
            );
          }}
        />
      )}
      {models && (
        <ModelsDialog
          organizationId={organization.id}
          connection={models}
          onClose={() => setModels(undefined)}
        />
      )}
      {oauthSetup && (
        <OAuthSetup
          providerId={oauthSetup.providerId}
          provider={catalog.data?.providers.find(
            (p) => p.id === oauthSetup.providerId,
          )}
          pending={action.pending}
          error={action.error}
          onClose={() => setOauthSetup(undefined)}
          onSave={(options) => {
            void action.run(
              () =>
                api.provider.beginProviderOAuth({
                  organizationId: organization.id,
                  ...oauthSetup,
                  ...options,
                }),
              (result) => {
                setOauth(result.authorization);
                setOauthMethod(
                  options.method ??
                    catalog.data?.providers.find(
                      (p) => p.id === oauthSetup.providerId,
                    )?.oauthMethods[0],
                );
                setOauthSetup(undefined);
              },
              translate("authorizationStarted"),
            );
          }}
        />
      )}
      {oauth && (
        <OAuthDialog
          organizationId={organization.id}
          authorization={oauth}
          method={oauthMethod}
          action={action}
          onClose={() => setOauth(undefined)}
        />
      )}
      {remove && (
        <DisconnectDialog
          connection={remove}
          providerLabel={
            catalog.data?.providers.find((p) => p.id === remove.providerId)
              ?.displayName ?? remove.providerId
          }
          organizationId={organization.id}
          organizationName={organization.name}
          pools={pools}
          routes={routes}
          pending={action.pending && attempted === remove.id}
          busy={action.pending}
          error={attempted === remove.id ? action.error : undefined}
          onClose={() => setRemove(undefined)}
          onConfirm={() => {
            setAttempted(remove.id);
            void action.run(
              () =>
                api.provider.deleteProviderConnection({
                  organizationId: organization.id,
                  connectionId: remove.id,
                }),
              () => {
                setRemove(undefined);
                setRestoreFocus(Date.now());
              },
              translate("providerDisconnected"),
            );
          }}
        />
      )}
    </>
  );
}
type ConnectionValues = ReturnType<typeof configurationValues> & {
  providerId: string;
  name: string;
  baseUrl: string;
  authKind: ProviderAuthKind;
  secret: string;
};
/** Load state of the provider catalog that backs the create variant. */
enum CatalogStatus {
  Loading = "loading",
  Unavailable = "unavailable",
  Ready = "ready",
}
/** Editor fields that can carry an associated validation message. */
enum EditorField {
  Provider = "provider",
  Authentication = "authentication",
  Name = "name",
  BaseUrl = "baseUrl",
  Secret = "secret",
  Configuration = "configuration",
}
type EditorErrors = Partial<Record<EditorField, string>>;
function isAbsoluteUrl(value: string) {
  try {
    new URL(value);
    return true;
  } catch {
    return false;
  }
}
function ConnectionEditor({
  connection,
  organizationName,
  providers,
  catalogStatus,
  catalogError,
  onRetryCatalog,
  onClose,
  onSave,
  pending,
  error,
}: {
  connection?: ProviderConnection;
  organizationName: string;
  providers: ProviderDefinition[];
  catalogStatus: CatalogStatus;
  catalogError: unknown;
  onRetryCatalog: () => void;
  onClose: () => void;
  onSave: (v: ConnectionValues) => void;
  pending: boolean;
  error: unknown;
}) {
  useLocale();
  const ids = useId();
  const fieldId = (field: EditorField) => `${ids}-${field}`;
  const summaryRef = useRef<HTMLDivElement>(null);
  const [configuration, setConfiguration] = useState(() =>
    configurationDraft(connection),
  );
  const [errors, setErrors] = useState<EditorErrors>({});
  const [attempt, setAttempt] = useState(0);
  const [providerId, setProvider] = useState(connection?.providerId ?? "");
  const [name, setName] = useState(connection?.name ?? "");
  const [baseUrl, setBase] = useState(connection?.baseUrl ?? "");
  const [authKind, setAuth] = useState(
    connection?.authKind ?? ProviderAuthKind.UNSPECIFIED,
  );
  const [secret, setSecret] = useState("");
  const [reveal, setReveal] = useState(false);
  const [search, setSearch] = useState("");
  const provider = providers.find((p) => p.id === providerId);
  const query = search.toLowerCase();
  const matchesSearch = (p: ProviderDefinition) =>
    `${p.displayName} ${p.id}`.toLowerCase().includes(query);
  const visibleProviders = providers.filter(
    (p) => p.id === providerId || matchesSearch(p),
  );
  const catalogReady =
    catalogStatus === CatalogStatus.Ready && providers.length > 0;
  const noMatch =
    catalogReady && !!search && !providers.some((p) => matchesSearch(p));
  const isOauthCreate = authKind === ProviderAuthKind.OAUTH && !connection;
  const entries = (Object.values(EditorField) as EditorField[])
    .filter((field) => errors[field])
    .map((field) => ({ field, message: errors[field] as string }));
  useEffect(() => {
    if (attempt > 0) summaryRef.current?.focus();
  }, [attempt]);
  const clearError = (field: EditorField) =>
    setErrors((current) => {
      if (!current[field]) return current;
      const { [field]: _removed, ...rest } = current;
      return rest;
    });
  const submit = () => {
    if (pending) return;
    const next: EditorErrors = {};
    if (!connection) {
      if (!providerId)
        next[EditorField.Provider] = translate("chooseAProvider");
      if (!authKind)
        next[EditorField.Authentication] = translate("chooseAuthentication");
    }
    if (!name.trim()) next[EditorField.Name] = translate("enterAnAccountLabel");
    if (authKind !== ProviderAuthKind.OAUTH && baseUrl.trim()) {
      if (!isAbsoluteUrl(baseUrl.trim()))
        next[EditorField.BaseUrl] = translate("enterAFullUrlSuchAsHttps");
    }
    if (authKind === ProviderAuthKind.API_KEY && !connection && !secret)
      next[EditorField.Secret] = translate("enterAnApiKey");
    let advanced: ReturnType<typeof configurationValues> | undefined;
    try {
      advanced = configurationValues(configuration);
    } catch (failure) {
      next[EditorField.Configuration] =
        failure instanceof Error
          ? failure.message
          : translate("checkTheConnectionSettings");
    }
    setErrors(next);
    if (!advanced || Object.keys(next).length) {
      setAttempt((value) => value + 1);
      return;
    }
    onSave({
      ...advanced,
      providerId,
      name: name.trim(),
      baseUrl: baseUrl.trim(),
      authKind,
      // A key typed for another authentication method must never be sent.
      secret: authKind === ProviderAuthKind.API_KEY ? secret : "",
    });
  };
  const providerLabel =
    provider?.displayName ?? connection?.providerId ?? providerId;
  return (
    <Dialog
      title={
        connection ? translate("editConnection") : translate("connectProvider")
      }
      onClose={onClose}
      initialFocus="[data-initial-focus]"
      wide
      className="connection-editor"
    >
      <p className="connection-editor-subtitle">
        {connection
          ? translate("connectionIdentitySummary", {
              value0: connection.name,
              value1: providerLabel,
              value2: organizationName,
            })
          : translate("onlyYoursIn", { value0: organizationName })}
      </p>
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        {entries.length > 0 && (
          <div
            className="notice danger form-error-summary"
            role="alert"
            tabIndex={-1}
            ref={summaryRef}
          >
            <p>
              <strong>{translate("fixTheseItemsBeforeSaving")}</strong>
            </p>
            <ul>
              {entries.map(({ field, message }) => (
                <li key={field}>
                  {field === EditorField.Configuration ? (
                    message
                  ) : (
                    <a
                      href={`#${fieldId(field)}`}
                      onClick={(e) => {
                        e.preventDefault();
                        document.getElementById(fieldId(field))?.focus();
                      }}
                    >
                      {message}
                    </a>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
        <section
          className="connection-group"
          aria-labelledby={`${ids}-group-provider`}
        >
          <h3 id={`${ids}-group-provider`}>
            {translate("providerAndAuthentication")}
          </h3>
          {connection ? (
            <>
              <dl className="connection-identity">
                <div>
                  <dt>{translate("provider")}</dt>
                  <dd>{providerLabel}</dd>
                </div>
                <div>
                  <dt>{translate("authentication")}</dt>
                  <dd>{authLabel[connection.authKind]}</dd>
                </div>
              </dl>
              <p className="connection-helper">
                {translate("providerAndAuthenticationCannotBeChanged")}
              </p>
            </>
          ) : (
            <>
              <Field label={translate("findAProvider")}>
                <input
                  type="search"
                  data-initial-focus
                  placeholder={translate("searchSupportedProviders")}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />
              </Field>
              {catalogStatus === CatalogStatus.Loading && (
                <div className="notice" role="status" aria-busy="true">
                  {translate("loadingSupportedProviders")}
                </div>
              )}
              {catalogStatus === CatalogStatus.Unavailable && (
                <ErrorNotice error={catalogError} retry={onRetryCatalog} />
              )}
              {catalogStatus === CatalogStatus.Ready &&
                providers.length === 0 && (
                  <div className="notice warning" role="status">
                    <p>{translate("noSupportedProvidersAreAvailable")}</p>
                    <Button onClick={onRetryCatalog}>
                      {translate("tryAgain")}
                    </Button>
                  </div>
                )}
              {noMatch && (
                <div className="notice warning" role="status">
                  <p>
                    {translate("noSupportedProvidersMatch", { value0: search })}
                  </p>
                  <Button onClick={() => setSearch("")}>
                    {translate("clearSearch")}
                  </Button>
                </div>
              )}
              <Field label={translate("provider")} error={errors.provider}>
                <select
                  id={fieldId(EditorField.Provider)}
                  required
                  disabled={!catalogReady}
                  value={providerId}
                  onChange={(e) => {
                    const p = providers.find((p) => p.id === e.target.value);
                    setProvider(e.target.value);
                    setConfiguration(configurationDraft());
                    setAuth(p?.authKinds[0] ?? ProviderAuthKind.UNSPECIFIED);
                    // A secret typed for one provider must not carry over to another.
                    setSecret("");
                    setReveal(false);
                    setErrors({});
                  }}
                >
                  <option value="">
                    {translate("chooseASupportedProvider")}
                  </option>
                  {visibleProviders.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.displayName}
                    </option>
                  ))}
                </select>
              </Field>
              <Field
                label={translate("authentication")}
                error={errors.authentication}
              >
                <select
                  id={fieldId(EditorField.Authentication)}
                  required
                  value={authKind}
                  onChange={(e) => {
                    setAuth(Number(e.target.value));
                    setSecret("");
                    setReveal(false);
                    clearError(EditorField.Authentication);
                    clearError(EditorField.Secret);
                  }}
                >
                  {!provider && (
                    <option value={0}>
                      {translate("chooseAProviderFirst")}
                    </option>
                  )}
                  {provider?.authKinds.map((k) => (
                    <option key={k} value={k}>
                      {authLabel[k]}
                    </option>
                  ))}
                </select>
              </Field>
            </>
          )}
        </section>
        <section
          className="connection-group"
          aria-labelledby={`${ids}-group-account`}
        >
          <h3 id={`${ids}-group-account`}>{translate("accountDetails")}</h3>
          <Field label={translate("accountLabel")} error={errors.name}>
            <input
              id={fieldId(EditorField.Name)}
              required
              data-initial-focus={connection ? true : undefined}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                clearError(EditorField.Name);
              }}
            />
          </Field>
          {authKind !== ProviderAuthKind.OAUTH && (
            <Field
              label={translate("baseUrlOptional")}
              hint={translate("leaveEmptyToUseTheProviderDefault")}
              error={errors.baseUrl}
            >
              <input
                id={fieldId(EditorField.BaseUrl)}
                type="url"
                placeholder={translate("providerDefault")}
                value={baseUrl}
                onChange={(e) => {
                  setBase(e.target.value);
                  clearError(EditorField.BaseUrl);
                }}
              />
            </Field>
          )}
          {authKind === ProviderAuthKind.API_KEY && (
            <div className="connection-secret">
              <Field
                label={
                  connection
                    ? translate("replacementApiKeyOptional")
                    : translate("apiKey")
                }
                hint={
                  connection
                    ? translate("leaveEmptyToKeepTheCurrentKey")
                    : translate("storedSecretsAreNeverShownAgain")
                }
                error={errors.secret}
              >
                <input
                  id={fieldId(EditorField.Secret)}
                  type={reveal ? "text" : "password"}
                  autoComplete="new-password"
                  required={!connection}
                  value={secret}
                  onChange={(e) => {
                    setSecret(e.target.value);
                    clearError(EditorField.Secret);
                  }}
                />
              </Field>
              <Button
                aria-pressed={reveal}
                aria-controls={fieldId(EditorField.Secret)}
                onClick={() => setReveal((v) => !v)}
              >
                {reveal ? translate("hideKey") : translate("revealKey")}
              </Button>
            </div>
          )}
          {authKind === ProviderAuthKind.NO_AUTH && (
            <p className="connection-helper">
              {translate("thisAuthenticationMethodNeedsNoApiKey")}
            </p>
          )}
        </section>
        {isOauthCreate ? (
          <Notice>
            {translate("afterAuthorizationEditThisConnectionToConfigure")}
          </Notice>
        ) : (
          <section
            className="connection-group"
            aria-labelledby={`${ids}-group-settings`}
          >
            <h3 id={`${ids}-group-settings`}>
              {translate("connectionSettings")}
            </h3>
            <ConfigurationFields
              draft={configuration}
              onChange={(draft) => {
                setConfiguration(draft);
                clearError(EditorField.Configuration);
              }}
              providerId={providerId}
              connection={connection}
            />
          </section>
        )}
        <ErrorNotice error={error} retry={submit} />
        {pending && (
          <p className="mutation-status" role="status">
            {translate("message_c5ca36dfc7")}
          </p>
        )}
        <div className="connection-editor-footer">
          <p className="connection-editor-privacy">
            {translate("yourCredentialsStayPrivateToYou")}
          </p>
          <div className="actions">
            <Button onClick={onClose}>{translate("cancel")}</Button>
            <Button
              type="submit"
              variant="primary"
              disabled={pending || (!connection && !catalogReady)}
            >
              {pending
                ? translate("message_23e39291d6")
                : isOauthCreate
                  ? translate("continueWithOauth")
                  : translate("saveConnection")}
            </Button>
          </div>
        </div>
      </form>
    </Dialog>
  );
}
type OAuthAction = ReturnType<typeof useAction>;
const oauthStateTone: Record<
  number,
  "neutral" | "success" | "warning" | "danger" | "info"
> = {
  [OAuthState.PENDING]: "info",
  [OAuthState.COMPLETED]: "success",
  [OAuthState.CANCELED]: "neutral",
  [OAuthState.EXPIRED]: "warning",
  [OAuthState.FAILED]: "danger",
};
// Text labels keep the state readable without color. Unknown values never borrow a known label.
function oauthStateLabel(state: OAuthState): string {
  switch (state) {
    case OAuthState.PENDING:
      return translate("oauthStatePending");
    case OAuthState.COMPLETED:
      return translate("oauthStateCompleted");
    case OAuthState.CANCELED:
      return translate("oauthStateCanceled");
    case OAuthState.EXPIRED:
      return translate("oauthStateExpired");
    case OAuthState.FAILED:
      return translate("oauthStateFailed");
    default:
      return translate("unknownStatus");
  }
}
// Every terminal state has its own safe text; none of them offers pending actions
// or implies that the old attempt can be retried.
function oauthTerminalMessage(auth: ProviderOAuth): {
  tone: "success" | "warning" | "danger" | "info";
  text: string;
} {
  switch (auth.state) {
    case OAuthState.COMPLETED:
      return auth.connectionId
        ? {
            tone: "success",
            text: translate("providerConnectedYourConnectionIsReadyIn"),
          }
        : {
            tone: "warning",
            text: translate("oauthCompletedWithoutConnection"),
          };
    case OAuthState.CANCELED:
      return { tone: "info", text: translate("oauthCanceledMessage") };
    case OAuthState.EXPIRED:
      return { tone: "warning", text: translate("oauthExpiredMessage") };
    case OAuthState.FAILED:
      return { tone: "danger", text: translate("oauthFailedMessage") };
    default:
      return { tone: "warning", text: translate("oauthUnknownMessage") };
  }
}
// The page owns the mutation state so a result survives dismissal of this dialog.
function OAuthDialog({
  organizationId,
  authorization,
  method,
  action,
  onClose,
}: {
  organizationId: string;
  authorization: ProviderOAuth;
  method?: ProviderOAuthMethod;
  action: OAuthAction;
  onClose: () => void;
}) {
  useLocale();
  const api = useApi();
  const refresh = useRefresh();
  const ids = useId();
  const [code, setCode] = useState("");
  const [state, setState] = useState(authorization.callbackState ?? "");
  const result = useQuery(
    ProviderService.method.getProviderOAuth,
    { organizationId, authorizationId: authorization.id },
    {
      refetchInterval: (q) =>
        !q.state.error &&
        q.state.data?.authorization?.state === OAuthState.PENDING
          ? Math.max(
              5,
              q.state.data?.authorization?.pollIntervalSeconds ??
                authorization.pollIntervalSeconds ??
                5,
            ) * 1000
          : false,
    },
  );
  const auth = result.data?.authorization ?? authorization;
  const pending = auth.state === OAuthState.PENDING;
  const pollFlow =
    method === ProviderOAuthMethod.DEVICE ||
    method === ProviderOAuthMethod.BROWSER_POLL ||
    auth.pollIntervalSeconds !== undefined ||
    authorization.pollIntervalSeconds !== undefined;
  const callbackState = auth.callbackState ?? state;
  // Focus is chosen once so a state change never re-opens the modal.
  const [initialFocus] = useState(() =>
    authorization.state === OAuthState.PENDING &&
    method !== ProviderOAuthMethod.DEVICE &&
    method !== ProviderOAuthMethod.BROWSER_POLL &&
    authorization.pollIntervalSeconds === undefined
      ? "input"
      : "[data-initial-focus]",
  );
  useEffect(() => {
    if (auth.state === OAuthState.COMPLETED) void refresh();
  }, [auth.state, refresh]);
  let url: URL | undefined;
  try {
    const parsed = new URL(auth.authorizationUrl);
    if (
      parsed.protocol === "https:" ||
      (parsed.protocol === "http:" &&
        ["localhost", "127.0.0.1"].includes(parsed.hostname))
    )
      url = parsed;
  } catch {
    /* Invalid authorization destinations are not rendered as links. */
  }
  const terminal = oauthTerminalMessage(auth);
  const helpId = `${ids}-help`;
  const errorId = `${ids}-error`;
  const loadingStatus =
    result.isFetching && (result.isPending || !!result.error);
  // Automatic checks stop on a query error, so the shown status may be stale.
  const checksStopped = pending && !!result.error;
  return (
    <Dialog
      title={translate("providerAuthorization")}
      onClose={onClose}
      className="oauth-dialog"
      initialFocus={initialFocus}
    >
      <div className="oauth-summary" tabIndex={-1} data-initial-focus>
        <Badge tone={oauthStateTone[auth.state] ?? "neutral"}>
          {oauthStateLabel(auth.state)}
        </Badge>
        <p className="oauth-expiry">
          {translate("expires_8c3e7e", { value0: formatDate(auth.expiresAt) })}
        </p>
      </div>
      {pending && (
        <Notice>
          {translate("closingThisDialogDoesNotCancelAuthorization")}
        </Notice>
      )}
      <p className="mutation-status" role="status">
        {loadingStatus ? translate("checkingAuthorizationStatus") : ""}
      </p>
      <ErrorNotice error={result.error} retry={() => void result.refetch()} />
      {pending && (
        <>
          <section className="oauth-step" aria-labelledby={`${ids}-sign-in`}>
            <h3 id={`${ids}-sign-in`}>{translate("signInWithYourProvider")}</h3>
            {url ? (
              <a
                className="button primary"
                href={url.href}
                target="_blank"
                rel="noopener noreferrer"
              >
                {translate("openProviderSignIn")}
              </a>
            ) : (
              <p>{translate("providerSignInUnavailable")}</p>
            )}
            {auth.userCode && (
              <p>
                {translate("enterThisCodeAtTheProvider")}
                <strong>{auth.userCode}</strong>
              </p>
            )}
          </section>
          {pollFlow ? (
            <section className="oauth-step">
              {checksStopped ? (
                <Notice tone="warning">
                  {translate("automaticStatusChecksStopped")}
                </Notice>
              ) : (
                <p className="oauth-waiting" role="status">
                  {translate("waitingForProviderApprovalThisPageChecks")}
                </p>
              )}
            </section>
          ) : (
            <form
              className="oauth-step"
              aria-labelledby={`${ids}-finish`}
              onSubmit={(e) => {
                e.preventDefault();
                void action.run(
                  () =>
                    api.provider.completeProviderOAuth({
                      organizationId,
                      authorizationId: auth.id,
                      ...oauthCompletion(code, method, callbackState),
                    }),
                  onClose,
                  translate("providerConnected"),
                );
              }}
            >
              <h3 id={`${ids}-finish`}>{translate("finishAuthorization")}</h3>
              <p id={helpId}>
                {translate("pasteTheAuthorizationCodeTokenOrFull")}
              </p>
              <Field label={translate("authorizationCodeTokenOrCallbackUrl")}>
                <input
                  type="password"
                  value={code}
                  autoComplete="off"
                  spellCheck={false}
                  aria-describedby={
                    action.error ? `${helpId} ${errorId}` : helpId
                  }
                  aria-invalid={action.error ? true : undefined}
                  onChange={(e) => setCode(e.target.value)}
                />
              </Field>
              {!auth.callbackState && (
                <Field label={translate("authorizationState")}>
                  <input
                    value={state}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(e) => setState(e.target.value)}
                  />
                </Field>
              )}
              <div id={errorId}>
                <ErrorNotice error={action.error} />
              </div>
              <Button
                type="submit"
                variant="primary"
                disabled={action.pending || !code.trim() || !callbackState}
              >
                {translate("completeAuthorization")}
              </Button>
            </form>
          )}
          {pollFlow && <ErrorNotice error={action.error} />}
          {action.pending && (
            <p className="mutation-status" role="status">
              {translate("message_c5ca36dfc7")}
            </p>
          )}
          <div className="actions oauth-footer">
            <Button
              className="oauth-cancel"
              variant="ghost"
              disabled={action.pending}
              onClick={() => {
                void action.run(
                  () =>
                    api.provider.cancelProviderOAuth({
                      organizationId,
                      authorizationId: auth.id,
                    }),
                  onClose,
                  translate("authorizationCanceled"),
                );
              }}
            >
              {translate("cancelAuthorization")}
            </Button>
            <Button onClick={onClose}>{translate("close")}</Button>
          </div>
          <p className="oauth-helper">
            {translate("cancelAuthorizationStopsThisAttemptCloseLeaves")}
          </p>
        </>
      )}
      {!pending && (
        <>
          <Notice tone={terminal.tone}>{terminal.text}</Notice>
          <div className="actions oauth-footer">
            <Button
              variant="primary"
              onClick={() => {
                void refresh();
                onClose();
              }}
            >
              {translate("done")}
            </Button>
          </div>
        </>
      )}
    </Dialog>
  );
}

function ModelsDialog({
  organizationId,
  connection,
  onClose,
}: {
  organizationId: string;
  connection: ProviderConnection;
  onClose: () => void;
}) {
  useLocale();
  const [search, setSearch] = useState("");
  const query = useQuery(ProviderService.method.listProviderModels, {
    organizationId,
    connectionId: connection.id,
  });
  const models = query.data?.models.filter((m) =>
    `${m.id} ${m.displayName}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <Dialog
      title={translate("modelsFrom", { value0: connection.name })}
      onClose={onClose}
    >
      <Field label={translate("findAModel")}>
        <input
          autoFocus
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </Field>
      <ErrorNotice error={query.error} />
      {query.isPending ? (
        <Loading />
      ) : models?.length === 0 ? (
        <p>{translate("noMatchingModels")}</p>
      ) : (
        <ul>
          {models?.map((m) => (
            <li key={m.id}>
              <strong>{m.displayName || m.id}</strong>
              <br />
              <code>{m.id}</code>
              <p>
                {translate("contextOutput", {
                  value0: formatNumber(m.contextLimit),
                  value1: formatNumber(m.outputLimit),
                })}
              </p>
            </li>
          ))}
        </ul>
      )}
      <p>{translate("useAModelIdWhenConfiguringA")}</p>
      <Button onClick={onClose}>{translate("done")}</Button>
    </Dialog>
  );
}

type OAuthOptions = Pick<
  BeginProviderOAuthRequest,
  | "method"
  | "redirectUri"
  | "loginProvider"
  | "ssoRegion"
  | "apiRegion"
  | "startUrl"
  | "baseUrl"
  | "authBaseUrl"
  | "portalUrl"
  | "registerUrl"
  | "clientId"
>;
const oauthMethodLabels: Record<number, string> = {
  get [ProviderOAuthMethod.PKCE]() {
    return translate("browserSignInPkce");
  },
  get [ProviderOAuthMethod.DEVICE]() {
    return translate("deviceCode");
  },
  get [ProviderOAuthMethod.BROWSER_POLL]() {
    return translate("browserApproval");
  },
  get [ProviderOAuthMethod.EXPLICIT_TOKEN]() {
    return translate("providerToken");
  },
  get [ProviderOAuthMethod.NATIVE_CALLBACK]() {
    return translate("nativeCallback");
  },
};
const oauthUrlFieldKeys = new Set([
  "redirectUri",
  "startUrl",
  "baseUrl",
  "authBaseUrl",
  "portalUrl",
  "registerUrl",
]);
function isValidUrl(value: string) {
  try {
    new URL(value);
    return true;
  } catch {
    return false;
  }
}
function OAuthSetup({
  provider,
  providerId,
  pending,
  error,
  onClose,
  onSave,
}: {
  provider?: ProviderDefinition;
  providerId: string;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (options: OAuthOptions) => void;
}) {
  useLocale();
  const formRef = useRef<HTMLFormElement>(null);
  const errorId = useId();
  const beforeId = useId();
  const [method, setMethod] = useState<ProviderOAuthMethod | undefined>();
  const [values, setValues] = useState<Record<string, string>>({});
  const [invalid, setInvalid] = useState<Record<string, boolean>>({});
  const [advancedOpen, setAdvancedOpen] = useState(false);
  // Incremented once per failed submission so autofocus runs only after a
  // failed submit, never when editing clears a field's invalid flag.
  const [focusAttempt, setFocusAttempt] = useState(0);
  // The shared page action may still hold an unrelated earlier failure when the
  // dialog opens; only show failures produced by this dialog's own submission.
  const initialError = useRef(error);
  const visibleError = error === initialError.current ? undefined : error;
  const update = (key: string, value: string) => {
    setValues((old) => ({ ...old, [key]: value }));
    setInvalid((old) => (old[key] ? { ...old, [key]: false } : old));
  };
  useEffect(() => {
    if (focusAttempt === 0) return;
    // Move focus to the first field that failed validation, opening the
    // advanced disclosure first so the control is reachable.
    formRef.current
      ?.querySelector<HTMLElement>('[aria-invalid="true"]')
      ?.focus();
  }, [focusAttempt]);
  const fields = [
    ["loginProvider", translate("loginProvider")],
    ["ssoRegion", translate("ssoRegion")],
    ["apiRegion", translate("apiRegion")],
    ["startUrl", translate("startUrl")],
    ["baseUrl", translate("apiBaseUrl")],
    ["authBaseUrl", translate("authenticationBaseUrl")],
    ["portalUrl", translate("portalUrl")],
    ["registerUrl", translate("registrationUrl")],
    ["clientId", translate("clientId")],
  ] as const;
  const invalidHint = (key: string) =>
    invalid[key] ? (
      <span className="oauth-field-error">{translate("enterAValidUrl")}</span>
    ) : undefined;
  const describedBy = visibleError ? errorId : undefined;
  return (
    <Dialog
      title={translate("authorize", {
        value0: provider?.displayName ?? providerId,
      })}
      onClose={onClose}
      className="oauth-setup"
      initialFocus="[data-initial-focus]"
    >
      <p className="oauth-intro">
        {translate("useTheCallbackAndRegionalSettingsRequired")}
        <br />
        <small>{translate("leaveOptionalSettingsEmptyToUseThe")}</small>
      </p>
      <form
        ref={formRef}
        noValidate
        aria-describedby={describedBy}
        onSubmit={(e) => {
          e.preventDefault();
          if (pending) return;
          const options: OAuthOptions = { method };
          const failed: Record<string, boolean> = {};
          for (const [key, value] of Object.entries(values)) {
            const trimmed = value.trim();
            if (!trimmed) continue;
            if (oauthUrlFieldKeys.has(key) && !isValidUrl(trimmed))
              failed[key] = true;
            Object.assign(options, { [key]: trimmed });
          }
          if (Object.keys(failed).length) {
            if (Object.keys(failed).some((key) => key !== "redirectUri"))
              setAdvancedOpen(true);
            setInvalid(failed);
            setFocusAttempt((attempt) => attempt + 1);
            return;
          }
          setInvalid({});
          onSave(options);
        }}
      >
        <fieldset className="oauth-group">
          <legend>{translate("authorization")}</legend>
          {!!provider?.oauthMethods.length && (
            <Field
              label={translate("authorizationMethod")}
              hint={translate("availableMethodsDependOnTheSelectedProvider")}
            >
              <select
                aria-describedby={describedBy}
                value={method ?? ""}
                onChange={(e) =>
                  setMethod(e.target.value ? Number(e.target.value) : undefined)
                }
              >
                <option value="">{translate("providerDefault")}</option>
                {provider.oauthMethods
                  .filter((m) => m !== ProviderOAuthMethod.UNSPECIFIED)
                  .map((m) => (
                    <option key={m} value={m}>
                      {oauthMethodLabels[m] ?? translate("providerMethod")}
                    </option>
                  ))}
              </select>
            </Field>
          )}
          <div className="oauth-callback">
            <Field
              label={translate("callbackUri")}
              hint={
                <>
                  {translate("requiredForNativeCallbacksAndMostPkce")}{" "}
                  {translate("theServerDoesNotStartALocalListener")}
                  {invalidHint("redirectUri")}
                </>
              }
            >
              <input
                data-initial-focus
                type="url"
                autoComplete="off"
                aria-describedby={describedBy}
                aria-invalid={invalid.redirectUri ? true : undefined}
                value={values.redirectUri ?? ""}
                onChange={(e) => update("redirectUri", e.target.value)}
              />
            </Field>
          </div>
        </fieldset>
        <section className="oauth-before" aria-labelledby={beforeId}>
          <strong id={beforeId}>{translate("beforeYouContinue")}</strong>
          <p>{translate("useTheCallbackUriRegisteredWithYour")}</p>
        </section>
        <details
          className="oauth-advanced"
          open={advancedOpen}
          onToggle={(e) => setAdvancedOpen(e.currentTarget.open)}
        >
          <summary>
            <span>{translate("regionalAndAdvancedProviderSettings")}</span>
            <small>{translate("loginProviderRegionsUrlsAndClientId")}</small>
          </summary>
          {fields.map(([key, label]) => (
            <Field key={key} label={label} hint={invalidHint(key)}>
              <input
                type={oauthUrlFieldKeys.has(key) ? "url" : "text"}
                autoComplete="off"
                aria-describedby={describedBy}
                aria-invalid={invalid[key] ? true : undefined}
                value={values[key] ?? ""}
                onChange={(e) => update(key, e.target.value)}
              />
            </Field>
          ))}
        </details>
        <div id={errorId}>
          <ErrorNotice error={visibleError} />
        </div>
        <p className="visually-hidden" role="status">
          {pending ? translate("message_bbe5fc3b9e") : ""}
        </p>
        <div className="actions oauth-setup-footer">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button type="submit" variant="primary" disabled={pending}>
            {pending
              ? translate("message_bbe5fc3b9e")
              : translate("startAuthorization")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
// Native callbacks can carry provider-specific payloads, so preserve their full
// pasted value. PKCE callbacks carry a code and state; never navigate to that URL.
export function oauthCompletion(
  input: string,
  method: ProviderOAuthMethod | undefined,
  state: string,
): { code: string; state: string } {
  const value = input.trim();
  if (method === ProviderOAuthMethod.PKCE) {
    let callback: URL | undefined;
    try {
      callback = new URL(value);
    } catch {
      /* A standalone authorization code is also valid. */
    }
    if (callback) {
      const code = callback.searchParams.get("code");
      const returnedState = callback.searchParams.get("state");
      if (!code || !returnedState || returnedState !== state)
        throw new ConnectError(
          translate("authorizationCallbackDoesNotMatchThisLogin"),
          Code.InvalidArgument,
        );
      return { code, state: returnedState };
    }
  }
  return { code: value, state };
}
