import { providerPresetNames, hostedProviderPresetOrder } from "@delinoio/delidev-api-client";
import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ProviderInventoryCapability, ProviderPresetId, ProviderQuery, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { More, Problem } from "./ui";

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

function ProviderToggle({ entry, presets, changed, refresh }: { entry: ProviderInventoryEntry; presets: Document[]; changed: () => void; refresh: () => unknown }) {
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
      schemaVersion: 1,
      documentJson: encode(next),
    });
  };
  const disabled = mutation.busy || mutation.uncertain || !presetData(entry, presets);
  return <div className="provider-toggle">
    <span>{entry.enabled ? "On" : "Off"}</span>
    <button type="button" role="switch" aria-checked={entry.enabled} aria-label={`${entry.enabled ? "Turn off" : "Turn on"} ${entry.displayName}`} disabled={disabled} onClick={() => toggle(!entry.enabled)} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same change</button> : null}
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
  addAccount: (providerID: string, entry: ProviderInventoryEntry, oauthSupported: boolean) => void;
  deleteCustom: (resource: Resource) => void;
}) {
  const [localState, setLocalState] = useState<ProviderListState>({ query: "", page: "" });
  const { query, page } = state ?? localState;
  const change = changeState ?? setLocalState;
  const setQuery = (query: string) => change({ query, page: "" });
  const setPage = (page: string) => change({ query, page });
  const result = useQuery(ProviderQuery.listProviderInventory, { query, enabledOnly: false, pageSize: 50, pageToken: page }, { enabled: active });
  const activeInventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 1, pageToken: "" }, { enabled: active });
  const presetsQuery = useQuery(ProviderQuery.listProviderPresets, {}, { enabled: active });
  const ready = providerInventoryReady(result.data?.capabilities);
  const presets: Document[] = [];
  try {
    if (presetsQuery.data) presets.push(...items(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(presetsQuery.data.presetsJson))).map(object));
  } catch { /* Provider preset defaults remain unavailable when server data is malformed. */ }
  const entries = ready ? result.data?.entries ?? [] : [];
  const presetsMain = entries.filter((entry) => hostedPresetOrder.includes(entry.presetId)).sort((left, right) => hostedPresetOrder.indexOf(left.presetId) - hostedPresetOrder.indexOf(right.presetId));
  const local = entries.filter((entry) => [ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId));
  const custom = entries.filter((entry) => entry.presetId === ProviderPresetId.UNSPECIFIED);
  const noEnabledProviders = Boolean(activeInventory.data && providerInventoryReady(activeInventory.data.capabilities) && activeInventory.data.entries.length === 0 && !activeInventory.data.nextPageToken);
  const row = (entry: ProviderInventoryEntry) => {
    const customCopy = presetData(entry, presets);
    const accountState = entry.accountCountsAvailable ? `${entry.connectedAccounts.toString()} connected · ${entry.totalAccounts.toString()} total` : "Entry counts unavailable";
    return <article className="result provider-row" key={providerIdentity(entry)}>
      <div className="provider-row-heading"><div><h4>{entry.displayName}</h4><p>{entry.presetId === ProviderPresetId.UNSPECIFIED ? "Custom API provider" : local.includes(entry) ? "Local API server" : "Preset"}</p></div><ProviderToggle entry={entry} presets={presets} changed={changed} refresh={result.refetch} /></div>
      <p>Entries: {accountState}. Connection state is separate from provider validation and model compatibility.</p>
      {!entry.enabled && entry.totalAccounts > 0n ? <p>Turning this provider off preserves its entries, credentials, models and history.</p> : null}
      <div className="actions">
        <button type="button" disabled={!entry.providerId || (entry.accountCountsAvailable && entry.totalAccounts === 0n && !entry.enabled)} onClick={() => {
          if (!entry.providerId) return;
          if (entry.accountCountsAvailable && entry.totalAccounts === 0n && entry.enabled) addAccount(entry.providerId, entry, Boolean(result.data?.capabilities.includes(ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1) && result.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER)));
          else manageAccounts(entry.providerId, entry);
        }}>{!entry.accountCountsAvailable || entry.totalAccounts > 0n ? "Manage AI API Keys" : "Add AI API key"}</button>
        {entry.accountCountsAvailable && entry.totalAccounts === 0n && !entry.enabled && entry.providerId ? <span>Turn on this provider to add an entry.</span> : null}
        {entry.presetId === ProviderPresetId.UNSPECIFIED && entry.provider ? <><button type="button" onClick={() => editCustom(entry.provider!)}>Edit custom provider</button><button type="button" onClick={() => deleteCustom(entry.provider!)}>Delete custom provider</button></> : null}
        {entry.presetId !== ProviderPresetId.UNSPECIFIED ? <button type="button" disabled={!customCopy} onClick={() => { if (!customCopy) return; const copy: Document = { ...customCopy, enabled: true }; delete copy.preset_id; createCustom(copy); }}>Create custom copy</button> : null}
      </div>
    </article>;
  };
  return <section aria-label="API provider inventory">
    <SettingsHeading title="API Providers" description="Manage API providers and their availability." actions={<><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh providers</button><button className="primary" type="button" disabled={!ready} onClick={() => createCustom()}>Custom provider</button></>} />
    <div className="search-form"><label>Search API providers<input value={query} maxLength={256} onChange={(event) => setQuery(event.target.value)} /></label></div>
    {result.isFetching && result.data ? <p role="status">Refreshing provider state. Displayed switches show the last confirmed server state.</p> : null}
    <Problem error={result.error || activeInventory.error || presetsQuery.error} />
    {!result.error && result.data && !ready ? <p role="alert">This server does not report the provider activation, active model filtering and account provider filtering capabilities required here. Update the server before changing provider or model settings.</p> : null}
    {result.isLoading ? <SettingsLoading label="Loading provider inventory…" /> : null}
    {ready && !query && noEnabledProviders ? <p className="notice">No API providers are enabled. Turn on a preset or create a custom provider to start new work.</p> : null}
    {ready && presetsMain.length ? <section><h3>Presets</h3>{presetsMain.map(row)}</section> : null}
    {ready && local.length ? <section><h3>Local API servers</h3>{local.map(row)}</section> : null}
    {ready && custom.length ? <section><h3>Custom providers</h3>{custom.map(row)}</section> : ready && !result.error && !query && !page && !result.data?.nextPageToken ? <section><h3>Custom providers</h3><SettingsEmpty title="No custom providers yet"><p>Use Custom provider to configure another API endpoint.</p></SettingsEmpty></section> : null}
    {ready && !entries.length ? <p className="empty">No providers match this search.</p> : null}
    <nav aria-label="Provider pages"><button type="button" disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><More available={Boolean(result.data?.nextPageToken)} busy={result.isFetching} load={() => setPage(result.data!.nextPageToken)} /></nav>
  </section>;
}
