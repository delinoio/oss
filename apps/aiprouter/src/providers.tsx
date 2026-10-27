// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, formatNumber, enumLabel } from "./i18n";
import { pageHref } from "./routes";
import { useEffect, useRef, useState } from "react";
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
function OAuthDialog({
  organizationId,
  authorization,
  method,
  onClose,
}: {
  organizationId: string;
  authorization: ProviderOAuth;
  method?: ProviderOAuthMethod;
  onClose: () => void;
}) {
  useLocale();
  const api = useApi();
  const refresh = useRefresh();
  const action = useAction();
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

  return (
    <Dialog title={translate("providerAuthorization")} onClose={onClose}>
      <Badge>{enumLabel(OAuthState[auth.state])}</Badge>
      <p>
        {translate("expiresClosingThisDialogDoesNotCancel", {
          value0: formatDate(auth.expiresAt),
        })}
      </p>
      {pending && (
        <>
          {url && (
            <a
              className="button primary"
              href={url.href}
              target="_blank"
              rel="noopener noreferrer"
            >
              {translate("openProviderSignIn")}
            </a>
          )}
          {auth.userCode && (
            <p>
              {translate("enterThisCodeAtTheProvider")}
              <strong>{auth.userCode}</strong>
            </p>
          )}
          {!pollFlow && (
            <form
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
              <p>{translate("pasteTheAuthorizationCodeTokenOrFull")}</p>
              <Field label={translate("authorizationCodeTokenOrCallbackUrl")}>
                <input
                  type="password"
                  value={code}
                  autoComplete="off"
                  onChange={(e) => setCode(e.target.value)}
                />
              </Field>
              {!auth.callbackState && (
                <Field label={translate("authorizationState")}>
                  <input
                    value={state}
                    autoComplete="off"
                    onChange={(e) => setState(e.target.value)}
                  />
                </Field>
              )}
              <Button
                type="submit"
                variant="primary"
                disabled={action.pending || !code || !callbackState}
              >
                {translate("completeAuthorization")}
              </Button>
            </form>
          )}
          {pollFlow && (
            <Notice>
              {translate("waitingForProviderApprovalThisPageChecks")}
            </Notice>
          )}
          <Button
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
        </>
      )}
      {auth.state === OAuthState.COMPLETED && auth.connectionId && (
        <Notice tone="success">
          {translate("providerConnectedYourConnectionIsReadyIn")}
        </Notice>
      )}
      {action.feedback}
      <ErrorNotice error={result.error} retry={() => void result.refetch()} />
      {!pending && (
        <Button
          onClick={() => {
            void refresh();
            onClose();
          }}
        >
          {translate("done")}
        </Button>
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
