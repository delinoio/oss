import { useProviderPages, useModelPages, inventoryIdentity } from "./model-pagination";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { LocalizedText, copy, useLocale } from "./localization";
import { providerPresetNames, hostedProviderPresetOrder } from "@delinoio/delidev-api-client";
import { configurationSchemaVersion, subscriptionService, subscriptionServiceNames, supportsResourceSchema } from "@delinoio/delidev-api-client";
import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { ConfigurationQuery, EntityKind, ProviderInventoryCapability, ProviderPresetId, ProviderQuery, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Failure, Problem } from "./ui";
import { NativeModelSettings } from "./native-model-settings";
import { ProviderMark } from "./provider-mark";

const requiredCapabilities = [
  ProviderInventoryCapability.PROVIDER_ACTIVATION,
  ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
  ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
] as const;

const hostedPresetOrder = hostedProviderPresetOrder;

export function providerInventoryReady(capabilities: readonly ProviderInventoryCapability[] | undefined): boolean {
  return Boolean(capabilities && requiredCapabilities.every((value) => capabilities.includes(value)));
}

function presetString(id: ProviderPresetId): string | undefined { return providerPresetNames.get(id); }

function presetData(entry: ProviderInventoryEntry, presets: Document[]): Document | undefined {
  if (entry.provider) return document(entry.provider);
  const id = presetString(entry.presetId);
  const preset = presets.find((value) => text(value.id) === id);
  return preset ? object(preset.provider) : undefined;
}

function providerIdentity(entry: ProviderInventoryEntry): string {
  // First activation can publish a saved UUID before its original response arrives.
  // Keep the row and retained request bound to the same managed preset throughout.
  const presetID = presetString(entry.presetId);
  return presetID ? `preset:${presetID}` : entry.providerId || `preset:${entry.presetId}`;
}

function ProviderToggle({ entry, presets, changed, refresh, readOnly }: { entry: ProviderInventoryEntry; presets: Document[]; changed: () => void; refresh: () => unknown; readOnly: boolean }) {
  useLocale();
  const presetID = presetString(entry.presetId);
  const identity = providerIdentity(entry);
  const mutation = useRetainedMutation(`provider-activation:${identity}`, ConfigurationQuery.saveConfiguration, changed);
  const saved = entry.provider;
  useEffect(() => { if (mutation.error) void refresh(); }, [mutation.error, refresh]);
  const toggle = (enabled: boolean) => {
    const provider = presetData(entry, presets);
    if (!provider) return;
    const next: Document = { ...provider, enabled };
    if (presetID) next.preset_id = presetID;
    void mutation.send({
      mutation: { id: saved?.id ?? "", expectedRevision: saved?.revision ?? 0n, requestId: newRequestId() },
      kind: EntityKind.PROVIDER,
      schemaVersion: configurationSchemaVersion(EntityKind.PROVIDER, next),
      documentJson: encode(next),
    });
  };
  const disabled = readOnly || mutation.busy || mutation.uncertain || !presetData(entry, presets);
  return <div className="provider-toggle">
    <span>{entry.enabled ? copy("provider-model-settings.on_130011") : copy("provider-model-settings.off_ca7981")}</span>
    <button type="button" role="switch" aria-checked={entry.enabled} aria-label={copy("provider-model-settings.message_42d375", { v0: entry.enabled ? copy("provider-model-settings.turnOff_06f0e2") : copy("provider-model-settings.turnOn_5a1f09"), v1: entry.displayName })} disabled={disabled} onClick={() => toggle(!entry.enabled)} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("provider-model-settings.retryTheSameChange_671de5")}</button> : null}
    <Problem error={mutation.error} />
  </div>;
}

export interface ProviderListState { query: string; page: string }

export function ApiProviderSettings({
  active,
  state,
  changeState,
  changed,
  createCustom,
  editCustom,
  manageAccounts,
  addAccount,
  deleteCustom,
}: {
  active: boolean;
  state?: ProviderListState;
  changeState?: (value: ProviderListState) => void;
  changed: () => void;
  createCustom: (data?: Document) => void;
  editCustom: (resource: Resource) => void;
  manageAccounts: (providerID: string, entry: ProviderInventoryEntry) => void;
  addAccount: (providerID: string, entry: ProviderInventoryEntry, capabilities: readonly ProviderInventoryCapability[]) => void;
  deleteCustom: (resource: Resource) => void;
}) {
  useLocale();
  const [localState, setLocalState] = useState<ProviderListState>({ query: "", page: "" });
  const { query } = state ?? localState;
  const root = useRef<HTMLElement>(null);
  const change = changeState ?? setLocalState;
  const setQuery = (query: string) => change({ query, page: "" });
  const result = useProviderPages(query, active, false, true);
  const activeInventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 1, pageToken: "" }, { enabled: active });
  const presetsQuery = useQuery(ProviderQuery.listProviderPresets, {}, { enabled: active });
  const ready = providerInventoryReady(result.data?.capabilities);
  const presets: Document[] = [];
  try {
    if (presetsQuery.data) presets.push(...items(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(presetsQuery.data.presetsJson))).map(object));
  } catch { /* Provider preset defaults remain unavailable when server data is malformed. */ }
  const noEnabledProviders = Boolean(activeInventory.data && providerInventoryReady(activeInventory.data.capabilities) && activeInventory.data.entries.length === 0 && !activeInventory.data.nextPageToken);
  const row = (entry: ProviderInventoryEntry) => {
    const customCopy = presetData(entry, presets);
    const accountState = entry.accountCountsAvailable ? copy("provider-model-settings.sentence.490d50c6611c", { v0: entry.connectedAccounts.toString(), v1: entry.totalAccounts.toString() }) : copy("provider-model-settings.extra.555765b26ebc");
    return <article className="result provider-row" key={providerIdentity(entry)}>
      <div className="provider-row-heading"><div><h4 className="api-provider-name"><ProviderMark preset={entry.presetId} /><span>{entry.displayName}</span></h4><p>{entry.presetId === ProviderPresetId.UNSPECIFIED ? copy("provider-model-settings.customApiProvider_c1db3d") : [ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId) ? copy("provider-model-settings.localApiServer_dd8eec") : copy("provider-model-settings.preset_7252e7")}</p></div><ProviderToggle entry={entry} presets={presets} changed={changed} refresh={result.refetch} readOnly={Boolean(result.error)} /></div>
      <p><LocalizedText id="provider-model-settings.entriesConnectionStateIsSeparateFrom_e54388" components={{ s0: <>{accountState}</> }} /></p>
      {!entry.enabled && entry.totalAccounts > 0n ? <p>{copy("provider-model-settings.turningThisProviderOffPreservesIts_2cf65c")}</p> : null}
      <div className="actions">
        <button type="button" disabled={Boolean(result.error) || !entry.providerId || (entry.accountCountsAvailable && entry.totalAccounts === 0n && !entry.enabled)} onClick={() => {
          if (!entry.providerId) return;
          if (entry.accountCountsAvailable && entry.totalAccounts === 0n && entry.enabled) addAccount(entry.providerId, entry, result.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER) ? result.data.capabilities : []);
          else manageAccounts(entry.providerId, entry);
        }}>{!entry.accountCountsAvailable || entry.totalAccounts > 0n ? copy("provider-model-settings.manageAiApiKeys_a84e42") : copy("provider-model-settings.addAiApiKey_2c04a8")}</button>
        {entry.accountCountsAvailable && entry.totalAccounts === 0n && !entry.enabled && entry.providerId ? <span>{copy("provider-model-settings.turnOnThisProviderToAdd_6ce913")}</span> : null}
        {entry.presetId === ProviderPresetId.UNSPECIFIED && entry.provider ? <><button type="button" disabled={Boolean(result.error)} onClick={() => editCustom(entry.provider!)}>{copy("provider-model-settings.editCustomProvider_15ad80")}</button><button type="button" disabled={Boolean(result.error)} onClick={() => deleteCustom(entry.provider!)}>{copy("provider-model-settings.deleteCustomProvider_d29669")}</button></> : null}
        {entry.presetId !== ProviderPresetId.UNSPECIFIED ? <button type="button" disabled={Boolean(result.error) || !customCopy} onClick={() => { if (!customCopy) return; const copy: Document = { ...customCopy, enabled: true }; delete copy.preset_id; createCustom(copy); }}>{copy("provider-model-settings.createCustomCopy_a6be49")}</button> : null}
      </div>
    </article>;
  };
  return <section data-settings-search-target="provider-inventory" data-settings-search-pending={result.isLoading ? "true" : undefined} ref={root} aria-label={copy("provider-model-settings.apiProviderInventory_db530c")}>
    <SettingsHeading title={copy("provider-model-settings.apiProviders_376855")} description={copy("provider-model-settings.manageApiProvidersAndTheirAvailability_946ee7")} actions={<><button type="button" disabled={result.isFetching} onClick={result.refreshExplicit}>{copy("provider-model-settings.refreshProviders_56b2d1")}</button><button className="primary" type="button" disabled={!ready} onClick={() => createCustom()}>{copy("provider-model-settings.customProvider_fee405")}</button></>} />
    <div className="search-form"><label>{copy("provider-model-settings.searchApiProviders_1b03d9")}<input value={query} maxLength={256} onChange={(event) => setQuery(event.target.value)} /></label></div>
    {result.error ? <Failure failure={result.error.failure} /> : <Problem error={activeInventory.error || presetsQuery.error} />}
    {!result.error && result.data && !ready ? <p role="alert">{copy("provider-model-settings.thisServerDoesNotReportThe_03ee8f")}</p> : null}
    {result.isLoading ? <SettingsLoading label={copy("provider-model-settings.loadingProviderInventory_fa3bbe")} /> : null}
    {ready && !query && noEnabledProviders ? <p className="notice">{copy("provider-model-settings.noApiProvidersAreEnabledTurn_a639f9")}</p> : null}
    <ScrollPayloadWindow query={result} root={root} active={active && ready}>{(payload, rows) => {
      const resident = result.payloadPages.flatMap(page => page.payload.flatMap(response => response.entries));
      const accepted = new Set(rows.map(row => row.id));
      const entries = payload.flatMap(page => page.entries).filter(entry => accepted.has(inventoryIdentity(entry))).map(entry => resident.filter(row => inventoryIdentity(row) === inventoryIdentity(entry)).reduce((best, row) => (row.provider?.revision ?? 1n) >= (best.provider?.revision ?? 1n) ? row : best, entry));
      const presetsMain = entries.filter(entry => hostedPresetOrder.includes(entry.presetId)).sort((left, right) => hostedPresetOrder.indexOf(left.presetId) - hostedPresetOrder.indexOf(right.presetId));
      const local = entries.filter(entry => [ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId));
      const custom = entries.filter(entry => entry.presetId === ProviderPresetId.UNSPECIFIED);
      return <>{ready && presetsMain.length ? <section data-settings-search-target="provider-presets"><h3>{copy("provider-model-settings.presets_954f93")}</h3>{presetsMain.map(row)}</section> : null}
      {ready && local.length ? <section data-settings-search-target="provider-local"><h3>{copy("provider-model-settings.localApiServers_2052f0")}</h3>{local.map(row)}</section> : null}
      {ready && custom.length ? <section data-settings-search-target="provider-custom"><h3>{copy("provider-model-settings.customProviders_52b22a")}</h3>{custom.map(row)}</section> : null}</>;
    }}</ScrollPayloadWindow>
    {ready && !result.rows.length && !result.error ? <p className="empty">{copy("provider-model-settings.noProvidersMatchThisSearch_45fe24")}</p> : null}
    {ready && !query && result.loaded && !result.nextPageToken && !result.rows.some(row => !row.id.startsWith("preset:")) ? <section data-settings-search-target="provider-custom"><h3>{copy("provider-model-settings.customProviders_52b22a")}</h3><SettingsEmpty title={copy("provider-model-settings.noCustomProvidersYet_8fdcb5")}><p>{copy("provider-model-settings.useCustomProviderToConfigureAnother_f7a531")}</p></SettingsEmpty></section> : null}
    <ScrollContinuation query={result} root={root} active={active && ready} label={copy("provider-model-settings.providerPages_ca1fc1")} />
  </section>;
}

export interface ModelListState { query: string; page: string }

function ModelReadProblem({ error, busy, retry, label }: { error: unknown; busy: boolean; retry: () => unknown; label: string }) {
  useLocale();
  const transient = error && [Code.Unavailable, Code.DeadlineExceeded, Code.Unknown, Code.Internal].includes(ConnectError.from(error).code);
  return error ? <div className="models-read-problem" role="group" aria-label={label}>
    <Problem error={error} />
    {transient ? <button type="button" disabled={busy} onClick={() => void retry()}>{copy("provider-model-settings.retry_942087")}</button> : null}
  </div> : null;
}

export function ActiveModelSettings({ active, state, changeState, createModel, editModel, priceModel }: {
  active: boolean;
  state: ModelListState;
  changeState: (state: ModelListState) => void;
  createModel: (data?: Document) => void;
  editModel: (resource: Resource) => void;
  priceModel: (resource: Resource) => void;
}) {
  useLocale();
  const { query } = state;
  const root = useRef<HTMLElement>(null);
  const inventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 200, pageToken: "" }, { enabled: active });
  const ready = providerInventoryReady(inventory.data?.capabilities);
  const models = useModelPages(query, active && ready);
  const enabledProviders = ready ? inventory.data?.entries ?? [] : [];
  const connectedAccounts = enabledProviders.reduce((sum, entry) => sum + entry.connectedAccounts, 0n);
  const accountCountsKnown = enabledProviders.every((entry) => entry.accountCountsAvailable);
  const providerInventoryComplete = Boolean(inventory.data && !inventory.data.nextPageToken);
  const noEnabledProviders = enabledProviders.length === 0 && providerInventoryComplete;
  // Retained data describes this scope's last successful result; a refresh
  // failure does not replace it. Initial failures have no model data.
  const hasEmptyResults = ready && models.loaded && models.rows.length === 0;
  const emptyFirstPage = hasEmptyResults && !query && !noEnabledProviders;
  const knownZeroAccounts = enabledProviders.length > 0 && providerInventoryComplete && accountCountsKnown && connectedAccounts === 0n;
  return <section ref={root} className="models-list" aria-label={copy("provider-model-settings.modelsFromActiveApiProvidersAnd_6f3768")}>
    <SettingsHeading title={copy("provider-model-settings.models_d17d2d")} actions={<>
      <button className="primary" type="button" disabled={!ready} onClick={() => createModel()}><LocalizedText id="provider-model-settings.newModel_aabdb9" components={{ s0: <span aria-hidden="true">+</span> }} /></button>
    </>} />
    <label className="models-search"><LocalizedText id="provider-model-settings.searchModels_ed8381" components={{ s0: <span className="models-search-control"><svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4" /></svg><input value={query} maxLength={256} placeholder={copy("provider-model-settings.searchModels_37b906")} onChange={(event) => changeState({ query: event.target.value, page: "" })} /></span> }} /></label>
    {inventory.isLoading ? <SettingsLoading label={copy("provider-model-settings.loadingProviderInventory_fa3bbe")} /> : null}
    {inventory.isFetching && inventory.data ? <p role="status">{copy("provider-model-settings.refreshingProviderInventory_06451e")}</p> : null}
    <ModelReadProblem error={inventory.error} busy={inventory.isFetching || !active} retry={inventory.refetch} label={copy("provider-model-settings.providerInventoryReadFailure_93b792")} />
    {inventory.error && inventory.data ? <p role="status">{copy("provider-model-settings.providerRefreshFailedShowingTheLast_f2301a")}</p> : null}
    {!inventory.error && inventory.data && !ready ? <p role="alert">{copy("provider-model-settings.thisServerDoesNotReportThe_3240be")}</p> : null}
    {ready && models.isLoading ? <SettingsLoading label={copy("provider-model-settings.loadingModels_cc8b46")} /> : null}
    {ready && models.isFetching && models.data ? <p role="status">{copy("provider-model-settings.refreshingModels_833352")}</p> : null}
    <Failure failure={models.error?.failure} />
    {ready && models.data && (models.error || inventory.error) ? <p role="status">{copy("provider-model-settings.refreshFailedShowingTheLastSuccessfully_df6f1e")}</p> : null}
    {emptyFirstPage ? <SettingsEmpty title={copy("provider-model-settings.noModelsYet_c7a9aa")}><p>{copy("provider-model-settings.addModelsManuallyUsingNewModel_c0e8e6")}</p>
      {knownZeroAccounts ? <div className="models-account-guidance"><p>{copy("provider-model-settings.youCanAddModelsWithoutAn_ffd4ea")}</p><p>{copy("provider-model-settings.connectAnAccountOnlyForAutomatic_050225")}</p></div> : null}
    </SettingsEmpty> : hasEmptyResults ? <p className="models-empty-message">{noEnabledProviders ? copy("provider-model-settings.noApiProvidersAreEnabledTurn_662508") : copy("provider-model-settings.noModelsMatchThisSearch_217f95")}</p> : null}
    <ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={models} root={root} active={active && ready}>{payload => {
      const groups = new Map<string, typeof payload>();
      for (const row of payload) {
        const data = document(row.model), id = text(data.provider_id) || `subscription:${text(data.subscription_service)}`;
        groups.set(id, [...(groups.get(id) ?? []), row]);
      }
      return [...groups.entries()].map(([id, entries]) => {
        const name = id.startsWith("subscription:") ? subscriptionServiceNames[subscriptionService(id.slice(13))!] ?? copy("provider-model-settings.extra.fdfda19280ec") : entries[0].providerName;
        return <section className="models-provider-group" key={id} aria-label={copy("provider-model-settings.modelsFrom_4b8ec8", { v0: name })}><h3>{name}</h3><div className="models-rows">{entries.map(({ model }) => {
        const data = document(model);
        return <article className="models-row" key={model.id}>
          <div className="models-row-details">
            <div className="models-row-heading"><h4>{text(data.name) || text(data.native_id)}</h4><span className="models-status">{data.new === true ? copy("provider-model-settings.new_a253ff") : copy("provider-model-settings.reviewed_fad605")}</span><span className="models-status">{data.hidden === true ? copy("provider-model-settings.hidden_7e6fef") : copy("provider-model-settings.visible_8411f5")}</span></div>
            <div className="models-identifiers"><p><LocalizedText id="provider-model-settings.nativeId_3dd1ba" components={{ s0: <>{text(data.native_id) || copy("provider-model-settings.extra.ca1844969742")}</> }} /></p><p><LocalizedText id="provider-model-settings.cliAlias_275567" components={{ s0: <>{text(data.alias) || copy("provider-model-settings.extra.dc937b598926")}</> }} /></p></div>
            <p><LocalizedText id="provider-model-settings.configuredHarnesses_94210e" components={{ s0: <>{items(data.harnesses).map(text).join(", ") || copy("provider-model-settings.extra.dc937b598926")}</> }} /></p>
          </div>
          <div className="models-row-actions"><button type="button" disabled={Boolean(models.error) || !supportsResourceSchema(model) || document(model).retired === true} onClick={() => editModel(model)}>{copy("provider-model-settings.editModel_1733ca")}</button><button type="button" disabled={Boolean(models.error) || !supportsResourceSchema(model) || document(model).retired === true} onClick={() => priceModel(model)}>{copy("provider-model-settings.tokenPricing_56b24f")}</button></div>
        </article>;
        })}</div></section>;
      });
    }}</ScrollPayloadWindow>
    <ScrollContinuation query={models} root={root} active={active && ready} label={copy("provider-model-settings.modelPages_507678")} />
    <p className="models-footnote">{copy("provider-model-settings.modelChoicesRetainIndependentSubscriptionService_a2c413")}</p>
    <NativeModelSettings active={active} createModel={createModel} />
  </section>;
}
