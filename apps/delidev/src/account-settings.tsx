import { OpenRouterOAuth, useOpenRouterOAuth, type OpenRouterOAuthFlow } from "./account-oauth";
import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  AccountQuery,
  AccountTypeFilter,
  FailureCode,
  clientFailure,
  ConfigurationQuery,
  EntityKind,
  ResourceQuery,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import "./api-account.css";
import { AccountConnection } from "./account-connection";
import { Authentication } from "./configuration-fields";
import { document, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { SubscriptionAccounts } from "./subscription-accounts";

export enum AccountSettingsSection {
  Api = "api",
  Subscription = "subscription",
}

export interface AccountProviderSummary {
  providerId: string;
  displayName: string;
  enabled: boolean;
  provider: Resource;
  keyGuidance: string;
  documentationUrl: string;
  oauthAvailable?: boolean;
}

export interface AccountProviderPicker {
  ready: boolean;
  loaded: boolean;
  fetching: boolean;
  error?: unknown;
  pageToken: string;
  nextPageToken: string;
  retry: () => void;
  next: () => void;
  first: () => void;
}

export interface AccountSettingsProps {
  oauth?: OpenRouterOAuthFlow;
  section: AccountSettingsSection;
  active: boolean;
  accountTypeFilteringReady: boolean;
  accountTypeFilteringProblem?: unknown;
  accountTypeFilteringLoading?: boolean;
  accountTypeFilteringFetching?: boolean;
  retryAccountCapabilities?: () => void;
  providerIdFilter?: string;
  clearProviderFilter: () => void;
  providers: readonly AccountProviderSummary[];
  /** One unfiltered server page of enabled API providers, independent of account filters. */
  eligibleProviders: readonly AccountProviderSummary[];
  providerSearch: string;
  setProviderSearch: (query: string) => void;
  providerSearchLoading: boolean;
  providerSearchError?: unknown;
  providerPicker: AccountProviderPicker;
  providerFilterHasMore?: boolean;
  loadMoreProviderFilters?: () => void;
  setProviderFilter: (providerId: string, provider?: AccountProviderSummary) => void;
  subscriptionProviderResources: readonly Resource[];
  subscriptionProviderManagement: ReactNode;
  openApiProviders: (providerId?: string) => void;
  manageAccount: (resource: Resource) => void;
  editAccount: (resource: Resource) => void;
  deleteAccount: (resource: Resource) => void;
  onWorkflowReadyChange?: (active: boolean) => void;
  startApiWizard?: { key: string; providerId: string; provider?: AccountProviderSummary };
  providerHint?: AccountProviderSummary;
}

enum WizardFocus { None, Picker, Account, Provider }

enum WizardStep {
  Provider,
  Account,
  Created,
}

function validAccount(resource: Resource | undefined, providerId: string, alias: string): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || !resource.id || resource.schemaVersion !== 1 || resource.revision < 1n) return false;
  const value = document(resource);
  return value.provider_id === providerId && value.type === "api" && value.alias === alias;
}

function validAccountObservation(resource: Resource | undefined, id: string, providerId: string, minimumRevision: bigint): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || resource.id !== id || resource.schemaVersion !== 1 || resource.revision < minimumRevision) return false;
  const data = document(resource);
  return data.type === "api" && data.provider_id === providerId;
}

function providerContract(provider: AccountProviderSummary): { id: string; authentication: string; protocol: string; endpoint: string; enabled: boolean } {
  const data = document(provider.provider);
  return { id: provider.providerId, authentication: text(data.authentication), protocol: text(data.protocol), endpoint: text(data.endpoint), enabled: provider.enabled };
}

function providerContractMatches(expected: ReturnType<typeof providerContract>, resource: Resource | undefined): boolean {
  if (!resource || resource.kind !== EntityKind.PROVIDER || resource.schemaVersion !== 1 || resource.id !== expected.id) return false;
  const data = document(resource);
  const enabled = data.enabled !== false;
  return text(data.authentication) === expected.authentication && text(data.protocol) === expected.protocol &&
    text(data.endpoint) === expected.endpoint && enabled === expected.enabled && expected.enabled;
}

function sameProviderContract(left: ReturnType<typeof providerContract> | undefined, right: ReturnType<typeof providerContract>): boolean {
  return Boolean(left && left.id === right.id && left.authentication === right.authentication && left.protocol === right.protocol &&
    left.endpoint === right.endpoint && left.enabled === right.enabled);
}

function AccountCreationWizard({
  oauth: suppliedOAuth,
  openEdit,
  active,
  accountTypeFilteringReady,
  initialProvider,
  providers,
  eligibleProviders,
  picker,
  close,
  openProviders,
  openManage,
  saved,
}: {
  oauth?: OpenRouterOAuthFlow;
  openEdit: (resource: Resource) => void;
  active: boolean;
  accountTypeFilteringReady: boolean;
  initialProvider?: AccountProviderSummary;
  providers: readonly AccountProviderSummary[];
  eligibleProviders: readonly AccountProviderSummary[];
  picker: AccountProviderPicker;
  close: () => void;
  openProviders: () => void;
  openManage: (resource: Resource) => void;
  saved: (resource: Resource) => void;
}) {
  const localOAuth = useOpenRouterOAuth();
  const oauth = suppliedOAuth ?? localOAuth;
  const [step, setStep] = useState(initialProvider ? WizardStep.Account : WizardStep.Provider);
  const [providerId, setProviderId] = useState(initialProvider?.providerId ?? "");
  // Keep the clicked contract authoritative when independent inventory pages retain different snapshots.
  const [selectedHint, setSelectedHint] = useState(initialProvider);
  const heading = useRef<HTMLHeadingElement>(null);
  const providerButtons = useRef(new Map<string, HTMLButtonElement>());
  const [focusTarget, setFocusTarget] = useState(initialProvider ? WizardFocus.Account : WizardFocus.Picker);
  const [alias, setAlias] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [providerChecking, setProviderChecking] = useState(false);
  const [providerMismatch, setProviderMismatch] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [excludeAutomatic, setExcludeAutomatic] = useState(false);
  const [recoveryNotifications, setRecoveryNotifications] = useState(true);
  const [advanced, setAdvanced] = useState(false);
  const [createdAccount, setCreatedAccount] = useState<Resource>();
  const [connectionKey, setConnectionKey] = useState("");
  const [connected, setConnected] = useState<Resource>();
  const [unknownResponse, setUnknownResponse] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const handoffKey = useRef<string | undefined>(undefined);
  const handoffProvider = useRef<ReturnType<typeof providerContract> | undefined>(undefined);
  const generation = useRef(0);
  const activeRef = useRef(active);
  activeRef.current = active;
  const createGeneration = useRef<number | undefined>(undefined);
  const connectGeneration = useRef<number | undefined>(undefined);
  const autoConnectAllowed = useRef(false);
  const lastProviderContract = useRef<ReturnType<typeof providerContract> | undefined>(undefined);
  const createAttempt = useRef(0);
  const acceptedCreateAttempt = useRef(0);
  const autoConnectStarted = useRef<number | undefined>(undefined);
  const [autoConnect, setAutoConnect] = useState<number>();
  const options = useMemo(() => eligibleProviders.filter((provider) =>
    provider.providerId && provider.enabled && document(provider.provider).protocol !== "native-subscription"), [eligibleProviders]);
  const selectedProvider = selectedHint?.providerId === providerId ? selectedHint :
    providers.find((provider) => provider.providerId === providerId) ?? eligibleProviders.find((provider) => provider.providerId === providerId);
  useEffect(() => {
    if (!active) { setFocusTarget(WizardFocus.None); return; }
    if ((focusTarget === WizardFocus.Account && step === WizardStep.Account) ||
      (focusTarget === WizardFocus.Picker && step === WizardStep.Provider)) heading.current?.focus();
    if (focusTarget === WizardFocus.Provider && step === WizardStep.Provider) providerButtons.current.get(providerId)?.focus();
    setFocusTarget(WizardFocus.None);
  }, [active, focusTarget, providerId, step]);
  const selectedProviderDocument = document(selectedProvider?.provider);
  const selectedAuthentication = text(selectedProviderDocument.authentication);
  const selectedProviderContract = selectedProvider ? providerContract(selectedProvider) : undefined;
  const providerContractRef = useRef(selectedProviderContract);
  providerContractRef.current = selectedProviderContract;
  const capabilityRef = useRef(accountTypeFilteringReady);
  capabilityRef.current = accountTypeFilteringReady;
  const providerRead = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: providerId }, { enabled: active && step === WizardStep.Account && Boolean(providerId) });
  const verifyProvider = async (expected: ReturnType<typeof providerContract>): Promise<boolean> => {
    try {
      const checked = await providerRead.refetch();
      return !checked.error && providerContractMatches(expected, checked.data?.resource) && sameProviderContract(providerContractRef.current, expected);
    } catch { return false; }
  };
  const keyless = selectedAuthentication === Authentication.Keyless;
  const apiKeyValid = keyless || /^[!-~]{1,8192}$/.test(apiKey);
  const aliasValid = alias.trim().length > 0 && !alias.includes(String.fromCharCode(0)) && new TextEncoder().encode(alias).byteLength <= 256;
  const currentId = createdAccount?.id ?? "pending";
  const clearHandoff = () => {
    handoffKey.current = undefined;
    handoffProvider.current = undefined;
  };

  const create = useRetainedMutation("api-account-wizard:create", ConfigurationQuery.saveConfiguration, (result, request) => {
    let expected: { providerId: string; alias: string } | undefined;
    try {
      const body = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(request.documentJson)) as Record<string, unknown>;
      expected = { providerId: text(body.provider_id), alias: text(body.alias) };
    } catch { /* Malformed request data cannot authorize a follow-up connection. */ }
    const resource = result.resource;
    if (request.kind !== EntityKind.ACCOUNT || !request.mutation || result.requestId !== request.mutation.requestId ||
      !expected || !validAccount(resource, expected.providerId, expected.alias)) {
      clearHandoff();
      setUnknownResponse(true);
      return;
    }
    acceptedCreateAttempt.current = createAttempt.current;
    setCreatedAccount(resource);
    setStep(WizardStep.Created);
    saved(resource);
    if (autoConnectAllowed.current && accountTypeFilteringReady && activeRef.current && createGeneration.current === generation.current) {
      setAutoConnect(generation.current);
    } else {
      autoConnectAllowed.current = false;
      clearHandoff();
    }
  });
  const connect = useRetainedMutation("account-connect:" + currentId, AccountQuery.connectAccount, (result, request) => {
    const expectedRevision = request.mutation?.expectedRevision ?? createdAccount?.revision ?? 0n;
    const returnedConnection = text(object(document(result.account).connection).id);
    const validResult = Boolean(createdAccount && result.requestId === request.mutation?.requestId &&
      validAccountObservation(result.account, request.mutation?.id ?? "", text(document(createdAccount).provider_id), expectedRevision) &&
      (result.replayed || (Boolean(returnedConnection) && result.account.revision > expectedRevision)));
    if (!validResult) {
      setUnknownResponse(true);
      return;
    }
    if (!activeRef.current || connectGeneration.current !== generation.current) return;
    setConnected(result.account);
  });

  useEffect(() => {
    if (!create.input || (!create.busy && !create.uncertain)) return;
    const retained = create.input as { kind?: EntityKind; documentJson?: Uint8Array };
    if (retained.kind !== EntityKind.ACCOUNT || !retained.documentJson?.byteLength) return;
    try {
      const body = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(retained.documentJson)) as Record<string, unknown>;
      if (text(body.type) !== "api" || !text(body.provider_id) || !text(body.alias)) return;
      if (!providerId) setProviderId(text(body.provider_id));
      if (!alias) setAlias(text(body.alias));
      setEnabled(body.enabled === true);
      setExcludeAutomatic(body.exclude_automatic === true);
      setRecoveryNotifications(body.recovery_notifications === true);
      setStep(WizardStep.Account);
    } catch { /* The exact retained request remains available for inspection and retry. */ }
  }, [alias, create.busy, create.input, create.uncertain, providerId]);

  useEffect(() => {
    if (active) return;
    generation.current += 1;
    clearHandoff();
    setApiKey("");
    setConnectionKey("");
    setProviderChecking(false);
    autoConnectStarted.current = undefined;
    setAutoConnect(undefined);
  }, [active]);

  useEffect(() => () => {
    generation.current += 1;
    clearHandoff();
    autoConnectAllowed.current = false;
  }, []);

  useEffect(() => {
    const next = selectedProviderContract;
    const previous = lastProviderContract.current;
    if (previous && next && previous.id === next.id &&
      (previous.authentication !== next.authentication || previous.protocol !== next.protocol || previous.endpoint !== next.endpoint || (previous.enabled && !next.enabled))) {
      generation.current += 1;
      clearHandoff();
      autoConnectAllowed.current = false;
      setApiKey("");
      setConnectionKey("");
      setAutoConnect(undefined);
    }
    lastProviderContract.current = next;
  }, [selectedProviderContract]);

  useEffect(() => {
    if (autoConnect === undefined) return;
    if (autoConnectStarted.current === autoConnect) return;
    autoConnectStarted.current = autoConnect;
    if (!activeRef.current || !accountTypeFilteringReady || autoConnect !== generation.current || !createdAccount || !selectedProvider) {
      clearHandoff();
      setAutoConnect(undefined);
      return;
    }
    const originalProvider = handoffProvider.current;
    if (!selectedProvider.enabled || !originalProvider || originalProvider.id !== selectedProvider.providerId ||
      originalProvider.authentication !== selectedAuthentication || originalProvider.protocol !== text(selectedProviderDocument.protocol) ||
      originalProvider.endpoint !== text(selectedProviderDocument.endpoint)) {
      clearHandoff();
      autoConnectAllowed.current = false;
      setAutoConnect(undefined);
      return;
    }
    setAutoConnect(undefined);
    void (async () => {
      setProviderChecking(true);
      const providerIsCurrent = await verifyProvider(originalProvider);
      setProviderChecking(false);
      if (!activeRef.current || generation.current !== autoConnect || !capabilityRef.current) {
        clearHandoff();
        autoConnectAllowed.current = false;
        return;
      }
      if (!providerIsCurrent || !sameProviderContract(providerContractRef.current, originalProvider)) {
        clearHandoff();
        autoConnectAllowed.current = false;
        setProviderMismatch(true);
        return;
      }
      const secret = handoffKey.current;
      clearHandoff();
      autoConnectAllowed.current = false;
      if (!keyless && !secret) return;
      connectGeneration.current = generation.current;
      void connect.send({
        mutation: { id: createdAccount.id, expectedRevision: createdAccount.revision, requestId: newRequestId() },
        keyless,
        apiKey: secret ? new TextEncoder().encode(secret) : new Uint8Array(),
      });
    })();
  }, [accountTypeFilteringReady, autoConnect, connect.send, createdAccount, keyless, selectedAuthentication, selectedProvider, selectedProviderDocument.endpoint, selectedProviderDocument.protocol]);

  const navigateBack = () => {
    if (create.busy || create.uncertain || connect.busy || connect.uncertain) return;
    clearHandoff();
    close();
  };
  const pickProvider = (provider: AccountProviderSummary) => {
    if (!activeRef.current || !accountTypeFilteringReady || !picker.ready || step !== WizardStep.Provider) return;
    generation.current += 1;
    clearHandoff();
    setApiKey("");
    setProviderId(provider.providerId);
    setSelectedHint(provider);
    setAutoConnect(undefined);
    setStep(WizardStep.Account);
    setFocusTarget(WizardFocus.Account);
    if (provider.oauthAvailable && oauth.available) oauth.start(provider);
  };
  const returnToProviders = () => {
    if (providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain) return;
    generation.current += 1;
    clearHandoff();
    setApiKey("");
    setStep(WizardStep.Provider);
    setFocusTarget(WizardFocus.Provider);
  };
  const createAccount = async () => {
    setAttempted(true);
    if (!accountTypeFilteringReady || unknownResponse || providerChecking || !selectedProvider || !selectedProvider.enabled || !aliasValid || !apiKeyValid || create.busy || create.uncertain) return;
    const requestGeneration = generation.current;
    const requestAlias = alias;
    const requestProviderId = selectedProvider.providerId;
    const requestProviderContract = providerContract(selectedProvider);
    const requestEnabled = enabled;
    const requestExcludeAutomatic = excludeAutomatic;
    const requestRecoveryNotifications = recoveryNotifications;
    handoffKey.current = keyless ? undefined : apiKey;
    handoffProvider.current = requestProviderContract;
    setApiKey("");
    setUnknownResponse(false);
    setProviderMismatch(false);
    setProviderChecking(true);
    autoConnectAllowed.current = false;
    const matches = await verifyProvider(requestProviderContract);
    setProviderChecking(false);
    if (!activeRef.current || generation.current !== requestGeneration || !capabilityRef.current) {
      clearHandoff();
      return;
    }
    if (!matches) {
      clearHandoff();
      setProviderMismatch(true);
      return;
    }
    const requestId = newRequestId();
    createGeneration.current = requestGeneration;
    const attempt = ++createAttempt.current;
    acceptedCreateAttempt.current = 0;
    autoConnectAllowed.current = true;
    void create.send({
      mutation: { id: "", expectedRevision: 0n, requestId },
      kind: EntityKind.ACCOUNT,
      schemaVersion: 1,
      documentJson: new TextEncoder().encode(JSON.stringify({
        alias: requestAlias,
        provider_id: requestProviderId,
        type: "api",
        enabled: requestEnabled,
        exclude_automatic: requestExcludeAutomatic,
        recovery_notifications: requestRecoveryNotifications,
        health: "disconnected",
        quota: [],
        confirmed_exhausted: false,
      })),
    }).then(() => {
      if (acceptedCreateAttempt.current !== attempt) {
        autoConnectAllowed.current = false;
        clearHandoff();
      }
    });
  };
  const connectExisting = async () => {
    if (!accountTypeFilteringReady || unknownResponse || providerChecking || !createdAccount || !selectedProvider || !selectedProvider.enabled || connect.busy || connect.uncertain) return;
    if (!keyless && !/^[!-~]{1,8192}$/.test(connectionKey)) return;
    const requestGeneration = generation.current;
    const requestProviderContract = providerContract(selectedProvider);
    const requestAccount = connected ?? createdAccount;
    const apiKeyBytes = keyless ? new Uint8Array() : new TextEncoder().encode(connectionKey);
    setConnectionKey("");
    setProviderMismatch(false);
    setProviderChecking(true);
    const matches = await verifyProvider(requestProviderContract);
    setProviderChecking(false);
    if (!activeRef.current || generation.current !== requestGeneration || !capabilityRef.current) return;
    if (!matches) {
      setProviderMismatch(true);
      return;
    }
    const input = {
      mutation: { id: requestAccount.id, expectedRevision: requestAccount.revision, requestId: newRequestId() },
      keyless,
      apiKey: apiKeyBytes,
    };
    connectGeneration.current = generation.current;
    setUnknownResponse(false);
    void connect.send(input);
  };
  const retryConnection = () => {
    if (!activeRef.current || !accountTypeFilteringReady || connect.busy || !connect.uncertain) return;
    connectGeneration.current = generation.current;
    void connect.retry();
  };

  if (createdAccount) {
    const current = connected ?? createdAccount;
    const data = document(current);
    const hasCredentials = Boolean(text(object(data.connection).id));
    const validationState = text(object(data.validation).state) || "not yet observed";
    const validationLabel = text(data.health) === "unverified" ? "validation required" : `validation ${validationState}`;
    return <section className="account-wizard api-keys-view" aria-labelledby="api-account-created-title">
      <button className="api-entry-back" type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={navigateBack}>Back to AI API Keys</button>
      <SettingsHeading title="AI API Keys" /><h2 id="api-account-created-title">{resourceName(current)}</h2>
      <p>Entry saved. Credential connection is separate from validation and model discovery.</p>
      {!accountTypeFilteringReady ? <p role="status">Connection is paused until this server reports the required provider inventory and account-type filtering capabilities. The saved entry and any exact pending request are retained.</p> : null}
      {create.busy ? <p role="status">Creating entry…</p> : null}
      {create.uncertain ? <p role="status">Result not confirmed. Retry the same entry creation to inspect the original request outcome.</p> : null}
      {hasCredentials ? <p role="status">Connected · health {text(data.health) || "unknown"} · {validationLabel}.</p> : <p>Connection: {text(data.health) || "disconnected"}</p>}
      {connect.busy ? <p role="status">Connecting entry…</p> : null}
      {connect.uncertain ? <p role="status">Result not confirmed. The original connection request is retained for exact retry.</p> : null}
      {connect.error && !connect.uncertain ? <p role="status">{keyless ? "Entry created; connection failed. Retry the local endpoint connection when ready." : "Entry created; connection failed. Re-enter the key and retry connection when ready."}</p> : null}
      {!hasCredentials && selectedProvider && selectedProvider.enabled ? keyless ? <p>Connect to this local endpoint on the selected server.</p> : <label>API key<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} disabled={providerChecking} value={connectionKey} onChange={(event) => setConnectionKey(event.target.value)} /></label> : !hasCredentials && selectedProvider ? <p role="status">The selected provider is off. Enable it in API Providers before connecting.</p> : !hasCredentials ? <p role="status">The selected provider is unavailable. Refresh API Providers before connecting.</p> : null}
      {!hasCredentials ? <div className="actions"><button className="primary" type="button" disabled={!accountTypeFilteringReady || providerChecking || unknownResponse || !selectedProvider?.enabled || (!keyless && !/^[!-~]{1,8192}$/.test(connectionKey)) || connect.busy || connect.uncertain} onClick={connectExisting}>{providerChecking ? "Checking provider…" : keyless ? "Connect local endpoint" : "Connect API key"}</button>{connect.uncertain ? <button type="button" disabled={!accountTypeFilteringReady || connect.busy} onClick={retryConnection}>Retry the same connection</button> : null}</div> : null}
      {providerMismatch ? <p role="alert">{keyless ? "This provider changed or is no longer available. Review the current API Providers entry before connecting." : "This provider changed or is no longer available. The API key was cleared. Review the current API Providers entry before connecting."}</p> : null}
      {providerChecking ? <p role="status">Checking the current provider settings…</p> : null}
      <Problem error={providerRead.error} />
      {unknownResponse ? <p role="alert">The server acknowledged a request without a matching entry result. Inspect the original request before starting another operation.</p> : null}
      <Problem error={connect.error} />
      <div className="actions"><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={() => openManage(current)}>Manage connection</button><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={close}>Done</button></div>
    </section>;
  }

  if (oauth.view) return <OpenRouterOAuth flow={oauth} back={returnToProviders} manual={() => { setStep(WizardStep.Account); setFocusTarget(WizardFocus.Account); }} edit={resource => { saved(resource); openEdit(resource); }} manage={resource => { saved(resource); openManage(resource); }} done={() => { if (oauth.view?.account) saved(oauth.view.account); close(); }} />;

  return <section className="account-wizard api-keys-view" aria-labelledby="api-account-wizard-title">
    <button className="api-entry-back" type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={navigateBack}>Back to AI API Keys</button>
    <SettingsHeading title="AI API Keys" /><h2 id="api-account-wizard-title">Add AI API key</h2>
    {step === WizardStep.Provider ? <>
      <h2 ref={heading} tabIndex={-1}>Choose an API provider</h2>
      <p>Select a provider to connect your entry.</p>
      {picker.fetching ? <p role="status">Loading providers…</p> : null}
      <Problem error={picker.error} />
      {picker.error && clientFailure(picker.error).code === FailureCode.PermissionDenied ? <p role="status">Provider inventory access is denied. Check this device’s permission on the selected server.</p> : null}
      {picker.error ? <button type="button" disabled={picker.fetching} onClick={picker.retry}>Retry providers</button> : null}
      {picker.error && picker.loaded ? <p className="notice" role="status">Refresh failed. Showing the last successfully loaded providers.</p> : null}
      {picker.loaded && (!accountTypeFilteringReady || !picker.ready) ? <p role="status">Provider choices are unavailable because this server does not report the required provider inventory and account-type filtering capabilities. Update the server before continuing.</p> : null}
      {accountTypeFilteringReady && picker.ready ? <>
        <div className="account-provider-choices">{options.map((provider) => <button type="button" className="account-provider-action" key={provider.providerId} ref={(button) => { if (button) providerButtons.current.set(provider.providerId, button); else providerButtons.current.delete(provider.providerId); }} onClick={() => pickProvider(provider)}>
          <span><strong>{provider.displayName}</strong><span className="account-provider-method">{provider.oauthAvailable && oauth.available ? "Browser sign-in" : document(provider.provider).authentication === Authentication.Keyless ? "Local endpoint" : "API key"}</span></span><span className="account-provider-chevron" aria-hidden="true">›</span>
        </button>)}</div>
        {options.length === 0 && !picker.fetching && !picker.error ? !picker.pageToken && !picker.nextPageToken ? <div><p>Enable an API provider to add an entry.</p><button type="button" onClick={openProviders}>Open API Providers</button></div> : <p>No enabled API providers on this page.</p> : null}
      </> : null}
      <p>Only enabled API providers appear here.</p>
      {picker.pageToken || picker.nextPageToken ? <nav className="actions" aria-label="Provider pages">
        {picker.pageToken ? <button type="button" disabled={picker.fetching} onClick={picker.first}>First page</button> : null}
        {picker.nextPageToken ? <button type="button" disabled={picker.fetching} onClick={picker.next}>Next page</button> : null}
      </nav> : null}
    </> : <>
      <h2 ref={heading} tabIndex={-1}>Connect your entry</h2>
      <div className="api-entry-provider"><div><strong>{selectedProvider?.displayName ?? "Unavailable"}</strong><span>{keyless ? "Local endpoint" : "API key"}</span></div><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain || unknownResponse} onClick={returnToProviders}>Change</button></div>
      {!accountTypeFilteringReady ? <p role="status">This server no longer reports the provider inventory and account-type filtering capabilities required here. Update the server before submitting or retrying.</p> : null}
      <form onSubmit={(event) => { event.preventDefault(); createAccount(); }}>
        <fieldset disabled={!accountTypeFilteringReady || providerChecking || create.busy || create.uncertain}>
          <label>Entry name<input autoComplete="off" maxLength={256} value={alias} aria-invalid={(attempted || alias.length > 0) && !aliasValid} onChange={(event) => setAlias(event.target.value)} /></label>
          {(attempted || alias.length > 0) && !aliasValid ? <p role="alert">Enter a non-empty entry name no longer than 256 UTF-8 bytes.</p> : null}
          {keyless ? <p>Connect to this local endpoint on the selected server.</p> : <>
            <label>API key<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} value={apiKey} aria-invalid={(attempted || apiKey.length > 0) && !apiKeyValid} onChange={(event) => setApiKey(event.target.value)} /></label>
            {!providerChecking && !create.busy && !create.uncertain && (attempted || apiKey.length > 0) && !apiKeyValid ? <p role="alert">Enter 1–8192 printable ASCII bytes without whitespace.</p> : null}
            <details><summary>Where to get an API key</summary><p>{selectedProvider?.keyGuidance || "Use the provider's documented API key flow."}</p>{selectedProvider?.documentationUrl ? <p>Provider documentation: <code>{selectedProvider.documentationUrl}</code></p> : null}</details>
          </>}
          <p>Use a separate entry for each API key.</p><p>Stored securely on the selected server.</p>
          <details open={advanced} onToggle={(event) => setAdvanced(event.currentTarget.open)}><summary>Advanced preferences</summary>
            <label className="checkbox"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />Enable this entry</label>
            <label className="checkbox"><input type="checkbox" checked={excludeAutomatic} onChange={(event) => setExcludeAutomatic(event.target.checked)} />Exclude from automatic entry selection</label>
            <label className="checkbox"><input type="checkbox" checked={recoveryNotifications} onChange={(event) => setRecoveryNotifications(event.target.checked)} />Notify when entry quota recovers</label>
          </details>
        </fieldset>
        <p className="api-entry-validation-note">New connections remain unverified until you explicitly validate the connection.</p>
        <Problem error={create.error} />
        {create.busy ? <p role="status">Creating entry…</p> : null}
        {create.uncertain ? <p role="status">Result not confirmed. The entry request remains retained for exact retry.</p> : null}
        {create.error && !create.uncertain ? <p role="status">Entry creation failed. Correct the details and submit again.</p> : null}
        {providerMismatch ? <p role="alert">{keyless ? "This provider changed or is no longer available. Review the current API Providers entry before submitting again." : "This provider changed or is no longer available. The API key was cleared. Review the current API Providers entry before submitting again."}</p> : null}
        {providerChecking ? <p role="status">Checking the current provider settings…</p> : null}
        <Problem error={providerRead.error} />
        <div className="actions"><button className="primary" disabled={!accountTypeFilteringReady || providerChecking || unknownResponse || !aliasValid || !apiKeyValid || !selectedProvider?.enabled || create.busy || create.uncertain}>{providerChecking ? "Checking provider…" : "Add and connect"}</button>{create.uncertain ? <button type="button" disabled={!accountTypeFilteringReady || providerChecking || create.busy} onClick={create.retry}>Retry the same entry creation</button> : null}</div>
      </form>
    </>}
  </section>;
}

export function AccountSettings(props: AccountSettingsProps) {
  return props.section === AccountSettingsSection.Subscription ? <SubscriptionAccounts active={props.active} editAccount={props.editAccount} deleteAccount={props.deleteAccount} onWorkflowReadyChange={props.onWorkflowReadyChange} /> : <ApiAccountSettings {...props} />;
}

function ApiAccountSettings({
  oauth,
  section,
  active,
  accountTypeFilteringReady,
  accountTypeFilteringProblem,
  accountTypeFilteringLoading = false,
  accountTypeFilteringFetching = false,
  retryAccountCapabilities,
  providerIdFilter = "",
  clearProviderFilter,
  providers,
  eligibleProviders,
  providerSearch,
  setProviderSearch,
  providerSearchLoading,
  providerSearchError,
  providerPicker,
  providerFilterHasMore = false,
  loadMoreProviderFilters,
  setProviderFilter,
  subscriptionProviderResources,
  subscriptionProviderManagement,
  openApiProviders,
  manageAccount,
  editAccount,
  deleteAccount,
  onWorkflowReadyChange,
  startApiWizard,
  providerHint,
}: AccountSettingsProps) {
  const [page, setPage] = useState<{ section: AccountSettingsSection; providerId: string; token: string }>({ section, providerId: "", token: "" });
  const [wizard, setWizard] = useState(false);
  const [wizardProvider, setWizardProvider] = useState<AccountProviderSummary>();
  const [pauseWorkflowLock, setPauseWorkflowLock] = useState(false);
  const [selectedAccount, setSelectedAccount] = useState<Resource>();
  const lastWizardRequest = useRef("");
  const providerSummaries = useMemo(() => {
    const result = [...providers];
    for (const provider of [providerHint, startApiWizard?.provider]) {
      if (provider && !result.some((item) => item.providerId === provider.providerId)) result.push(provider);
    }
    return result;
  }, [providerHint, providers, startApiWizard]);
  const pageToken = page.section === section && page.providerId === providerIdFilter ? page.token : "";
  const accountType = AccountTypeFilter.API;
  const rows = useQuery(ResourceQuery.listResources, {
    filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken },
    providerId: providerIdFilter,
    accountType,
  }, { enabled: active && accountTypeFilteringReady && !wizard && !selectedAccount });
  const providersById = useMemo(() => {
    const values = new Map<string, { displayName: string; enabled: boolean }>();
    for (const provider of providerSummaries) values.set(provider.providerId, { displayName: provider.displayName, enabled: provider.enabled });
    return values;
  }, [providerSummaries]);
  const workflowActive = (wizard && !pauseWorkflowLock) || Boolean(selectedAccount);
  useEffect(() => {
    onWorkflowReadyChange?.(workflowActive);
    return () => onWorkflowReadyChange?.(false);
  }, [onWorkflowReadyChange, workflowActive]);
  useEffect(() => {
    if (active && pauseWorkflowLock) setPauseWorkflowLock(false);
  }, [active, pauseWorkflowLock]);
  useEffect(() => {
    if (!startApiWizard || !startApiWizard.key || startApiWizard.key === lastWizardRequest.current) return;
    lastWizardRequest.current = startApiWizard.key;
    const provider = startApiWizard.provider ?? providers.find((item) => item.providerId === startApiWizard.providerId);
    if (!provider?.enabled || !provider.providerId || document(provider.provider).protocol === "native-subscription") return;
    setWizardProvider(provider);
    setPauseWorkflowLock(false);
    onWorkflowReadyChange?.(true);
    setWizard(true);
  }, [onWorkflowReadyChange, providers, startApiWizard]);
  const browseApiProviders = () => {
    if (!wizard || pauseWorkflowLock) return;
    onWorkflowReadyChange?.(false);
    setPauseWorkflowLock(true);
    openApiProviders();
  };

  if (selectedAccount && section === AccountSettingsSection.Api) return <><SettingsHeading title="AI API Keys" /><AccountConnection initial={selectedAccount} active={active} close={() => { setSelectedAccount(undefined); void rows.refetch(); }} /></>;

  if (wizard) return <AccountCreationWizard oauth={oauth} openEdit={editAccount} active={active} accountTypeFilteringReady={accountTypeFilteringReady && providerPicker.ready} initialProvider={wizardProvider} providers={providerSummaries} eligibleProviders={eligibleProviders} picker={providerPicker} close={() => { onWorkflowReadyChange?.(false); setWizard(false); setWizardProvider(undefined); setPauseWorkflowLock(false); }} openProviders={browseApiProviders} openManage={(resource) => { onWorkflowReadyChange?.(true); setWizard(false); setPauseWorkflowLock(false); manageAccount(resource); }} saved={() => { void rows.refetch(); }} />;

  const inventoryProblem = accountTypeFilteringProblem || providerSearchError;
  const readProblem = inventoryProblem || rows.error;
  const readDenied = readProblem && clientFailure(readProblem).code === FailureCode.PermissionDenied;
  const successfulEmpty = accountTypeFilteringReady && rows.data?.resources.length === 0 && !readProblem;
  const finalFirstPage = !pageToken && !rows.data?.nextPageToken;
  return <section className="account-settings api-keys-view" aria-label="AI API Keys settings">
    <SettingsHeading title="AI API Keys" description="Manage AI API keys and keyless local connections. Connection and health are separate states." actions={<>
      <button className="primary" type="button" disabled={!accountTypeFilteringReady} onClick={() => { setWizardProvider(undefined); onWorkflowReadyChange?.(true); setWizard(true); }}>Add AI API key</button>
    </>} />
    {providerIdFilter ? <div className="api-entry-filter"><p>Provider: {providersById.get(providerIdFilter)?.displayName || providerIdFilter}</p><button type="button" onClick={() => { setPage({ section, providerId: "", token: "" }); clearProviderFilter(); }}>Clear provider filter</button></div> : null}
    {accountTypeFilteringLoading ? <p role="status">Loading provider capabilities…</p> : null}
    <Problem error={inventoryProblem} />
    {inventoryProblem ? <button type="button" disabled={accountTypeFilteringFetching || providerSearchLoading} onClick={retryAccountCapabilities}>Retry provider inventory</button> : null}
    {readDenied ? <p role="status">Entry access is denied. Check this device’s permission on the selected server.</p> : null}
    {!accountTypeFilteringReady && !accountTypeFilteringLoading && !inventoryProblem ? <p role="status">Entry lists require a server that supports the required provider inventory and account-type filtering capabilities. Update the selected server before opening this list or adding an entry.</p> : null}
    {accountTypeFilteringReady ? <>
      <Problem error={rows.error} />
      {rows.error ? <button type="button" disabled={rows.isFetching} onClick={() => { void rows.refetch(); }}>Retry entries</button> : null}
      {readProblem && rows.data ? <p className="notice" role="status">Refresh failed. Showing the last successfully loaded entries.</p> : null}
      {rows.isFetching && !rows.data ? <SettingsLoading label="Loading entries…" /> : null}
      {rows.data?.resources.length ? <div className="api-entry-rows">{rows.data.resources.map((row) => {
        const data = document(row);
        const provider = providersById.get(text(data.provider_id));
        const quotaCount = Array.isArray(data.quota) ? data.quota.length : 0;
        return <article className="api-entry-row" key={row.id}>
          <h2>{resourceName(row)}</h2>
          {row.schemaVersion !== 1 ? <p className="api-entry-provider-name">{row.id}</p> : null}
          <p className="api-entry-provider-name">{provider?.displayName ?? "Provider unavailable · " + text(data.provider_id)}</p>
          <dl>
            <div><dt>Connection:</dt><dd>{text(object(data.removal).request_id) ? "Credential cleanup pending" : text(object(data.connection).id) ? "Credential connected" : "Disconnected"}</dd></div>
            <div><dt>Health:</dt><dd>{text(data.health) || "Unknown"}</dd></div>
            <div><dt>Entry:</dt><dd>{data.enabled === true ? "Enabled" : "Disabled"}</dd></div>
            <div><dt>Provider status:</dt><dd>{provider ? provider.enabled ? "Enabled" : "Off" : "Unavailable"}</dd></div>
            <div><dt>Quota:</dt><dd>{data.confirmed_exhausted === true ? "Confirmed exhausted" : quotaCount ? `${quotaCount} observations` : "No quota observation"}</dd></div>
          </dl>
          <div className="actions"><button type="button" disabled={row.schemaVersion !== 1} onClick={() => { onWorkflowReadyChange?.(true); setSelectedAccount(row); }}>Manage connection</button><button type="button" disabled={row.schemaVersion !== 1} onClick={() => editAccount(row)}>Edit preferences</button><button className="api-entry-delete" type="button" disabled={row.schemaVersion !== 1} onClick={() => deleteAccount(row)}>Delete entry</button></div>
        </article>;
      })}</div> : null}
      {successfulEmpty ? finalFirstPage && !providerIdFilter ? <SettingsEmpty title="No AI API key entries"><p>Add an entry for an enabled API provider.</p><p>Keyless local providers do not require a key.</p></SettingsEmpty> : <p className="api-entry-page-empty">{finalFirstPage && providerIdFilter ? "No entries for this provider." : "No entries on this page."}</p> : null}
      {pageToken || rows.data?.nextPageToken ? <nav className="settings-pages" aria-label="Entry pages">{pageToken ? <button type="button" disabled={rows.isFetching} onClick={() => setPage({ section, providerId: providerIdFilter, token: "" })}>First page</button> : null}{rows.data?.nextPageToken ? <button type="button" disabled={rows.isFetching} onClick={() => setPage({ section, providerId: providerIdFilter, token: rows.data!.nextPageToken })}>Next page</button> : null}</nav> : null}
    </> : null}
    <p className="api-entry-storage-note">Credentials are stored securely on the selected server.</p>
  </section>;
}
