import { LocalizedText, copy, useLocale } from "./localization";
import { ProviderGuidance } from "./provider-guidance";
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
  presetId?: string;
  keyCreationUrl?: string;
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
  useLocale();
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
      <button className="api-entry-back" type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={navigateBack}>{copy("account-settings.backToAiApiKeys_2d6214")}</button>
      <SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} /><h2 id="api-account-created-title">{resourceName(current)}</h2>
      <p>{copy("account-settings.entrySavedCredentialConnectionIsSeparate_9ee9e5")}</p>
      {!accountTypeFilteringReady ? <p role="status">{copy("account-settings.connectionIsPausedUntilThisServer_0528c4")}</p> : null}
      {create.busy ? <p role="status">{copy("account-settings.creatingEntry_e95d50")}</p> : null}
      {create.uncertain ? <p role="status">{copy("account-settings.resultNotConfirmedRetryTheSame_5df0a1")}</p> : null}
      {hasCredentials ? <p role="status"><LocalizedText id="account-settings.connectedHealth_c7ee69" components={{ s0: <>{text(data.health) || "unknown"}</>, s1: <>{validationLabel}</> }} /></p> : <p><LocalizedText id="account-settings.connection_654eff" components={{ s0: <>{text(data.health) || "disconnected"}</> }} /></p>}
      {connect.busy ? <p role="status">{copy("account-settings.connectingEntry_34fa4f")}</p> : null}
      {connect.uncertain ? <p role="status">{copy("account-settings.resultNotConfirmedTheOriginalConnection_0f9cdc")}</p> : null}
      {connect.error && !connect.uncertain ? <p role="status">{keyless ? copy("account-settings.entryCreatedConnectionFailedRetryThe_174e75") : copy("account-settings.entryCreatedConnectionFailedReEnter_fc83a6")}</p> : null}
      {!hasCredentials && selectedProvider && selectedProvider.enabled ? keyless ? <p>{copy("account-settings.connectToThisLocalEndpointOn_70be8a")}</p> : <label>{copy("account-settings.apiKey_16f0ee")}<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} disabled={providerChecking} value={connectionKey} onChange={(event) => setConnectionKey(event.target.value)} /></label> : !hasCredentials && selectedProvider ? <p role="status">{copy("account-settings.theSelectedProviderIsOffEnable_eaed28")}</p> : !hasCredentials ? <p role="status">{copy("account-settings.theSelectedProviderIsUnavailableRefresh_3c01cf")}</p> : null}
      {!hasCredentials ? <div className="actions"><button className="primary" type="button" disabled={!accountTypeFilteringReady || providerChecking || unknownResponse || !selectedProvider?.enabled || (!keyless && !/^[!-~]{1,8192}$/.test(connectionKey)) || connect.busy || connect.uncertain} onClick={connectExisting}>{providerChecking ? copy("account-settings.checkingProvider_051bfc") : keyless ? copy("account-settings.connectLocalEndpoint_f37d68") : copy("account-settings.connectApiKey_0f97e9")}</button>{connect.uncertain ? <button type="button" disabled={!accountTypeFilteringReady || connect.busy} onClick={retryConnection}>{copy("account-settings.retryTheSameConnection_bc857b")}</button> : null}</div> : null}
      {providerMismatch ? <p role="alert">{keyless ? copy("account-settings.thisProviderChangedOrIsNo_15d334") : copy("account-settings.thisProviderChangedOrIsNo_0b47f9")}</p> : null}
      {providerChecking ? <p role="status">{copy("account-settings.checkingTheCurrentProviderSettings_411acc")}</p> : null}
      <Problem error={providerRead.error} />
      {unknownResponse ? <p role="alert">{copy("account-settings.theServerAcknowledgedARequestWithout_eb87fb")}</p> : null}
      <Problem error={connect.error} />
      <div className="actions"><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={() => openManage(current)}>{copy("account-settings.manageConnection_ad2892")}</button><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={close}>{copy("account-settings.done_11a676")}</button></div>
    </section>;
  }

  if (oauth.view) return <OpenRouterOAuth flow={oauth} back={returnToProviders} manual={() => { setStep(WizardStep.Account); setFocusTarget(WizardFocus.Account); }} edit={resource => { saved(resource); openEdit(resource); }} manage={resource => { saved(resource); openManage(resource); }} done={() => { if (oauth.view?.account) saved(oauth.view.account); close(); }} />;

  return <section className="account-wizard api-keys-view" aria-labelledby="api-account-wizard-title">
    <button className="api-entry-back" type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={navigateBack}>{copy("account-settings.backToAiApiKeys_2d6214")}</button>
    <SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} /><h2 id="api-account-wizard-title">{copy("account-settings.addAiApiKey_2c04a8")}</h2>
    {step === WizardStep.Provider ? <>
      <h2 ref={heading} tabIndex={-1}>{copy("account-settings.chooseAnApiProvider_929afa")}</h2>
      <p>{copy("account-settings.selectAProviderToConnectYour_585388")}</p>
      {picker.fetching ? <p role="status">{copy("account-settings.loadingProviders_d8de93")}</p> : null}
      <Problem error={picker.error} />
      {picker.error && clientFailure(picker.error).code === FailureCode.PermissionDenied ? <p role="status">{copy("account-settings.providerInventoryAccessIsDeniedCheck_6101ae")}</p> : null}
      {picker.error ? <button type="button" disabled={picker.fetching} onClick={picker.retry}>{copy("account-settings.retryProviders_9bd189")}</button> : null}
      {picker.error && picker.loaded ? <p className="notice" role="status">{copy("account-settings.refreshFailedShowingTheLastSuccessfully_058f65")}</p> : null}
      {picker.loaded && (!accountTypeFilteringReady || !picker.ready) ? <p role="status">{copy("account-settings.providerChoicesAreUnavailableBecauseThis_af2379")}</p> : null}
      {accountTypeFilteringReady && picker.ready ? <>
        <div className="account-provider-choices">{options.map((provider) => <button type="button" className="account-provider-action" key={provider.providerId} ref={(button) => { if (button) providerButtons.current.set(provider.providerId, button); else providerButtons.current.delete(provider.providerId); }} onClick={() => pickProvider(provider)}>
          <span><strong>{provider.displayName}</strong><span className="account-provider-method">{provider.oauthAvailable && oauth.available ? copy("account-settings.browserSignIn_5db278") : document(provider.provider).authentication === Authentication.Keyless ? copy("account-settings.localEndpoint_c04191") : copy("account-settings.apiKey_16f0ee")}</span></span><span className="account-provider-chevron" aria-hidden="true">›</span>
        </button>)}</div>
        {options.length === 0 && !picker.fetching && !picker.error ? !picker.pageToken && !picker.nextPageToken ? <div><p>{copy("account-settings.enableAnApiProviderToAdd_e516fd")}</p><button type="button" onClick={openProviders}>{copy("account-settings.openApiProviders_1e4d77")}</button></div> : <p>{copy("account-settings.noEnabledApiProvidersOnThis_7ad0ab")}</p> : null}
      </> : null}
      <p>{copy("account-settings.onlyEnabledApiProvidersAppearHere_c9d5a2")}</p>
      {picker.pageToken || picker.nextPageToken ? <nav className="actions" aria-label={copy("account-settings.providerPages_ca1fc1")}>
        {picker.pageToken ? <button type="button" disabled={picker.fetching} onClick={picker.first}>{copy("account-settings.firstPage_0bdbb7")}</button> : null}
        {picker.nextPageToken ? <button type="button" disabled={picker.fetching} onClick={picker.next}>{copy("account-settings.nextPage_c08ac7")}</button> : null}
      </nav> : null}
    </> : <>
      <h2 ref={heading} tabIndex={-1}>{copy("account-settings.connectYourEntry_17c199")}</h2>
      <div className="api-entry-provider"><div><strong>{selectedProvider?.displayName ?? copy("account-settings.unavailable_ca1844")}</strong><span>{keyless ? copy("account-settings.localEndpoint_c04191") : copy("account-settings.apiKey_16f0ee")}</span></div><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain || unknownResponse} onClick={returnToProviders}>{copy("account-settings.change_c0bf75")}</button></div>
      {!accountTypeFilteringReady ? <p role="status">{copy("account-settings.thisServerNoLongerReportsThe_81fc29")}</p> : null}
      <form onSubmit={(event) => { event.preventDefault(); createAccount(); }}>
        <fieldset disabled={!accountTypeFilteringReady || providerChecking || create.busy || create.uncertain}>
          <label>{copy("account-settings.entryName_978463")}<input autoComplete="off" maxLength={256} value={alias} aria-invalid={(attempted || alias.length > 0) && !aliasValid} onChange={(event) => setAlias(event.target.value)} /></label>
          {(attempted || alias.length > 0) && !aliasValid ? <p role="alert">{copy("account-settings.enterANonEmptyEntryName_24d18d")}</p> : null}
          {keyless ? <p>{copy("account-settings.connectToThisLocalEndpointOn_70be8a")}</p> : <>
            <label>{copy("account-settings.apiKey_16f0ee")}<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} value={apiKey} aria-invalid={(attempted || apiKey.length > 0) && !apiKeyValid} onChange={(event) => setApiKey(event.target.value)} /></label>
            {!providerChecking && !create.busy && !create.uncertain && (attempted || apiKey.length > 0) && !apiKeyValid ? <p role="alert">{copy("account-settings.enter18192PrintableAsciiBytes_5695b1")}</p> : null}
            <details><summary>{copy("account-settings.whereToGetAnApiKey_525ff8")}</summary><p>{selectedProvider?.keyGuidance || "Use the provider's documented API key flow."}</p>{selectedProvider ? <ProviderGuidance preset={selectedProvider.presetId} documentation={selectedProvider.documentationUrl} keyCreation={selectedProvider.keyCreationUrl} /> : null}</details>
          </>}
          <p>{copy("account-settings.useASeparateEntryForEach_d8b0c8")}</p><p>{copy("account-settings.storedSecurelyOnTheSelectedServer_110ddf")}</p>
          <details open={advanced} onToggle={(event) => setAdvanced(event.currentTarget.open)}><summary>{copy("account-settings.advancedPreferences_6abb0c")}</summary>
            <label className="checkbox"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />{copy("account-settings.enableThisEntry_9d9bf5")}</label>
            <label className="checkbox"><input type="checkbox" checked={excludeAutomatic} onChange={(event) => setExcludeAutomatic(event.target.checked)} />{copy("account-settings.excludeFromAutomaticEntrySelection_464713")}</label>
            <label className="checkbox"><input type="checkbox" checked={recoveryNotifications} onChange={(event) => setRecoveryNotifications(event.target.checked)} />{copy("account-settings.notifyWhenEntryQuotaRecovers_b06486")}</label>
          </details>
        </fieldset>
        <p className="api-entry-validation-note">{copy("account-settings.newConnectionsRemainUnverifiedUntilYou_f3828e")}</p>
        <Problem error={create.error} />
        {create.busy ? <p role="status">{copy("account-settings.creatingEntry_e95d50")}</p> : null}
        {create.uncertain ? <p role="status">{copy("account-settings.resultNotConfirmedTheEntryRequest_579041")}</p> : null}
        {create.error && !create.uncertain ? <p role="status">{copy("account-settings.entryCreationFailedCorrectTheDetails_72993a")}</p> : null}
        {providerMismatch ? <p role="alert">{keyless ? copy("account-settings.thisProviderChangedOrIsNo_72e318") : copy("account-settings.thisProviderChangedOrIsNo_c94fdd")}</p> : null}
        {providerChecking ? <p role="status">{copy("account-settings.checkingTheCurrentProviderSettings_411acc")}</p> : null}
        <Problem error={providerRead.error} />
        <div className="actions"><button className="primary" disabled={!accountTypeFilteringReady || providerChecking || unknownResponse || !aliasValid || !apiKeyValid || !selectedProvider?.enabled || create.busy || create.uncertain}>{providerChecking ? copy("account-settings.checkingProvider_051bfc") : copy("account-settings.addAndConnect_4ffa9b")}</button>{create.uncertain ? <button type="button" disabled={!accountTypeFilteringReady || providerChecking || create.busy} onClick={create.retry}>{copy("account-settings.retryTheSameEntryCreation_8f455b")}</button> : null}</div>
      </form>
    </>}
  </section>;
}

export function AccountSettings(props: AccountSettingsProps) {
  useLocale();
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
  useLocale();
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

  if (selectedAccount && section === AccountSettingsSection.Api) return <><SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} /><AccountConnection initial={selectedAccount} active={active} close={() => { setSelectedAccount(undefined); void rows.refetch(); }} /></>;

  if (wizard) return <AccountCreationWizard oauth={oauth} openEdit={editAccount} active={active} accountTypeFilteringReady={accountTypeFilteringReady && providerPicker.ready} initialProvider={wizardProvider} providers={providerSummaries} eligibleProviders={eligibleProviders} picker={providerPicker} close={() => { onWorkflowReadyChange?.(false); setWizard(false); setWizardProvider(undefined); setPauseWorkflowLock(false); }} openProviders={browseApiProviders} openManage={(resource) => { onWorkflowReadyChange?.(true); setWizard(false); setPauseWorkflowLock(false); manageAccount(resource); }} saved={() => { void rows.refetch(); }} />;

  const inventoryProblem = accountTypeFilteringProblem || providerSearchError;
  const readProblem = inventoryProblem || rows.error;
  const readDenied = readProblem && clientFailure(readProblem).code === FailureCode.PermissionDenied;
  const successfulEmpty = accountTypeFilteringReady && rows.data?.resources.length === 0 && !readProblem;
  const finalFirstPage = !pageToken && !rows.data?.nextPageToken;
  return <section className="account-settings api-keys-view" aria-label={copy("account-settings.aiApiKeysSettings_111960")}>
    <SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} description={copy("account-settings.manageAiApiKeysAndKeyless_372629")} actions={<>
      <button className="primary" type="button" disabled={!accountTypeFilteringReady} onClick={() => { setWizardProvider(undefined); onWorkflowReadyChange?.(true); setWizard(true); }}>{copy("account-settings.addAiApiKey_2c04a8")}</button>
    </>} />
    {providerIdFilter ? <div className="api-entry-filter"><p><LocalizedText id="account-settings.provider_bcf1a6" components={{ s0: <>{providersById.get(providerIdFilter)?.displayName || providerIdFilter}</> }} /></p><button type="button" onClick={() => { setPage({ section, providerId: "", token: "" }); clearProviderFilter(); }}>{copy("account-settings.clearProviderFilter_e0b8c0")}</button></div> : null}
    {accountTypeFilteringLoading ? <p role="status">{copy("account-settings.loadingProviderCapabilities_012324")}</p> : null}
    <Problem error={inventoryProblem} />
    {inventoryProblem ? <button type="button" disabled={accountTypeFilteringFetching || providerSearchLoading} onClick={retryAccountCapabilities}>{copy("account-settings.retryProviderInventory_afa130")}</button> : null}
    {readDenied ? <p role="status">{copy("account-settings.entryAccessIsDeniedCheckThis_755d6f")}</p> : null}
    {!accountTypeFilteringReady && !accountTypeFilteringLoading && !inventoryProblem ? <p role="status">{copy("account-settings.entryListsRequireAServerThat_d168c9")}</p> : null}
    {accountTypeFilteringReady ? <>
      <Problem error={rows.error} />
      {rows.error ? <button type="button" disabled={rows.isFetching} onClick={() => { void rows.refetch(); }}>{copy("account-settings.retryEntries_038902")}</button> : null}
      {readProblem && rows.data ? <p className="notice" role="status">{copy("account-settings.refreshFailedShowingTheLastSuccessfully_09833b")}</p> : null}
      {rows.isFetching && !rows.data ? <SettingsLoading label={copy("account-settings.loadingEntries_49f7f3")} /> : null}
      {rows.data?.resources.length ? <div className="api-entry-rows">{rows.data.resources.map((row) => {
        const data = document(row);
        const provider = providersById.get(text(data.provider_id));
        const quotaCount = Array.isArray(data.quota) ? data.quota.length : 0;
        return <article className="api-entry-row" key={row.id}>
          <h2>{resourceName(row)}</h2>
          {row.schemaVersion !== 1 ? <p className="api-entry-provider-name">{row.id}</p> : null}
          <p className="api-entry-provider-name">{provider?.displayName ?? "Provider unavailable · " + text(data.provider_id)}</p>
          <dl>
            <div><dt>{copy("account-settings.connection_5d80f5")}</dt><dd>{text(object(data.removal).request_id) ? copy("account-settings.credentialCleanupPending_50459d") : text(object(data.connection).id) ? copy("account-settings.credentialConnected_eed6f1") : copy("account-settings.disconnected_04dfac")}</dd></div>
            <div><dt>{copy("account-settings.health_ac2be4")}</dt><dd>{text(data.health) || "Unknown"}</dd></div>
            <div><dt>{copy("account-settings.entry_861e39")}</dt><dd>{data.enabled === true ? copy("account-settings.enabled_92c1cd") : copy("account-settings.disabled_75081b")}</dd></div>
            <div><dt>{copy("account-settings.providerStatus_369744")}</dt><dd>{provider ? provider.enabled ? copy("account-settings.enabled_92c1cd") : copy("account-settings.off_ca7981") : copy("account-settings.unavailable_ca1844")}</dd></div>
            <div><dt>{copy("account-settings.quota_e67c46")}</dt><dd>{data.confirmed_exhausted === true ? copy("account-settings.confirmedExhausted_763851") : quotaCount ? copy("account-settings.observations_402a6b", { v0: quotaCount }) : copy("account-settings.noQuotaObservation_d9e3af")}</dd></div>
          </dl>
          <div className="actions"><button type="button" disabled={row.schemaVersion !== 1} onClick={() => { onWorkflowReadyChange?.(true); setSelectedAccount(row); }}>{copy("account-settings.manageConnection_ad2892")}</button><button type="button" disabled={row.schemaVersion !== 1} onClick={() => editAccount(row)}>{copy("account-settings.editPreferences_00b4cc")}</button><button className="api-entry-delete" type="button" disabled={row.schemaVersion !== 1} onClick={() => deleteAccount(row)}>{copy("account-settings.deleteEntry_d2968b")}</button></div>
        </article>;
      })}</div> : null}
      {successfulEmpty ? finalFirstPage && !providerIdFilter ? <SettingsEmpty title={copy("account-settings.noAiApiKeyEntries_319a32")}><p>{copy("account-settings.addAnEntryForAnEnabled_309062")}</p><p>{copy("account-settings.keylessLocalProvidersDoNotRequire_8313c6")}</p></SettingsEmpty> : <p className="api-entry-page-empty">{finalFirstPage && providerIdFilter ? copy("account-settings.noEntriesForThisProvider_86ca40") : copy("account-settings.noEntriesOnThisPage_c02ff6")}</p> : null}
      {pageToken || rows.data?.nextPageToken ? <nav className="settings-pages" aria-label={copy("account-settings.entryPages_b4e028")}>{pageToken ? <button type="button" disabled={rows.isFetching} onClick={() => setPage({ section, providerId: providerIdFilter, token: "" })}>{copy("account-settings.firstPage_0bdbb7")}</button> : null}{rows.data?.nextPageToken ? <button type="button" disabled={rows.isFetching} onClick={() => setPage({ section, providerId: providerIdFilter, token: rows.data!.nextPageToken })}>{copy("account-settings.nextPage_c08ac7")}</button> : null}</nav> : null}
    </> : null}
    <p className="api-entry-storage-note">{copy("account-settings.credentialsAreStoredSecurelyOnThe_be612b")}</p>
  </section>;
}
