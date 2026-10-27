// SPDX-License-Identifier: Apache-2.0
import { pageHref } from "./routes";
import { useEffect, useState } from "react";
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
  [ConnectionState.UNSPECIFIED]: "Unknown status",
  [ConnectionState.READY]: "Connected",
  [ConnectionState.AUTHORIZATION_REQUIRED]: "Reconnect required",
  [ConnectionState.DISABLED]: "Not connected",
  [ConnectionState.COOLDOWN]: "Cooling down",
  [ConnectionState.ERROR]: "Connection failed",
};
export const authLabel: Record<number, string> = {
  [ProviderAuthKind.API_KEY]: "API key",
  [ProviderAuthKind.OAUTH]: "OAuth",
  [ProviderAuthKind.NO_AUTH]: "No authentication",
};
export function ProvidersPage({ organization }: PageProps) {
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
        title="Your providers"
        description={`Only yours in ${organization.name}.`}
        action={
          <Button variant="primary" onClick={() => setEdit(true)}>
            Connect provider
          </Button>
        }
      />
      <Notice>
        Private to you. Your providers and routing are separate in every
        organization.
      </Notice>
      <div className="actions">
        <a className="button" href={pageHref("pools", organization.id)}>
          Manage account pools
        </a>
      </div>
      <HostedHelpers organizationId={organization.id} />
      {action.feedback}
      <Field label="Search your connections">
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
            search ? "No matching connections" : "Connect your first provider"
          }
          action={
            search ? (
              <Button onClick={() => setSearch("")}>Clear filters</Button>
            ) : undefined
          }
        >
          Only you can use these provider accounts.
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
                {connectionLabel[c.state] ?? "Unknown status"}
              </Badge>
              <p>{authLabel[c.authKind]} · Personal account</p>
              <p>Added {formatDate(c.createdAt)}</p>
              {c.cooldownUntil && (
                <p>Cooling down until {formatDate(c.cooldownUntil)}</p>
              )}
              <div className="actions">
                <Button onClick={() => setEdit(c)}>Edit connection</Button>
                <Button onClick={() => setModels(c)}>View models</Button>
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
                    Reconnect
                  </Button>
                )}
                <Button variant="danger" onClick={() => setRemove(c)}>
                  Disconnect
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
              "Connection saved.",
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
              "Authorization started.",
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
        <ConfirmDialog
          title={`Disconnect ${remove.name}?`}
          confirmLabel="Disconnect provider"
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
              "Provider disconnected.",
            );
          }}
        >
          Future requests can no longer use this connection. Your external
          provider account is not deleted. Affected pools:{" "}
          {pools.data
            ?.filter((p) => p.members.some((m) => m.connectionId === remove.id))
            .map((p) => p.name)
            .join(", ") ||
            (pools.isPending || routes.isPending
              ? "Checking dependencies…"
              : "None")}
          . Direct routes:{" "}
          {routes.data
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
              ? "Checking dependencies…"
              : "None")}
          . Routes through these pools also need review.{" "}
          {(pools.error || routes.error) &&
            "Dependencies could not be loaded. Review routing before disconnecting. "}
          <a href={pageHref("routing", organization.id)}>Review routing</a>{" "}
          before disconnecting.
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
      title={connection ? "Edit connection" : "Connect provider"}
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
                : "Check the connection settings.",
            );
          }
        }}
      >
        {!connection && (
          <>
            <Field label="Find a provider">
              <input
                type="search"
                autoFocus
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </Field>
            <Field label="Provider">
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
                <option value="">Choose a supported provider</option>
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
            <Field label="Authentication">
              <select
                required
                value={authKind}
                onChange={(e) => setAuth(Number(e.target.value))}
              >
                {!provider && (
                  <option value={0}>Choose a provider first</option>
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
        <Field label="Account label">
          <input
            required
            autoFocus={!!connection}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        {authKind !== ProviderAuthKind.OAUTH && (
          <Field
            label="Base URL (optional)"
            hint="Leave empty to use the provider default. Private or local destinations require operator approval."
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
              label={connection ? "Replacement API key (optional)" : "API key"}
              hint="Stored secrets are never shown again."
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
              {reveal ? "Hide key" : "Reveal key"}
            </Button>
          </>
        )}
        {authKind === ProviderAuthKind.OAUTH && !connection ? (
          <Notice>
            After authorization, edit this connection to configure API formats,
            policies, or headers.
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
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !providerId || !name.trim() || !authKind}
          >
            {pending
              ? "Saving…"
              : authKind === ProviderAuthKind.OAUTH && !connection
                ? "Continue with OAuth"
                : "Save connection"}
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
    <Dialog title="Provider authorization" onClose={onClose}>
      <Badge>{OAuthState[auth.state]?.toLowerCase() ?? "Unknown status"}</Badge>
      <p>
        Expires {formatDate(auth.expiresAt)}. Closing this dialog does not
        cancel authorization.
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
              Open provider sign-in
            </a>
          )}
          {auth.userCode && (
            <p>
              Enter this code at the provider: <strong>{auth.userCode}</strong>
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
                  "Provider connected.",
                );
              }}
            >
              <p>
                Paste the authorization code, token, or full callback URL
                returned by your provider. This server does not open a local
                callback listener.
              </p>
              <Field label="Authorization code, token, or callback URL">
                <input
                  type="password"
                  value={code}
                  autoComplete="off"
                  onChange={(e) => setCode(e.target.value)}
                />
              </Field>
              {!auth.callbackState && (
                <Field label="Authorization state">
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
                Complete authorization
              </Button>
            </form>
          )}
          {pollFlow && (
            <Notice>
              Waiting for provider approval. This page checks the provider at
              its current polling interval.
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
                "Authorization canceled.",
              );
            }}
          >
            Cancel authorization
          </Button>
        </>
      )}
      {auth.state === OAuthState.COMPLETED && auth.connectionId && (
        <Notice tone="success">
          Provider connected. Your connection is ready in this organization.
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
          Done
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
  const [search, setSearch] = useState("");
  const query = useQuery(ProviderService.method.listProviderModels, {
    organizationId,
    connectionId: connection.id,
  });
  const models = query.data?.models.filter((m) =>
    `${m.id} ${m.displayName}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <Dialog title={`Models from ${connection.name}`} onClose={onClose}>
      <Field label="Find a model">
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
        <p>No matching models.</p>
      ) : (
        <ul>
          {models?.map((m) => (
            <li key={m.id}>
              <strong>{m.displayName || m.id}</strong>
              <br />
              <code>{m.id}</code>
              <p>
                Context: {m.contextLimit?.toString() ?? "Unknown"} · Output:{" "}
                {m.outputLimit?.toString() ?? "Unknown"}
              </p>
            </li>
          ))}
        </ul>
      )}
      <p>Use a model ID when configuring a route for this connection.</p>
      <Button onClick={onClose}>Done</Button>
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
  [ProviderOAuthMethod.PKCE]: "Browser sign-in (PKCE)",
  [ProviderOAuthMethod.DEVICE]: "Device code",
  [ProviderOAuthMethod.BROWSER_POLL]: "Browser approval",
  [ProviderOAuthMethod.EXPLICIT_TOKEN]: "Provider token",
  [ProviderOAuthMethod.NATIVE_CALLBACK]: "Native callback",
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
  const [method, setMethod] = useState<ProviderOAuthMethod | undefined>();
  const [values, setValues] = useState<Record<string, string>>({});
  const update = (key: string, value: string) =>
    setValues((old) => ({ ...old, [key]: value }));
  const fields = [
    ["loginProvider", "Login provider"],
    ["ssoRegion", "SSO region"],
    ["apiRegion", "API region"],
    ["startUrl", "Start URL"],
    ["baseUrl", "API base URL"],
    ["authBaseUrl", "Authentication base URL"],
    ["portalUrl", "Portal URL"],
    ["registerUrl", "Registration URL"],
    ["clientId", "Client ID"],
  ] as const;
  return (
    <Dialog
      title={`Authorize ${provider?.displayName ?? "provider"}`}
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
          Use the callback and regional settings required by your provider.
          Leave optional settings empty to use the provider defaults.
        </Notice>
        {!!provider?.oauthMethods.length && (
          <Field label="Authorization method">
            <select
              value={method ?? ""}
              onChange={(e) =>
                setMethod(e.target.value ? Number(e.target.value) : undefined)
              }
            >
              <option value="">Provider default</option>
              {provider.oauthMethods
                .filter((m) => m !== ProviderOAuthMethod.UNSPECIFIED)
                .map((m) => (
                  <option key={m} value={m}>
                    {oauthMethodLabels[m] ?? "Provider method"}
                  </option>
                ))}
            </select>
          </Field>
        )}
        <Field
          label="Callback URI"
          hint="Required for native callbacks and most PKCE providers. Enter the exact registered callback URI. The server does not start a local listener; paste the resulting callback in the next step."
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
          <summary>Regional and advanced provider settings</summary>
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
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={pending}>
            {pending ? "Starting…" : "Start authorization"}
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
          "Authorization callback does not match this login.",
          Code.InvalidArgument,
        );
      return { code, state: returnedState };
    }
  }
  return { code: value, state };
}
