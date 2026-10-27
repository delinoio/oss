// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber, enumLabel } from "./i18n";
import { pageHref } from "./routes";
import { useEffect, useId, useState } from "react";
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
} from "@aiprouter/client";
import { useApi, useRefresh, type PageProps, pageSize } from "./api";
import { useAction } from "./actions";
import { usePersonalResources } from "./resources";
import {
  PageHeader,
  Button,
  Card,
  Field,
  Dialog,
  ConfirmDialog,
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
      <HostedHelpers organizationId={organization.id} />
      {action.feedback}
      <Field label={translate("searchYourConnections")}>
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </Field>
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
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
                    {catalog.data?.providers.find((p) => p.id === c.providerId)
                      ?.displayName ?? c.providerId}
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
      <Pagination
        next={query.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
      />
      {edit && (
        <ConnectionEditor
          connection={edit === true ? undefined : edit}
          providers={catalog.data?.providers ?? []}
          error={action.error ?? catalog.error}
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
        <ConfirmDialog
          title={translate("disconnect_51710a", { value0: remove.name })}
          confirmLabel={translate("disconnectProvider")}
          onClose={() => setRemove(undefined)}
          pending={action.pending}
          error={action.error}
          onConfirm={() => {
            void action.run(
              () =>
                api.provider.deleteProviderConnection({
                  organizationId: organization.id,
                  connectionId: remove.id,
                }),
              () => setRemove(undefined),
              translate("providerDisconnected"),
            );
          }}
        >
          {translate("futureRequestsCanNoLongerUseThis", {
            value0:
              pools.data
                ?.filter((p) =>
                  p.members.some((m) => m.connectionId === remove.id),
                )
                .map((p) => p.name)
                .join(", ") ||
              (pools.isPending || routes.isPending
                ? translate("message_8bf7d8fc28")
                : translate("message_dc937b5989")),
            value1:
              routes.data
                ?.filter((r) =>
                  r.targets.some(
                    (t) =>
                      t.target.case === "connectionId" &&
                      t.target.value === remove.id,
                  ),
                )
                .map((r) => r.modelId)
                .join(", ") ||
              (pools.isPending || routes.isPending
                ? translate("message_8bf7d8fc28")
                : translate("message_dc937b5989")),
            value2:
              (pools.error || routes.error) && translate("message_0ff7c2b8d6"),
          })}
          <a href={pageHref("routing", organization.id)}>
            {translate("reviewRouting")}
          </a>
          {translate("beforeDisconnecting")}
        </ConfirmDialog>
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
function ConnectionEditor({
  connection,
  providers,
  onClose,
  onSave,
  pending,
  error,
}: {
  connection?: ProviderConnection;
  providers: ProviderDefinition[];
  onClose: () => void;
  onSave: (v: ConnectionValues) => void;
  pending: boolean;
  error: unknown;
}) {
  useLocale();
  const [configuration, setConfiguration] = useState(() =>
    configurationDraft(connection),
  );
  const [validationError, setValidationError] = useState("");
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
  return (
    <Dialog
      title={
        connection ? translate("editConnection") : translate("connectProvider")
      }
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          try {
            const advanced = configurationValues(configuration);
            setValidationError("");
            onSave({
              ...advanced,
              providerId,
              name: name.trim(),
              baseUrl: baseUrl.trim(),
              authKind,
              secret,
            });
          } catch (error) {
            setValidationError(
              error instanceof Error
                ? error.message
                : translate("checkTheConnectionSettings"),
            );
          }
        }}
      >
        {!connection && (
          <>
            <Field label={translate("findAProvider")}>
              <input
                type="search"
                autoFocus
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </Field>
            <Field label={translate("provider")}>
              <select
                required
                value={providerId}
                onChange={(e) => {
                  const p = providers.find((p) => p.id === e.target.value);
                  setProvider(e.target.value);
                  setConfiguration(configurationDraft());
                  setAuth(p?.authKinds[0] ?? ProviderAuthKind.UNSPECIFIED);
                }}
              >
                <option value="">
                  {translate("chooseASupportedProvider")}
                </option>
                {providers
                  .filter(
                    (p) =>
                      p.id === providerId ||
                      `${p.displayName} ${p.id}`
                        .toLowerCase()
                        .includes(search.toLowerCase()),
                  )
                  .map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.displayName}
                    </option>
                  ))}
              </select>
            </Field>
            <Field label={translate("authentication")}>
              <select
                required
                value={authKind}
                onChange={(e) => setAuth(Number(e.target.value))}
              >
                {!provider && (
                  <option value={0}>{translate("chooseAProviderFirst")}</option>
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
        <Field label={translate("accountLabel")}>
          <input
            required
            autoFocus={!!connection}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        {authKind !== ProviderAuthKind.OAUTH && (
          <Field
            label={translate("baseUrlOptional")}
            hint={translate("leaveEmptyToUseTheProviderDefault")}
          >
            <input
              type="url"
              value={baseUrl}
              onChange={(e) => setBase(e.target.value)}
            />
          </Field>
        )}
        {authKind === ProviderAuthKind.API_KEY && (
          <>
            <Field
              label={
                connection
                  ? translate("replacementApiKeyOptional")
                  : translate("apiKey")
              }
              hint={translate("storedSecretsAreNeverShownAgain")}
            >
              <input
                type={reveal ? "text" : "password"}
                autoComplete="new-password"
                required={!connection}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
            </Field>
            <Button aria-pressed={reveal} onClick={() => setReveal((v) => !v)}>
              {reveal ? translate("hideKey") : translate("revealKey")}
            </Button>
          </>
        )}
        {authKind === ProviderAuthKind.OAUTH && !connection ? (
          <Notice>
            {translate("afterAuthorizationEditThisConnectionToConfigure")}
          </Notice>
        ) : (
          <ConfigurationFields
            draft={configuration}
            onChange={setConfiguration}
            providerId={providerId}
            connection={connection}
          />
        )}
        {validationError && <Notice tone="danger">{validationError}</Notice>}
        <ErrorNotice error={error} />
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !providerId || !name.trim() || !authKind}
          >
            {pending
              ? translate("message_23e39291d6")
              : authKind === ProviderAuthKind.OAUTH && !connection
                ? translate("continueWithOauth")
                : translate("saveConnection")}
          </Button>
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
function OAuthSetup({
  provider,
  pending,
  error,
  onClose,
  onSave,
}: {
  provider?: ProviderDefinition;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (options: OAuthOptions) => void;
}) {
  useLocale();
  const [method, setMethod] = useState<ProviderOAuthMethod | undefined>();
  const [values, setValues] = useState<Record<string, string>>({});
  const update = (key: string, value: string) =>
    setValues((old) => ({ ...old, [key]: value }));
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
  return (
    <Dialog
      title={translate("authorize", {
        value0: provider?.displayName ?? "provider",
      })}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const options: OAuthOptions = { method };
          for (const [key, value] of Object.entries(values)) {
            if (value.trim()) Object.assign(options, { [key]: value.trim() });
          }
          onSave(options);
        }}
      >
        <Notice>
          {translate("useTheCallbackAndRegionalSettingsRequired")}
        </Notice>
        {!!provider?.oauthMethods.length && (
          <Field label={translate("authorizationMethod")}>
            <select
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
        <Field
          label={translate("callbackUri")}
          hint={translate("requiredForNativeCallbacksAndMostPkce")}
        >
          <input
            autoFocus
            type="url"
            autoComplete="off"
            value={values.redirectUri ?? ""}
            onChange={(e) => update("redirectUri", e.target.value)}
          />
        </Field>
        <details>
          <summary>{translate("regionalAndAdvancedProviderSettings")}</summary>
          {fields.map(([key, label]) => (
            <Field key={key} label={label}>
              <input
                type={key.toLowerCase().endsWith("url") ? "url" : "text"}
                autoComplete="off"
                value={values[key] ?? ""}
                onChange={(e) => update(key, e.target.value)}
              />
            </Field>
          ))}
        </details>
        <ErrorNotice error={error} />
        <div className="actions">
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
