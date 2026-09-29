import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ProviderInventoryCapability, ProviderPresetId, ProviderQuery, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { More, Problem } from "./ui";

const requiredCapabilities = [
  ProviderInventoryCapability.PROVIDER_ACTIVATION,
  ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
  ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
] as const;

const hostedPresetOrder = [
  ProviderPresetId.OPENAI,
  ProviderPresetId.ANTHROPIC,
  ProviderPresetId.OPENROUTER,
  ProviderPresetId.VERCEL_AI_GATEWAY,
  ProviderPresetId.XAI,
  ProviderPresetId.DEEPSEEK,
];

export function providerInventoryReady(capabilities: readonly ProviderInventoryCapability[] | undefined): boolean {
  return Boolean(capabilities && requiredCapabilities.every((value) => capabilities.includes(value)));
}

function presetString(id: ProviderPresetId): string | undefined {
  switch (id) {
    case ProviderPresetId.VERCEL_AI_GATEWAY: return "vercel-ai-gateway";
    case ProviderPresetId.OPENROUTER: return "openrouter";
    case ProviderPresetId.OPENAI: return "openai";
    case ProviderPresetId.ANTHROPIC: return "anthropic";
    case ProviderPresetId.XAI: return "xai";
    case ProviderPresetId.DEEPSEEK: return "deepseek";
    case ProviderPresetId.OLLAMA: return "ollama";
    case ProviderPresetId.LM_STUDIO: return "lm-studio";
    case ProviderPresetId.VLLM: return "vllm";
    default: return undefined;
  }
}

function presetData(entry: ProviderInventoryEntry, presets: Document[]): Document | undefined {
  if (entry.provider) return document(entry.provider);
  const id = presetString(entry.presetId);
  const preset = presets.find((value) => text(value.id) === id);
  return preset ? object(preset.provider) : undefined;
}

function ProviderToggle({ entry, presets, changed, refresh }: { entry: ProviderInventoryEntry; presets: Document[]; changed: () => void; refresh: () => unknown }) {
  const presetID = presetString(entry.presetId);
  const identity = entry.providerId || `preset:${presetID ?? entry.presetId}`;
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

export function ApiProviderSettings({
  active,
  changed,
  createCustom,
  editCustom,
  manageAccounts,
  addAccount,
  deleteCustom,
}: {
  active: boolean;
  changed: () => void;
  createCustom: (data?: Document) => void;
  editCustom: (resource: Resource) => void;
  manageAccounts: (providerID: string, entry: ProviderInventoryEntry) => void;
  addAccount: (providerID: string, entry: ProviderInventoryEntry) => void;
  deleteCustom: (resource: Resource) => void;
}) {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState("");
  const result = useQuery(ProviderQuery.listProviderInventory, { query, enabledOnly: false, pageSize: 50, pageToken: page }, { enabled: active });
  const activeInventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 1, pageToken: "" }, { enabled: active });
  const presetsQuery = useQuery(ProviderQuery.listProviderPresets, {}, { enabled: active });
  const ready = providerInventoryReady(result.data?.capabilities);
  const presets: Document[] = [];
  try {
    if (presetsQuery.data) presets.push(...items(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(presetsQuery.data.presetsJson))).map(object));
  } catch { /* Provider preset defaults remain unavailable when server data is malformed. */ }
  useEffect(() => { setPage(""); }, [query]);
  const entries = ready ? result.data?.entries ?? [] : [];
  const presetsMain = entries.filter((entry) => hostedPresetOrder.includes(entry.presetId)).sort((left, right) => hostedPresetOrder.indexOf(left.presetId) - hostedPresetOrder.indexOf(right.presetId));
  const local = entries.filter((entry) => [ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId));
  const custom = entries.filter((entry) => entry.presetId === ProviderPresetId.UNSPECIFIED);
  const noEnabledProviders = Boolean(activeInventory.data && providerInventoryReady(activeInventory.data.capabilities) && activeInventory.data.entries.length === 0 && !activeInventory.data.nextPageToken);
  const row = (entry: ProviderInventoryEntry) => {
    const customCopy = presetData(entry, presets);
    const accountState = entry.accountCountsAvailable ? `${entry.connectedAccounts.toString()} connected · ${entry.totalAccounts.toString()} total` : "Account counts unavailable";
    return <article className="result provider-row" key={entry.providerId || `preset:${entry.presetId}`}>
      <div className="provider-row-heading"><div><h4>{entry.displayName}</h4><p>{entry.presetId === ProviderPresetId.UNSPECIFIED ? "Custom API provider" : local.includes(entry) ? "Local API server" : "Preset"}</p></div><ProviderToggle entry={entry} presets={presets} changed={changed} refresh={result.refetch} /></div>
      <p>Accounts: {accountState}. Connection state is separate from provider validation and model compatibility.</p>
      {!entry.enabled && entry.totalAccounts > 0n ? <p>Turning this provider off preserves its accounts, credentials, models and history.</p> : null}
      <div className="actions">
        <button type="button" disabled={!entry.providerId || (entry.accountCountsAvailable && entry.totalAccounts === 0n && !entry.enabled)} onClick={() => {
          if (!entry.providerId) return;
          if (entry.accountCountsAvailable && entry.totalAccounts === 0n && entry.enabled) addAccount(entry.providerId, entry);
          else manageAccounts(entry.providerId, entry);
        }}>{!entry.accountCountsAvailable || entry.totalAccounts > 0n ? "Manage accounts" : "Add account"}</button>
        {entry.accountCountsAvailable && entry.totalAccounts === 0n && !entry.enabled && entry.providerId ? <span>Turn on this provider to add an account.</span> : null}
        {entry.presetId === ProviderPresetId.UNSPECIFIED && entry.provider ? <><button type="button" onClick={() => editCustom(entry.provider!)}>Edit custom provider</button><button type="button" onClick={() => deleteCustom(entry.provider!)}>Delete custom provider</button></> : null}
        {entry.presetId !== ProviderPresetId.UNSPECIFIED ? <button type="button" disabled={!customCopy} onClick={() => { if (!customCopy) return; const copy: Document = { ...customCopy, enabled: true }; delete copy.preset_id; createCustom(copy); }}>Create custom copy</button> : null}
      </div>
    </article>;
  };
  return <section aria-label="API provider inventory">
    <header><p>Provider availability is saved on the selected server.</p><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh providers</button></header>
    <div className="search-form"><label>Search API providers<input value={query} maxLength={256} onChange={(event) => setQuery(event.target.value)} /></label><button className="primary" type="button" disabled={!ready} onClick={() => createCustom()}>Custom provider</button></div>
    {result.isFetching && result.data ? <p role="status">Refreshing provider state. Displayed switches show the last confirmed server state.</p> : null}
    <Problem error={result.error || activeInventory.error || presetsQuery.error} />
    {!result.error && result.data && !ready ? <p role="alert">This server does not report the provider activation, active model filtering and account provider filtering capabilities required here. Update the server before changing provider or model settings.</p> : null}
    {result.isLoading ? <p role="status">Loading provider inventory…</p> : null}
    {ready && !query && noEnabledProviders ? <p className="notice">No API providers are enabled. Turn on a preset or create a custom provider to start new work.</p> : null}
    {ready && presetsMain.length ? <section><h3>Presets</h3>{presetsMain.map(row)}</section> : null}
    {ready && local.length ? <section><h3>Local API servers</h3>{local.map(row)}</section> : null}
    {ready && custom.length ? <section><h3>Custom providers</h3>{custom.map(row)}</section> : null}
    {ready && !entries.length ? <p className="empty">No providers match this search.</p> : null}
    <nav aria-label="Provider pages"><button type="button" disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><More available={Boolean(result.data?.nextPageToken)} busy={result.isFetching} load={() => setPage(result.data!.nextPageToken)} /></nav>
  </section>;
}

export function ActiveModelSettings({ active, createModel, editModel, priceModel }: {
  active: boolean;
  createModel: () => void;
  editModel: (resource: Resource) => void;
  priceModel: (resource: Resource) => void;
}) {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState("");
  const inventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 200, pageToken: "" }, { enabled: active });
  const ready = providerInventoryReady(inventory.data?.capabilities);
  const models = useQuery(ProviderQuery.searchModels, { query, providerId: "", includeHidden: true, pageSize: 50, pageToken: page, enabledProvidersOnly: true }, { enabled: active && ready });
  const enabledProviders = ready ? inventory.data?.entries ?? [] : [];
  const connectedAccounts = enabledProviders.reduce((sum, entry) => sum + entry.connectedAccounts, 0n);
  const accountCountsKnown = enabledProviders.every((entry) => entry.accountCountsAvailable);
  const providerInventoryComplete = !inventory.data?.nextPageToken;
  const providers = new Map((models.data?.providers ?? []).map((provider) => [provider.id, provider]));
  const grouped = new Map<string, Resource[]>();
  for (const model of models.data?.models ?? []) {
    const data = document(model);
    const providerID = text(data.provider_id);
    grouped.set(providerID, [...(grouped.get(providerID) ?? []), model]);
  }
  useEffect(() => { setPage(""); }, [query]);
  return <section aria-label="Models from active API providers">
    <header><p>New model choices come only from enabled API providers. Existing disabled references stay attached to their original identities.</p><button className="primary" type="button" disabled={!ready} onClick={createModel}>New Model</button></header>
    <label>Search active provider models<input value={query} maxLength={256} onChange={(event) => setQuery(event.target.value)} /></label>
    <Problem error={inventory.error || models.error} />
    {!inventory.error && inventory.data && !ready ? <p role="alert">This server does not report the required provider and active-model filtering capabilities. Update the server before using model settings.</p> : null}
    {models.isFetching && models.data ? <p role="status">Refreshing active provider models.</p> : null}
    {ready && enabledProviders.length === 0 ? <p className="notice">No API providers are enabled. Turn on a provider in API Providers.</p> : null}
    {ready && enabledProviders.length > 0 && providerInventoryComplete && accountCountsKnown && connectedAccounts === 0n && !models.data?.models.length ? <p className="notice">You can add models manually. Connect an API account only when you want to discover models automatically.</p> : null}
    {ready && [...grouped.entries()].map(([providerID, entries]) => <section key={providerID} aria-label={`Models from ${resourceName(providers.get(providerID))}`}>
      <h3>{resourceName(providers.get(providerID))}</h3>
      {entries.map((model) => {
        const data = document(model);
        return <article className="result" key={model.id}><h4>{text(data.name) || text(data.native_id)}</h4><p>Native ID: {text(data.native_id) || "Unavailable"}</p><p>CLI alias: {text(data.alias) || "None"} · {data.new === true ? "NEW" : "Reviewed"} · {data.hidden === true ? "Hidden" : "Visible"}</p><p>Configured harnesses: {items(data.harnesses).map(text).join(", ") || "None"}</p><div className="actions"><button type="button" disabled={model.schemaVersion !== 1} onClick={() => editModel(model)}>Edit model</button><button type="button" disabled={model.schemaVersion !== 1} onClick={() => priceModel(model)}>Token pricing</button></div></article>;
      })}
    </section>)}
    {ready && models.data?.models.length === 0 ? <p className="empty">{query ? "No models match this search." : "No models yet for the enabled providers."}</p> : null}
    <nav aria-label="Model pages"><button type="button" disabled={!page || models.isFetching} onClick={() => setPage("")}>First page</button><More available={Boolean(models.data?.nextPageToken)} busy={models.isFetching} load={() => setPage(models.data!.nextPageToken)} /></nav>
  </section>;
}
