// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDismissButton } from "./settings-task";
import { useAccountStorage } from "./account-storage";
import { LocalizedText, copy, useLocale } from "./localization";
import { statusLabel } from "./product-status";
import { useCloseSettingsTask, useInSettingsTask, useSettingsTaskDismiss, useSettingsTaskVisible } from "./settings-task-context";
import { SettingsTaskDialog, SettingsDialogSize, SettingsTaskActions } from "./settings-task";
import { ProviderGuidance } from "./provider-guidance";
import { APIFormatChoice } from "./api-format-choice";
import { AccountOAuth, useAccountOAuth, type AccountOAuthFlow } from "./account-oauth";
import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { useEffect, useMemo, useRef, useState, type ReactNode, useId } from "react";
import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { useResourceScrollQuery } from "./resource-scroll-query";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useIsFetching, useQueryClient } from "@tanstack/react-query";
import { ApiVerification } from "./api-verification";
import { ApiEntryRow } from "./api-entry-row";
import type { UsageEntry } from "./usage-entry";
import {
  AccountQuery,
  APIFormatId, APIAuthenticationId, apiFormat, apiFormatLabels, providerAPIFormats, supportsResourceSchema,
  AccountTypeFilter,
  FailureCode,
  clientFailure,
  ConfigurationQuery,
  EntityKind,
  ResourceQuery,
  UsageQuery,
  newRequestId,
  type APIFormatProfile,
  type Resource,
} from "@delinoio/delidev-api-client";
import "./api-account.css";
import { AccountConnection } from "./account-connection";
import { Authentication } from "./configuration-fields";
import { document, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Failure, Problem } from "./ui";
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
  oauthFormatSelectingAvailable?: boolean;
  apiFormats?: APIFormatProfile[];
}

export interface AccountProviderPicker {
  failure?: import("@delinoio/delidev-api-client").ClientFailure;
  query?: import("./scroll-continuation").ScrollContinuationQuery;
  renderOptions?: (render: (provider: AccountProviderSummary) => ReactNode, root: import("react").RefObject<HTMLElement | null>, active: boolean) => ReactNode;
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
  openUsage?: (entry: UsageEntry) => void;
  oauth?: AccountOAuthFlow;
  section: AccountSettingsSection;
  active: boolean;
  accountTypeFilteringReady: boolean;
  apiFormatSelectingReady?: boolean;
  accountTypeFilteringProblem?: unknown;
  providerInventoryFailure?: import("@delinoio/delidev-api-client").ClientFailure;
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

function validAccount(resource: Resource | undefined, providerId: string, alias: string, protocol: string): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || !resource.id || resource.schemaVersion !== 3 || !supportsResourceSchema(resource) || resource.revision < 1n) return false;
  const value = document(resource);
  return value.provider_id === providerId && value.type === "api" && value.alias === alias && value.api_protocol === protocol;
}

function validAccountObservation(resource: Resource | undefined, id: string, providerId: string, minimumRevision: bigint): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || resource.id !== id || !supportsResourceSchema(resource) || resource.revision < minimumRevision) return false;
  const data = document(resource);
  return data.type === "api" && data.provider_id === providerId;
}

function providerFormats(provider: AccountProviderSummary): APIFormatProfile[] {
  return provider.apiFormats ?? providerAPIFormats(document(provider.provider));
}

function providerContract(provider: AccountProviderSummary, protocol?: APIFormatId | ""): { id: string; authentication: string; protocol: string; endpoint: string; enabled: boolean; formats: string; rawAuthentication: string; rawProtocol: string; rawEndpoint: string; rawFormats: string } {
  const data = document(provider.provider);
  const formats = providerFormats(provider);
  const selected = protocol ? formats.find(profile => profile.protocol === protocol) : undefined;
  return { id: provider.providerId, authentication: selected?.authentication ?? text(data.authentication), protocol: selected?.protocol ?? text(data.protocol), endpoint: selected?.endpoint ?? text(data.endpoint), enabled: provider.enabled, formats: JSON.stringify(formats), rawAuthentication: text(data.authentication), rawProtocol: text(data.protocol), rawEndpoint: text(data.endpoint), rawFormats: JSON.stringify(providerAPIFormats(data)) };
}

function providerContractMatches(expected: ReturnType<typeof providerContract>, resource: Resource | undefined): boolean {
  if (!resource || resource.kind !== EntityKind.PROVIDER || !supportsResourceSchema(resource) || resource.id !== expected.id) return false;
  const data = document(resource);
  const enabled = data.enabled !== false;
  const selected = (JSON.parse(expected.formats) as APIFormatProfile[]).find(profile => profile.protocol === expected.protocol);
  return Boolean(selected && selected.authentication === expected.authentication && selected.endpoint === expected.endpoint &&
    text(data.authentication) === expected.rawAuthentication && text(data.protocol) === expected.rawProtocol &&
    text(data.endpoint) === expected.rawEndpoint && JSON.stringify(providerAPIFormats(data)) === expected.rawFormats &&
    enabled === expected.enabled && expected.enabled);
}

function sameProviderContract(left: ReturnType<typeof providerContract> | undefined, right: ReturnType<typeof providerContract>): boolean {
  return Boolean(left && left.id === right.id && left.authentication === right.authentication && left.protocol === right.protocol &&
    left.endpoint === right.endpoint && left.enabled === right.enabled && left.formats === right.formats &&
    left.rawAuthentication === right.rawAuthentication && left.rawProtocol === right.rawProtocol && left.rawEndpoint === right.rawEndpoint && left.rawFormats === right.rawFormats);
}

function AccountCreationWizard({
  oauth: suppliedOAuth,
  openEdit,
  active,
  accountTypeFilteringReady,
  apiFormatSelectingReady = false,
  initialProvider,
  providers,
  eligibleProviders,
  picker,
  close,
  openProviders,
  openManage,
  saved,
}: {
  oauth?: AccountOAuthFlow;
  openEdit: (resource: Resource) => void;
  active: boolean;
  accountTypeFilteringReady: boolean;
  apiFormatSelectingReady?: boolean;
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
  const taskFormId = useId(), taskVisible = useSettingsTaskVisible(), inTask = useInSettingsTask(), closeTask = useCloseSettingsTask(close);
  const localOAuth = useAccountOAuth();
  const oauth = suppliedOAuth ?? localOAuth;
  const oauthStarted = useRef(false);
  useEffect(() => {
    if (oauthStarted.current || !initialProvider?.oauthAvailable || !oauth.supports(initialProvider)) return;
    let live = true;
    queueMicrotask(() => {
      if (!live || oauthStarted.current) return;
      oauthStarted.current = true;
      oauth.start(initialProvider);
    });
    return () => { live = false; };
  }, [initialProvider, oauth]);
  const [step, setStep] = useState(initialProvider ? WizardStep.Account : WizardStep.Provider);
  const [providerId, setProviderId] = useState(initialProvider?.providerId ?? "");
  // Keep the clicked contract authoritative when independent inventory pages retain different snapshots.
  const [selectedHint, setSelectedHint] = useState(initialProvider);
  const heading = useRef<HTMLHeadingElement>(null);
  const providerButtons = useRef(new Map<string, HTMLButtonElement>());
  const [focusTarget, setFocusTarget] = useState(initialProvider ? WizardFocus.Account : WizardFocus.Picker);
  const [alias, setAlias] = useState("");
  const [protocolChoice, setProtocol] = useState<APIFormatId | "">("");
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
    if (!taskVisible) return;
    if (!active) { setFocusTarget(WizardFocus.None); return; }
    if ((focusTarget === WizardFocus.Account && step === WizardStep.Account) ||
      (focusTarget === WizardFocus.Picker && step === WizardStep.Provider)) heading.current?.focus();
    if (focusTarget === WizardFocus.Provider && step === WizardStep.Provider) providerButtons.current.get(providerId)?.focus();
    setFocusTarget(WizardFocus.None);
  }, [active, focusTarget, providerId, step, taskVisible]);
  const selectedProviderDocument = document(selectedProvider?.provider);
  const formats = (selectedProvider ? providerFormats(selectedProvider) : providerAPIFormats(selectedProviderDocument)).sort((a, b) => Object.values(APIFormatId).indexOf(a.protocol) - Object.values(APIFormatId).indexOf(b.protocol));
  const protocol = protocolChoice || (formats.length === 1 ? formats[0].protocol : "");
  const selectedProfile = formats.find(profile => profile.protocol === protocol);
  const selectedAuthentication = selectedProfile?.authentication ?? "";
  const selectedProviderContract = selectedProvider ? providerContract(selectedProvider, protocol) : undefined;
  const manualReady = accountTypeFilteringReady && apiFormatSelectingReady;
  const providerContractRef = useRef(selectedProviderContract);
  providerContractRef.current = selectedProviderContract;
  const capabilityRef = useRef(manualReady);
  capabilityRef.current = manualReady;
  const pickerContent = useRef<HTMLElement>(null), pickerRoot = useScrollRoot(pickerContent);
  const providerRead = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: providerId }, { enabled: active && step === WizardStep.Account && Boolean(providerId) });
  const verifyProvider = async (expected: ReturnType<typeof providerContract>): Promise<boolean> => {
    try {
      const checked = await providerRead.refetch();
      return !checked.error && providerContractMatches(expected, checked.data?.resource) && sameProviderContract(providerContractRef.current, expected);
    } catch { return false; }
  };
  const metadataUnavailable = providerRead.isFetching || Boolean(providerRead.error || picker.error || picker.failure) || picker.fetching;
  const keyless = selectedAuthentication === APIAuthenticationId.Keyless;
  const apiKeyValid = keyless || /^[!-~]{1,8192}$/.test(apiKey);
  const aliasValid = alias.trim().length > 0 && !alias.includes(String.fromCharCode(0)) && new TextEncoder().encode(alias).byteLength <= 256;
  const currentId = createdAccount?.id ?? "pending";
  const clearHandoff = () => {
    handoffKey.current = undefined;
    handoffProvider.current = undefined;
  };

  const create = useRetainedMutation("api-account-wizard:create", ConfigurationQuery.saveConfiguration, (result, request) => {
    let expected: { providerId: string; alias: string; protocol: string } | undefined;
    try {
      const body = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(request.documentJson)) as Record<string, unknown>;
      expected = { providerId: text(body.provider_id), alias: text(body.alias), protocol: text(body.api_protocol) };
    } catch { /* Malformed request data cannot authorize a follow-up connection. */ }
    const resource = result.resource;
    if (request.kind !== EntityKind.ACCOUNT || !request.mutation || result.requestId !== request.mutation.requestId ||
      !expected || !validAccount(resource, expected.providerId, expected.alias, expected.protocol)) {
      clearHandoff();
      setUnknownResponse(true);
      return;
    }
    acceptedCreateAttempt.current = createAttempt.current;
    setCreatedAccount(resource);
    setStep(WizardStep.Created);
    saved(resource);
    if (autoConnectAllowed.current && manualReady && activeRef.current && createGeneration.current === generation.current) {
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
      document(result.account).api_protocol === document(createdAccount).api_protocol &&
      (result.replayed || (Boolean(returnedConnection) && result.account.revision > expectedRevision)));
    if (!validResult) {
      setUnknownResponse(true);
      return;
    }
    if (!activeRef.current || connectGeneration.current !== generation.current) return;
    setConnected(result.account);
  });

  useSettingsTaskDismiss(() => {
    setApiKey(""); setConnectionKey("");
    // The submitted Add-and-connect intent owns its one-shot credential handoff.
    // Clear only editable inputs while that original intent can still complete.
    if (!create.busy && !create.uncertain && autoConnect === undefined && !providerChecking) clearHandoff();
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
      if (!protocol) setProtocol(apiFormat(body.api_protocol) ?? "");
      setEnabled(body.enabled === true);
      setExcludeAutomatic(body.exclude_automatic === true);
      setRecoveryNotifications(body.recovery_notifications === true);
      setStep(WizardStep.Account);
    } catch { /* The exact retained request remains available for inspection and retry. */ }
  }, [alias, create.busy, create.input, create.uncertain, providerId, protocol]);

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
      (previous.authentication !== next.authentication || previous.protocol !== next.protocol || previous.endpoint !== next.endpoint || previous.formats !== next.formats || (previous.enabled && !next.enabled))) {
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
    if (!activeRef.current || !manualReady || autoConnect !== generation.current || !createdAccount || !selectedProvider) {
      clearHandoff();
      setAutoConnect(undefined);
      return;
    }
    const originalProvider = handoffProvider.current;
    if (!selectedProvider.enabled || !originalProvider || !selectedProviderContract || !sameProviderContract(originalProvider, selectedProviderContract)) {
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
  }, [manualReady, autoConnect, connect.send, createdAccount, keyless, selectedProvider, selectedProviderContract]);

  const navigateBack = () => {
    if (unknownResponse) { closeTask(); return; }
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
    setProtocol("");
    setSelectedHint(provider);
    setAutoConnect(undefined);
    setStep(WizardStep.Account);
    setFocusTarget(WizardFocus.Account);
    if (oauth.supports(provider)) oauth.start(provider);
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
    if (!manualReady || metadataUnavailable || !selectedProfile || !protocol || unknownResponse || providerChecking || !selectedProvider || !selectedProvider.enabled || !aliasValid || !apiKeyValid || create.busy || create.uncertain) return;
    const requestGeneration = generation.current;
    const requestAlias = alias;
    const requestProviderId = selectedProvider.providerId;
    const requestProviderContract = providerContract(selectedProvider, protocol);
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
      schemaVersion: 3,
      documentJson: new TextEncoder().encode(JSON.stringify({
        alias: requestAlias,
        provider_id: requestProviderId,
        type: "api",
        api_protocol: protocol,
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
    const requestProviderContract = providerContract(selectedProvider, protocol);
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
    const validationState = text(object(data.validation).state) || copy("account-settings.extra.3bc91159cd96");
    const validationLabel = text(data.health) === "unverified" ? copy("account-settings.extra.b2c4eef1f935") : copy("account-settings.sentence.60031f8620a9", { v0: statusLabel(validationState) });
    return <section className="account-wizard api-keys-view" aria-labelledby="api-account-created-title">
      <button className="api-entry-back" type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={navigateBack}>{copy("account-settings.backToAiApiKeys_2d6214")}</button>
      <SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} /><h2 id="api-account-created-title">{resourceName(current)}</h2>
      <p>{copy("account-settings.apiFormat")}: {apiFormatLabels[apiFormat(data.api_protocol) ?? protocol as APIFormatId]}</p>
      <p>{copy("account-settings.entrySavedCredentialConnectionIsSeparate_9ee9e5")}</p>
      {!accountTypeFilteringReady ? <p role="status">{copy("account-settings.connectionIsPausedUntilThisServer_0528c4")}</p> : null}
      {create.busy ? <p role="status">{copy("account-settings.creatingEntry_e95d50")}</p> : null}
      {create.uncertain ? <p role="status">{copy("account-settings.resultNotConfirmedRetryTheSame_5df0a1")}</p> : null}
      {hasCredentials ? <p role="status"><LocalizedText id="account-settings.connectedHealth_c7ee69" components={{ s0: <>{statusLabel(text(data.health) || "unknown")}</>, s1: <>{validationLabel}</> }} /></p> : <p><LocalizedText id="account-settings.connection_654eff" components={{ s0: <>{statusLabel(text(data.health) || "disconnected")}</> }} /></p>}
      {connect.busy ? <p role="status">{copy("account-settings.connectingEntry_34fa4f")}</p> : null}
      {connect.uncertain ? <p role="status">{copy("account-settings.resultNotConfirmedTheOriginalConnection_0f9cdc")}</p> : null}
      {connect.error && !connect.uncertain ? <p role="status">{keyless ? copy("account-settings.entryCreatedConnectionFailedRetryThe_174e75") : copy("account-settings.entryCreatedConnectionFailedReEnter_fc83a6")}</p> : null}
      {!hasCredentials && selectedProvider && selectedProvider.enabled ? keyless ? <p>{copy("account-settings.connectToThisLocalEndpointOn_70be8a")}</p> : <label>{copy("account-settings.apiKey_16f0ee")}<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} disabled={providerChecking} value={connectionKey} onChange={(event) => setConnectionKey(event.target.value)} /></label> : !hasCredentials && selectedProvider ? <p role="status">{copy("account-settings.theSelectedProviderIsOffEnable_eaed28")}</p> : !hasCredentials ? <p role="status">{copy("account-settings.theSelectedProviderIsUnavailableRefresh_3c01cf")}</p> : null}
      {!hasCredentials ? <SettingsTaskActions className=""><button className="primary" type="button" disabled={!accountTypeFilteringReady || providerChecking || unknownResponse || !selectedProvider?.enabled || (!keyless && !/^[!-~]{1,8192}$/.test(connectionKey)) || connect.busy || connect.uncertain} onClick={connectExisting}>{providerChecking ? copy("account-settings.checkingProvider_051bfc") : keyless ? copy("account-settings.connectLocalEndpoint_f37d68") : copy("account-settings.connectApiKey_0f97e9")}</button>{connect.uncertain ? <button type="button" disabled={!accountTypeFilteringReady || connect.busy} onClick={retryConnection}>{copy("account-settings.retryTheSameConnection_bc857b")}</button> : null}</SettingsTaskActions> : null}
      {providerMismatch ? <p role="alert">{keyless ? copy("account-settings.thisProviderChangedOrIsNo_15d334") : copy("account-settings.thisProviderChangedOrIsNo_0b47f9")}</p> : null}
      {providerChecking || metadataUnavailable ? <p role="status">{copy("account-settings.checkingTheCurrentProviderSettings_411acc")}</p> : null}
      <Problem error={providerRead.error} />
      {unknownResponse ? <p role="alert">{copy("account-settings.theServerAcknowledgedARequestWithout_eb87fb")}</p> : null}
      <Problem error={connect.error} />
      <SettingsTaskActions className=""><button type="button" disabled={unknownResponse || providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={() => openManage(current)}>{copy("account-settings.manageConnection_ad2892")}</button><SettingsTaskDismissButton type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={unknownResponse ? closeTask : close}>{copy("account-settings.done_11a676")}</SettingsTaskDismissButton></SettingsTaskActions>
    </section>;
  }

  if (oauth.view) {
    const original = oauth.view.provider;
    const metadataMatches = providerRead.isSuccess && providerRead.data?.resource?.revision === original.provider.revision &&
      providerContractMatches(providerContract(original), providerRead.data?.resource);
    return <AccountOAuth metadataReady={!metadataUnavailable && picker.ready && metadataMatches && accountTypeFilteringReady && (!original.oauthFormatSelectingAvailable || apiFormatSelectingReady)} metadataProblem={<>
      <Problem error={providerRead.error ?? picker.error} /><Failure failure={picker.failure} />
      {providerRead.isSuccess && !metadataMatches ? <p role="alert">{copy("account-settings.thisProviderChangedOrIsNo_c94fdd")}</p> : null}
      {!accountTypeFilteringReady ? <p role="status">{copy("account-settings.connectionIsPausedUntilThisServer_0528c4")}</p> : null}
    </>} flow={oauth} back={returnToProviders} manual={() => { setStep(WizardStep.Account); setFocusTarget(WizardFocus.Account); }} done={resource => { saved(resource); close(); }} />;
  }

  return <section ref={pickerContent} className="account-wizard api-keys-view" aria-labelledby="api-account-wizard-title">
    <button className="api-entry-back" type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain} onClick={navigateBack}>{copy("account-settings.backToAiApiKeys_2d6214")}</button>
    <SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} /><h2 hidden={inTask} id="api-account-wizard-title">{copy("account-settings.addAiApiKey_2c04a8")}</h2>
    {step === WizardStep.Provider ? <>
      <h2 ref={heading} tabIndex={-1}>{copy("account-settings.chooseAnApiProvider_929afa")}</h2>
      <p>{copy("account-settings.selectAProviderToConnectYour_585388")}</p>
      {picker.fetching ? <p role="status">{copy("account-settings.loadingProviders_d8de93")}</p> : null}
      <Problem error={picker.error} /><Failure failure={picker.failure} />
      {(picker.failure?.code ?? (picker.error ? clientFailure(picker.error).code : undefined)) === FailureCode.PermissionDenied ? <p role="status">{copy("account-settings.providerInventoryAccessIsDeniedCheck_6101ae")}</p> : null}
      {picker.error || picker.failure ? <button type="button" disabled={picker.fetching} onClick={picker.retry}>{copy("account-settings.retryProviders_9bd189")}</button> : null}
      {(picker.error || picker.failure) && picker.loaded ? <p className="notice" role="status">{copy("account-settings.refreshFailedShowingTheLastSuccessfully_058f65")}</p> : null}
      {picker.loaded && (!accountTypeFilteringReady || !picker.ready) ? <p role="status">{copy("account-settings.providerChoicesAreUnavailableBecauseThis_af2379")}</p> : null}
      {accountTypeFilteringReady && picker.ready ? <>
        <div className="account-provider-choices">{picker.renderOptions ? picker.renderOptions(provider => <button type="button" className="account-provider-action" key={provider.providerId} ref={(button) => { if (button) providerButtons.current.set(provider.providerId, button); else providerButtons.current.delete(provider.providerId); }} onClick={() => pickProvider(provider)}>
          <span><strong>{provider.displayName}</strong><span className="account-provider-method">{oauth.supports(provider) ? copy("account-settings.browserSignIn_5db278") : document(provider.provider).authentication === Authentication.Keyless ? copy("account-settings.localEndpoint_c04191") : copy("account-settings.apiKey_16f0ee")}</span></span><span className="account-provider-chevron" aria-hidden="true">›</span>
        </button>, pickerRoot, active && step === WizardStep.Provider) : options.map(provider => <button type="button" className="account-provider-action" key={provider.providerId} ref={(button) => { if (button) providerButtons.current.set(provider.providerId, button); else providerButtons.current.delete(provider.providerId); }} onClick={() => pickProvider(provider)}>
          <span><strong>{provider.displayName}</strong><span className="account-provider-method">{oauth.supports(provider) ? copy("account-settings.browserSignIn_5db278") : document(provider.provider).authentication === Authentication.Keyless ? copy("account-settings.localEndpoint_c04191") : copy("account-settings.apiKey_16f0ee")}</span></span><span className="account-provider-chevron" aria-hidden="true">›</span>
        </button>)}</div>
        {options.length === 0 && !picker.fetching && !picker.error && !picker.failure ? !picker.pageToken && !picker.nextPageToken ? <div><p>{copy("account-settings.enableAnApiProviderToAdd_e516fd")}</p><button type="button" onClick={openProviders}>{copy("account-settings.openApiProviders_1e4d77")}</button></div> : <p>{copy("account-settings.noEnabledApiProvidersOnThis_7ad0ab")}</p> : null}
      </> : null}
      <p>{copy("account-settings.onlyEnabledApiProvidersAppearHere_c9d5a2")}</p>
      <ScrollContinuation showInitial={false} showErrors={false} root={pickerRoot} active={active && step === WizardStep.Provider} label={copy("account-settings.providerPages_ca1fc1")} query={picker.query ?? { loaded: picker.loaded, nextPageToken: picker.nextPageToken, append: picker.next, retry: picker.retry, reload: picker.first }} />
    </> : <>
      <h2 ref={heading} tabIndex={-1}>{copy("account-settings.connectYourEntry_17c199")}</h2>
      <div className="api-entry-provider"><div><strong>{selectedProvider?.displayName ?? copy("account-settings.unavailable_ca1844")}</strong><span>{keyless ? copy("account-settings.localEndpoint_c04191") : copy("account-settings.apiKey_16f0ee")}</span></div><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain || unknownResponse} onClick={returnToProviders}>{copy("account-settings.change_c0bf75")}</button></div>
      {!accountTypeFilteringReady ? <p role="status">{copy("account-settings.thisServerNoLongerReportsThe_81fc29")}</p> : null}
      <form id={`${taskFormId}-1`} onSubmit={(event) => { event.preventDefault(); createAccount(); }}>
        <fieldset disabled={!manualReady || metadataUnavailable || providerChecking || create.busy || create.uncertain}>
          <label>{copy("account-settings.entryName_978463")}<input autoComplete="off" maxLength={256} value={alias} aria-invalid={(attempted || alias.length > 0) && !aliasValid} onChange={(event) => setAlias(event.target.value)} /></label>
          {(attempted || alias.length > 0) && !aliasValid ? <p role="alert">{copy("account-settings.enterANonEmptyEntryName_24d18d")}</p> : null}
          <APIFormatChoice profiles={formats} value={protocol} invalid={attempted && !selectedProfile} onChange={value => { generation.current += 1; clearHandoff(); setApiKey(""); setConnectionKey(""); setProtocol(value); setProviderMismatch(false); }} />
          <p>{protocol === APIFormatId.Responses ? copy("account-settings.responsesHelp") : copy("account-settings.formatHelp")}</p>
          {keyless ? <p>{copy("account-settings.connectToThisLocalEndpointOn_70be8a")}</p> : <>
            <label>{copy("account-settings.apiKey_16f0ee")}<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} value={apiKey} aria-invalid={(attempted || apiKey.length > 0) && !apiKeyValid} onChange={(event) => setApiKey(event.target.value)} /></label>
            {!providerChecking && !create.busy && !create.uncertain && (attempted || apiKey.length > 0) && !apiKeyValid ? <p role="alert">{copy("account-settings.enter18192PrintableAsciiBytes_5695b1")}</p> : null}
            <details><summary>{copy("account-settings.whereToGetAnApiKey_525ff8")}</summary><p>{selectedProvider?.keyGuidance || copy("account-settings.extra.7a611b78ccfc")}</p>{selectedProvider ? <ProviderGuidance preset={selectedProvider.presetId} documentation={selectedProvider.documentationUrl} keyCreation={selectedProvider.keyCreationUrl} /> : null}</details>
          </>}
          <p>{copy("account-settings.useASeparateEntryForEach_d8b0c8")}</p><p>{copy("account-settings.storedSecurelyOnTheSelectedServer_110ddf")}</p>
          <details open={advanced} onToggle={(event) => setAdvanced(event.currentTarget.open)}><summary>{copy("account-settings.advancedPreferences_6abb0c")}</summary>
            <label className="checkbox"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />{copy("account-settings.enableThisEntry_9d9bf5")}</label>
            <label className="checkbox"><input type="checkbox" checked={excludeAutomatic} onChange={(event) => setExcludeAutomatic(event.target.checked)} />{copy("account-settings.excludeFromAutomaticEntrySelection_464713")}</label>
            <label className="checkbox"><input type="checkbox" checked={recoveryNotifications} onChange={(event) => setRecoveryNotifications(event.target.checked)} />{copy("account-settings.notifyWhenEntryQuotaRecovers_b06486")}</label>
          </details>
        </fieldset>
        <p className="api-entry-validation-note">{copy("api-verification.automatic")}</p>
        <Problem error={create.error} />
        {!apiFormatSelectingReady ? <p role="status">{copy("account-settings.apiFormatUnsupported")}</p> : null}
        {create.busy ? <p role="status">{copy("account-settings.creatingEntry_e95d50")}</p> : null}
        {create.uncertain ? <p role="status">{copy("account-settings.resultNotConfirmedTheEntryRequest_579041")}</p> : null}
        {create.error && !create.uncertain ? <p role="status">{copy("account-settings.entryCreationFailedCorrectTheDetails_72993a")}</p> : null}
        {providerMismatch ? <p role="alert">{keyless ? copy("account-settings.thisProviderChangedOrIsNo_72e318") : copy("account-settings.thisProviderChangedOrIsNo_c94fdd")}</p> : null}
        {providerChecking || metadataUnavailable ? <p role="status">{copy("account-settings.checkingTheCurrentProviderSettings_411acc")}</p> : null}
        <Problem error={providerRead.error} />
        <SettingsTaskActions form={`${taskFormId}-1`} className=""><button type="button" disabled={providerChecking || create.busy || create.uncertain || connect.busy || connect.uncertain || unknownResponse} onClick={returnToProviders}>{copy("agent-worker-wizard.back")}</button><button className="primary" disabled={!manualReady || metadataUnavailable || !selectedProfile || providerChecking || unknownResponse || !aliasValid || !apiKeyValid || !selectedProvider?.enabled || create.busy || create.uncertain}>{providerChecking ? copy("account-settings.checkingProvider_051bfc") : copy("account-settings.addAndConnect_4ffa9b")}</button>{create.uncertain ? <button type="button" disabled={!accountTypeFilteringReady || providerChecking || create.busy} onClick={create.retry}>{copy("account-settings.retryTheSameEntryCreation_8f455b")}</button> : null}</SettingsTaskActions>
      </form>
    </>}
  </section>;
}

export function AccountSettings(props: AccountSettingsProps) {
  useLocale();
  return props.section === AccountSettingsSection.Subscription ? <SubscriptionAccounts active={props.active} editAccount={props.editAccount} deleteAccount={props.deleteAccount} onWorkflowReadyChange={props.onWorkflowReadyChange} /> : <ApiAccountSettings {...props} />;
}

function ApiAccountSettings({
  openUsage,
  oauth,
  section,
  active,
  accountTypeFilteringReady,
  apiFormatSelectingReady = false,
  accountTypeFilteringProblem,
  providerInventoryFailure,
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
  const client = useQueryClient();
  const transport = useTransport();
  const usageKey = createConnectQueryKey({ schema: UsageQuery.getUsageSummary, transport, cardinality: "finite" });
  const usageFetching = useIsFetching({ queryKey: usageKey });
  const content = useRef<HTMLElement>(null), root = useScrollRoot(content);
  const addAccountButton = useRef<HTMLButtonElement>(null);
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
  const accountType = AccountTypeFilter.API;
  const inventory = useResourceScrollQuery(EntityKind.ACCOUNT, active && accountTypeFilteringReady && !wizard && !selectedAccount, section, false, accountType, providerIdFilter, undefined, true);
  const resources = inventory.payloadPages.flatMap(page => page.payload);
  const rows = { data: inventory.loaded ? { resources, nextPageToken: inventory.nextPageToken } : undefined,
    error: inventory.error?.failure, isFetching: Boolean(inventory.loading),
    refetch: () => inventory.refreshExplicit() };
  const entryRetryShown = useRef(false);
  if (rows.error) entryRetryShown.current = true;
  else if (!rows.isFetching) entryRetryShown.current = false;
  const providersById = useMemo(() => {
    const values = new Map<string, { displayName: string; enabled: boolean; protocol?: string }>();
    for (const provider of providerSummaries) values.set(provider.providerId, { displayName: provider.displayName, enabled: provider.enabled, protocol: text(document(provider.provider).protocol) });
    return values;
  }, [providerSummaries]);
  const workflowActive = (wizard && !pauseWorkflowLock) || Boolean(selectedAccount);
  const storage = useAccountStorage(rows.data?.resources ?? [], active && accountTypeFilteringReady && Boolean(rows.data) && !rows.error && !workflowActive, Boolean(rows.error));
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

  const inventoryProblem = accountTypeFilteringProblem || providerSearchError;
  const hasInventoryProblem = Boolean(inventoryProblem || providerInventoryFailure);
  const readProblem = inventoryProblem || providerInventoryFailure || rows.error;
  const readDenied = (rows.error?.code ?? providerInventoryFailure?.code ?? (inventoryProblem ? clientFailure(inventoryProblem).code : undefined)) === FailureCode.PermissionDenied;
  const successfulEmpty = accountTypeFilteringReady && inventory.loaded && inventory.rows.length === 0 && !readProblem;
  const finalFirstPage = inventory.loaded && !inventory.nextPageToken;
  return <section ref={content} className="account-settings api-keys-view api-usage-list" aria-label={copy("account-settings.aiApiKeysSettings_111960")}>
    <SettingsHeading title={copy("account-settings.aiApiKeys_da1a0f")} description={copy("account-settings.manageAiApiKeysAndKeyless_372629")} actions={<>
      <button ref={addAccountButton} className="primary" type="button" disabled={!accountTypeFilteringReady} onClick={() => { setWizardProvider(undefined); onWorkflowReadyChange?.(true); setWizard(true); }}>{copy("account-settings.addAiApiKey_2c04a8")}</button>
    </>} />
    {providerIdFilter ? <div className="api-entry-filter"><p><LocalizedText id="account-settings.provider_bcf1a6" components={{ s0: <>{providersById.get(providerIdFilter)?.displayName || providerIdFilter}</> }} /></p><button type="button" onClick={() => {  clearProviderFilter(); }}>{copy("account-settings.clearProviderFilter_e0b8c0")}</button></div> : null}
    {accountTypeFilteringLoading ? <p role="status">{copy("account-settings.loadingProviderCapabilities_012324")}</p> : null}
    <Problem error={inventoryProblem} /><Failure failure={providerInventoryFailure} />
    {hasInventoryProblem ? <button type="button" disabled={accountTypeFilteringFetching || providerSearchLoading} onClick={retryAccountCapabilities}>{copy("account-settings.retryProviderInventory_afa130")}</button> : null}
    {readDenied ? <p role="status">{copy("account-settings.entryAccessIsDeniedCheckThis_755d6f")}</p> : null}
    {!accountTypeFilteringReady && !accountTypeFilteringLoading && !hasInventoryProblem ? <p role="status">{copy("account-settings.entryListsRequireAServerThat_d168c9")}</p> : null}
    {accountTypeFilteringReady ? <>
      {rows.data?.resources.length ? storage.header : null}
      <Failure failure={rows.error} />
      {entryRetryShown.current ? <button type="button" disabled={rows.isFetching} onClick={() => { void rows.refetch(); }}>{copy("account-settings.retryEntries_038902")}</button> : null}
      {readProblem && rows.data ? <p className="notice" role="status">{copy("account-settings.refreshFailedShowingTheLastSuccessfully_09833b")}</p> : null}
      {rows.isFetching && !rows.data ? <SettingsLoading label={copy("account-settings.loadingEntries_49f7f3")} /> : null}
      <div className="api-usage-list-heading"><div><h2>{copy("account-settings.yourApiKeys_e9bf62")}</h2><p>{copy("account-settings.delidevUsageLast30Days_6c267c")}</p></div><button type="button" disabled={!active || rows.isFetching || usageFetching > 0} onClick={() => { void rows.refetch(); void client.refetchQueries({ queryKey: usageKey, type: "active" }); }}><LocalizedText id="account-settings.refreshUsage_831ddd" components={{ s0: <span aria-hidden="true">↻</span> }} /></button></div>
      <ScrollPayloadWindow query={inventory} root={root} active={active && !wizard && !selectedAccount} identity={paginationIdentity} revision={paginationRevision}>{resources => <div className="api-entry-rows">{resources.map(row => <ApiEntryRow key={row.id} storage={storage.forAccount(row)} verification={<ApiVerification row={row} provider={providerSummaries.find(provider => provider.providerId === text(document(row).provider_id))?.provider} active={active && accountTypeFilteringReady && !readDenied && !workflowActive && !rows.error} changed={() => { void rows.refetch(); }} />} row={row} provider={providersById.get(text(document(row).provider_id))} active={active && accountTypeFilteringReady && !readDenied && !wizard && !selectedAccount} manage={() => { onWorkflowReadyChange?.(true); setSelectedAccount(row); }} edit={() => editAccount(row)} remove={() => deleteAccount(row)} openUsage={openUsage} />)}</div>}</ScrollPayloadWindow>
      {successfulEmpty ? finalFirstPage && !providerIdFilter ? <SettingsEmpty title={copy("account-settings.noAiApiKeyEntries_319a32")}><p>{copy("account-settings.addAnEntryForAnEnabled_309062")}</p><p>{copy("account-settings.keylessLocalProvidersDoNotRequire_8313c6")}</p></SettingsEmpty> : <p className="api-entry-page-empty">{finalFirstPage && providerIdFilter ? copy("account-settings.noEntriesForThisProvider_86ca40") : copy("account-settings.noEntriesOnThisPage_c02ff6")}</p> : null}
      <div hidden={wizard || Boolean(selectedAccount)}><ScrollContinuation showInitial={false} showErrors={false} query={inventory} root={root} active={active && !wizard && !selectedAccount} label={copy("account-settings.entryPages_b4e028")} /></div>
    </> : null}
    {accountTypeFilteringReady ? <p className="api-entry-storage-note">{copy("account-settings.knownUsageMayBeIncompleteEstimates_30eea0")}</p> : null}
    <p className="api-entry-storage-note">{copy("account-settings.credentialsAreStoredSecurelyOnThe_be612b")}</p>
    {selectedAccount && section === AccountSettingsSection.Api ? <SettingsTaskDialog key={selectedAccount.id} title={copy("account-settings.manageConnection_ad2892")} size={SettingsDialogSize.Wide} close={() => { setSelectedAccount(undefined); void rows.refetch(); }}><AccountConnection initial={selectedAccount} active={active} close={() => { setSelectedAccount(undefined); void rows.refetch(); }} /></SettingsTaskDialog> : null}
    {wizard ? <SettingsTaskDialog fallbackFocus={() => addAccountButton.current} title={copy("account-settings.addAiApiKey_2c04a8")} size={SettingsDialogSize.Form} close={() => { onWorkflowReadyChange?.(false); setWizard(false); setWizardProvider(undefined); setPauseWorkflowLock(false); }}><AccountCreationWizard apiFormatSelectingReady={apiFormatSelectingReady} oauth={oauth} openEdit={editAccount} active={active} accountTypeFilteringReady={accountTypeFilteringReady && providerPicker.ready} initialProvider={wizardProvider} providers={providerSummaries} eligibleProviders={eligibleProviders} picker={providerPicker} close={() => { onWorkflowReadyChange?.(false); setWizard(false); setWizardProvider(undefined); setPauseWorkflowLock(false); }} openProviders={browseApiProviders} openManage={(resource) => { onWorkflowReadyChange?.(true); setWizard(false); setPauseWorkflowLock(false); manageAccount(resource); }} saved={() => { void rows.refetch(); }} /></SettingsTaskDialog> : null}
  </section>;
}
