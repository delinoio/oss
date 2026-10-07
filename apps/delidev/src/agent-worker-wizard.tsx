// SPDX-License-Identifier: Apache-2.0
import "./agent-worker-wizard.css";
import { useCallback, useDeferredValue, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountTypeFilter, KnownSubscriptionModelCatalogSource, SystemCapability, SystemQuery, ConfigurationQuery, EntityKind, ProviderQuery, ResourceQuery, SubscriptionServiceId, newRequestId, subscriptionServiceHarnesses, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationFields, Harness, Routing, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { SourceKind, SelectedAccount, fromKey, modelSource, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { useRetainedMutation } from "./mutation";
import { NativeModelSettings } from "./native-model-settings";
import { Problem } from "./ui";
import { revealAgentInvalidControl } from "./agent-configuration";
import { ToastKind, useNotifications } from "./toast-notifications";
import { WorkerHarnessPicker } from "./worker-harness-picker";
import { copy, displayLocale, ownedMessage, useLocale, useProductMessage, type MessageKey, type OwnedMessage } from "./localization";
import { statusLabel } from "./product-status";

// Groups remain mounted through step changes and reordering. Their stable keys
// own pagination/discovery receipts independently of their priority position.
enum Step { Harness = 1, Accounts, Model, Configure }
const stepName = (step: Step) => copy(step === Step.Harness ? "agent-worker-wizard.harnessStep" : step === Step.Accounts ? "agent-worker-wizard.accountsStep" : step === Step.Model ? "agent-worker-wizard.modelStep" : "agent-worker-wizard.configureStep");
enum SuggestionKind { Known = "known", Saved = "saved" }
interface ModelSuggestion { nativeId: string; name: string; kind: SuggestionKind; resource?: Resource }
const harnessNames = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };
interface Draft { original: Document; key: string; source?: Source; accounts: Document[]; routing?: string; modelID: string; model?: Resource; input: string }
interface Evidence { accountError: MessageKey | ""; modelError: MessageKey | ""; pending: boolean; label: string; accounts: string[] }
function draft(value: Document = {}): Draft {
  return { original: value, key: newRequestId(), accounts: items(value.accounts).map(object), routing: text(value.routing) || undefined, modelID: text(value.model_id), input: "" };
}
function showAccountChoice(row: Resource) {
  const data = document(row);
  return data.connection !== null && typeof data.connection === "object" && !Array.isArray(data.connection) && !data.removal && (data.health === "ready" || data.health === "unverified");
}
function accountStatus(data: Document) {
  const connection = copy(data.connection ? "agent-worker-wizard.connected" : "agent-worker-wizard.disconnected");
  const health = text(data.health);
  if (data.type !== "subscription") return copy("agent-worker-wizard.connectionHealth", { v0: connection, v1: statusLabel(health) });
  const quota = copy(data.confirmed_exhausted === true ? "agent-worker-wizard.quotaExhausted" : object(data.subscription).quota_state === "failed" ? "agent-worker-wizard.quotaFailed" : "agent-worker-wizard.quotaUnknown");
  return copy("agent-worker-wizard.subscriptionStatus", { v0: connection, v1: health && !["ready", "unverified", "disconnected"].includes(health) ? copy("agent-worker-wizard.healthSuffix", { v0: statusLabel(health) }) : "", v2: quota });
}

function SourceGroup({ value, index, count, step, harness, active, locked, refresh, duplicateSources, update, report, move, remove }: {
  value: Draft; index: number; count: number; step: Step; harness: Harness; active: boolean; locked: boolean; refresh: number; duplicateSources: string[];
  update: (key: string, patch: Partial<Draft>) => void; report: (key: string, value: Evidence) => void; move: (key: string, offset: number) => void; remove: (key: string) => void;
}) {
  useLocale();
  const [savedModels, setSavedModels] = useState<{ key: string; rows: Record<string, Resource> }>({ key: "", rows: {} });
  const [providerPage, setProviderPage] = useState("");
  const [accountPage, setAccountPage] = useState("");
  const [modelPage, setModelPage] = useState("");
  const [choosing, setChoosing] = useState(!value.accounts.length);
  const [known, setKnown] = useState<Record<string, Resource | undefined>>({});
  const [popup, setPopup] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const [nativePending, setNativePending] = useState(false);
  const listID = useId();
  const group = useRef<HTMLElement>(null);
  const source = value.source, ids = value.accounts.map(account => text(account.id));
  const sourceID = sourceKey(source);
  const query = useDeferredValue(value.input);
  const initialModel = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: value.modelID }, { enabled: active && Boolean(value.modelID), refetchInterval: active && step === Step.Model && Boolean(value.modelID) ? 5000 : false });
  const providers = useQuery(ProviderQuery.listProviderInventory, { enabledOnly: true, pageSize: 50, pageToken: providerPage }, { enabled: active && step === Step.Accounts });
  const provider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: source?.kind === SourceKind.Api ? source.id : "" }, { enabled: active && source?.kind === SourceKind.Api });
  const rows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: accountPage }, providerId: source?.kind === SourceKind.Api ? source.id : "", accountType: source?.kind === SourceKind.Api ? AccountTypeFilter.API : AccountTypeFilter.SUBSCRIPTION, subscriptionService: wireService(source) }, { enabled: active && Boolean(source) && step === Step.Accounts });
  const models = useQuery(ProviderQuery.searchModels, { query, providerId: source?.kind === SourceKind.Api ? source.id : "", subscriptionService: wireService(source), includeHidden: true, enabledProvidersOnly: true, pageSize: 50, pageToken: modelPage }, { enabled: active && Boolean(source) && step === Step.Model });
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const knownSupported = status.data?.capabilities.includes(SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1) === true;
  const knownModels = useQuery(ProviderQuery.listKnownSubscriptionModels, { subscriptionService: wireService(source) }, { enabled: active && knownSupported && source?.kind === SourceKind.Subscription && step === Step.Model });
  const date = knownModels.data?.updatedAt ?? "";
  const parsedDate = new Date(`${date}T00:00:00Z`);
  const knownValid = knownSupported && source?.kind === SourceKind.Subscription && knownModels.data?.subscriptionService === wireService(source) && knownModels.data.models.length <= 200 && /^sha256:[a-f0-9]{64}$/.test(knownModels.data.catalogVersion) && /^\d{4}-\d{2}-\d{2}$/.test(date) && Number.isFinite(parsedDate.getTime()) && parsedDate.toISOString().slice(0, 10) === date && [KnownSubscriptionModelCatalogSource.BUNDLED, KnownSubscriptionModelCatalogSource.CACHE, KnownSubscriptionModelCatalogSource.ONLINE].includes(knownModels.data.source) && knownModels.data.models.every(row => row.nativeId && row.displayName) && new Set(knownModels.data.models.map(row => row.nativeId)).size === knownModels.data.models.length;
  const remember = useCallback((id: string, row?: Resource) => setKnown(previous => previous[id] === row ? previous : { ...previous, [id]: row }), []);
  const pageValid = rows.data?.resources.every(row => sameSource(row, source));
  useEffect(() => { if (pageValid) for (const row of rows.data?.resources ?? []) remember(row.id, row); }, [pageValid, rows.data, remember]);
  useEffect(() => {
    const row = initialModel.data?.resource;
    if (!value.source && row && modelSource(row)) update(value.key, { source: modelSource(row), model: row, input: text(document(row).native_id) });
  }, [initialModel.data, value.source, value.key, update]);
  const label = source?.kind === SourceKind.Subscription ? `${subscriptionServiceNames[source.id as SubscriptionServiceId]} ${copy("agent-worker-wizard.subscription")}` : provider.data?.resource ? resourceName(provider.data.resource) : source?.id || copy("agent-worker-wizard.chooseSource");
  const selectedRows = ids.flatMap(id => known[id] ? [known[id]!] : []);
  const refreshAccount = selectedRows.find(row => document(row).connection && document(row).enabled !== false && !document(row).removal);
  const discovery = useRetainedMutation(`agent-worker-routes:discovery:${value.key}:${sourceID}`, ProviderQuery.discoverModels, result => {
    if (result.account) remember(result.account.id, result.account);
    setModelPage(""); void models.refetch(); void rows.refetch();
  }, (result, request) => result.requestId === request.mutation?.requestId && result.account?.id === request.mutation?.id);
  const pending = nativePending || discovery.busy || discovery.uncertain || step >= Step.Model && Boolean(value.modelID) && initialModel.isLoading;
  const accountError: MessageKey | "" = !source || source.kind === SourceKind.Subscription && subscriptionServiceHarnesses[source.id as SubscriptionServiceId] !== harness ? "agent-worker-wizard.chooseAccountSourceForHarness"
    : duplicateSources.includes(sourceID) ? "agent-worker-wizard.duplicateSource"
    : !ids.length || ids.some(id => !known[id] || !sameSource(known[id]!, source)) ? "agent-worker-wizard.selectCurrentAccount"
    : value.routing === Routing.Fixed && ids.length !== 1 || value.accounts.some(account => !Number.isInteger(account.weight) || Number(account.weight) < 1 || Number(account.weight) > 1000) ? "agent-worker-wizard.fixedRoutingWeights" : "";
  const modelError: MessageKey | "" = !value.input.trim() || new TextEncoder().encode(value.input.trim()).byteLength > 256 ? "agent-worker-wizard.selectModelFromSource"
    : value.model && (sourceKey(modelSource(value.model)) !== sourceID || !initialModel.data?.resource || initialModel.error || initialModel.data.resource.revision !== value.model.revision || sourceKey(modelSource(initialModel.data.resource)) !== sourceID) ? "agent-worker-wizard.selectedModelUnavailable" : "";
  const accountNames = selectedRows.map(resourceName).join("\u0000");
  useEffect(() => { report(value.key, { accountError, modelError, pending, label, accounts: accountNames.split("\u0000").filter(Boolean) }); }, [value.key, accountError, modelError, pending, label, accountNames, report]);
  useEffect(() => { if (step !== Step.Model) { setPopup(false); setHighlight(-1); } }, [step]);
  const chooseSource = (key: string) => { update(value.key, { source: fromKey(key), accounts: [], modelID: "", model: undefined, input: "" }); setAccountPage(""); setModelPage(""); setChoosing(true); };
  const catalogValid = models.data?.models.every(row => supportsResourceSchema(row) && sourceKey(modelSource(row)) === sourceID);
  const savedModelKey = `${sourceID}\u0000${query}`;
  const cached = savedModels.key === savedModelKey ? savedModels.rows : {};
  useEffect(() => {
    if (!catalogValid || !models.data || models.isFetching || models.error) return;
    setSavedModels(previous => {
      const current = previous.key === savedModelKey ? previous.rows : {};
      const next = { ...current };
      let changed = false;
      for (const row of models.data!.models) {
        const nativeId = text(document(row).native_id);
        const prior = current[nativeId];
        if (!nativeId || prior && prior.id === row.id && prior.revision === row.revision) continue;
        next[nativeId] = row; changed = true;
      }
      return changed ? { key: savedModelKey, rows: next } : previous;
    });
  }, [catalogValid, models.data, models.error, models.isFetching, savedModelKey]);
  const suggestions: ModelSuggestion[] = catalogValid ? models.data!.models.map(resource => ({ nativeId: text(document(resource).native_id), name: resourceName(resource), kind: SuggestionKind.Saved, resource })) : [];
  const savedIDs = new Set([...Object.keys(cached), ...suggestions.map(row => row.nativeId)]);
  const search = query.trim().toLocaleLowerCase();
  // A known candidate is advisory. Read every saved page first so a later-page
  // duplicate cannot bypass the saved model's exact revision guard.
  if (knownValid && catalogValid && models.data && !models.isFetching && !models.error && !models.data.nextPageToken) for (const row of knownModels.data!.models) {
    if (!savedIDs.has(row.nativeId) && (!search || `${row.displayName} ${row.nativeId}`.toLocaleLowerCase().includes(search))) suggestions.push({ nativeId: row.nativeId, name: row.displayName, kind: SuggestionKind.Known });
  }
  const catalogDate = knownValid ? new Intl.DateTimeFormat(displayLocale(), { year: "numeric", month: "short", day: "numeric", timeZone: "UTC" }).format(parsedDate) : "";
  const pick = (row?: ModelSuggestion) => { update(value.key, { model: row?.resource, modelID: row?.resource?.id ?? "", input: row?.nativeId ?? value.input }); setPopup(false); setHighlight(-1); setModelPage(""); setSavedModels({ key: "", rows: {} }); if (row?.resource) void initialModel.refetch(); };
  useEffect(() => {
    if (!active || !refresh) return;
    if (value.modelID) void initialModel.refetch();
    if (source) void rows.refetch();
  }, [active, refresh, value.modelID, initialModel.refetch, rows.refetch, sourceID]);
  const entries = providers.data?.entries.filter(entry => entry.enabled && entry.providerId) ?? [];
  const changeRouting = (routing: string) => update(value.key, { routing: routing || undefined });
  return <section ref={group} className="worker-source-group" data-source-group={value.key} hidden={step !== Step.Accounts && step !== Step.Model} aria-label={copy("agent-worker-wizard.sourceLabel", { v0: index + 1 })}>
    <fieldset disabled={locked}><header><h4 tabIndex={-1} data-wizard-field="source-heading">{index + 1} · {label}</h4>{step === Step.Accounts ? <div className="actions"><button type="button" disabled={index === 0} aria-label={copy("agent-worker-wizard.moveSourceUp", { v0: index + 1 })} onClick={() => move(value.key, -1)}>{copy("agent-worker-wizard.moveUp")}</button><button type="button" disabled={index === count - 1} aria-label={copy("agent-worker-wizard.moveSourceDown", { v0: index + 1 })} onClick={() => move(value.key, 1)}>{copy("agent-worker-wizard.moveDown")}</button><button type="button" disabled={count === 1} aria-label={copy("agent-worker-wizard.removeSource", { v0: index + 1 })} onClick={() => remove(value.key)}>{copy("agent-worker-wizard.remove")}</button></div> : null}</header>
    {ids.map(id => <SelectedAccount key={id} id={id} active={active} refresh={refresh} read={remember} />)}
    <div hidden={step !== Step.Accounts}>
      <p>{copy("agent-worker-wizard.accountsSelected", { v0: ids.length })}</p>
      {selectedRows.map(row => { const data = document(row); return <label className="worker-account-row" key={row.id}><input type="checkbox" aria-label={copy("agent-worker-wizard.selectAccount", { v0: resourceName(row) })} checked onChange={() => update(value.key, { accounts: value.accounts.filter(account => account.id !== row.id) })} /><span><strong>{resourceName(row)}</strong><small>{accountStatus(data)}</small><small>{copy("agent-worker-wizard.executionEligibility", { v0: data.enabled === false ? copy("agent-worker-wizard.disabled") : data.removal ? copy("agent-worker-wizard.removalPending") : copy("agent-worker-wizard.checkedAtExecution") })}</small></span></label>; })}
      <button type="button" aria-expanded={choosing} onClick={() => setChoosing(value => !value)}>{copy("agent-worker-wizard.chooseAccounts")}</button>
      <div role="region" aria-label={copy("agent-worker-wizard.chooseAccounts")} hidden={!choosing}>
        <label>{copy("agent-worker-wizard.accountSource")}<select data-wizard-field="source" aria-label={copy("agent-worker-wizard.sourceLabel", { v0: index + 1 })} value={sourceID} onChange={event => chooseSource(event.target.value)}><option value="">{copy("agent-worker-wizard.selectSource")}</option>{Object.values(SubscriptionServiceId).filter(service => subscriptionServiceHarnesses[service] === harness).map(service => <option key={service} value={`${SourceKind.Subscription}:${service}`} disabled={duplicateSources.includes(`${SourceKind.Subscription}:${service}`)}>{subscriptionServiceNames[service]} {copy("agent-worker-wizard.subscription")}</option>)}{source?.kind === SourceKind.Api && !entries.some(entry => entry.providerId === source.id) ? <option value={sourceID}>{label}{copy("agent-worker-wizard.retainedSourceSuffix")}</option> : null}{entries.map(entry => <option key={entry.providerId} value={`${SourceKind.Api}:${entry.providerId}`} disabled={duplicateSources.includes(`${SourceKind.Api}:${entry.providerId}`)}>{entry.displayName || resourceName(entry.provider)}</option>)}</select></label>
        <Problem error={providers.error || provider.error} />{providers.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingApiSources")}</p> : providers.data && entries.length === 0 ? <p>{copy("agent-worker-wizard.noApiSources")}</p> : null}
        <nav aria-label={copy("agent-worker-wizard.providerPages", { v0: index + 1 })}><button type="button" disabled={!providerPage || providers.isFetching} onClick={() => setProviderPage("")}>{copy("agent-worker-wizard.firstSourcePage")}</button><button type="button" disabled={!providers.data?.nextPageToken || providers.isFetching} onClick={() => setProviderPage(providers.data!.nextPageToken)}>{copy("agent-worker-wizard.nextSourcePage")}</button></nav>
        <Problem error={rows.error} />{source && rows.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingAccounts")}</p> : null}{rows.error && rows.data ? <p role="status">{copy("agent-worker-wizard.refreshFailedStaleAccounts")}</p> : null}{rows.data && !pageValid ? <p role="alert">{copy("agent-worker-wizard.invalidAccountPage")}</p> : null}
        {pageValid ? rows.data!.resources.filter(row => !ids.includes(row.id) && showAccountChoice(row)).map(row => <label className="worker-account-row" key={row.id}><input type="checkbox" checked={false} onChange={() => update(value.key, { accounts: [...value.accounts, { id: row.id, weight: 1 }] })} /><span><strong>{resourceName(row)}</strong><small>{accountStatus(document(row))}</small><small>{copy("agent-worker-wizard.executionEligibility", { v0: document(row).enabled === false ? copy("agent-worker-wizard.disabled") : copy("agent-worker-wizard.checkedAtExecution") })}</small></span></label>) : null}
        {source && pageValid && rows.data!.resources.length > 0 && !rows.error && !rows.isFetching && !rows.data!.resources.some(showAccountChoice) ? <div className="worker-account-empty"><p>{copy("agent-worker-wizard.noAccountsToSelectOnThisPage")}</p><p>{copy("agent-worker-wizard.connectAnAccountThenRefresh")}</p></div> : null}
        {rows.data?.resources.length === 0 && !rows.error ? <p>{copy("agent-worker-wizard.noAccountsForSource")}</p> : null}
        {source ? <nav aria-label={copy("agent-worker-wizard.sourceAccountPages", { v0: index + 1 })}><button type="button" disabled={!accountPage || rows.isFetching} onClick={() => setAccountPage("")}>{copy("agent-worker-wizard.firstAccountPage")}</button><button type="button" disabled={!rows.data?.nextPageToken || rows.isFetching} onClick={() => setAccountPage(rows.data!.nextPageToken)}>{copy("agent-worker-wizard.nextAccountPage")}</button><button type="button" disabled={rows.isFetching} onClick={() => { void rows.refetch(); }}>{copy("agent-worker-wizard.refreshAccounts")}</button></nav> : null}
      </div>
      <details className="worker-routing"><summary>{copy("agent-worker-wizard.routingOptions")} · {value.routing === Routing.Priority ? copy("agent-worker-wizard.inOrder") : value.routing || copy("agent-worker-wizard.serverDefault")}</summary><label>{copy("agent-worker-wizard.accountRouting")}<select data-wizard-field="routing" value={value.routing || ""} onChange={event => changeRouting(event.target.value)}><option value="">{copy("agent-worker-wizard.serverDefault")}</option>{Object.values(Routing).map(policy => <option key={policy} value={policy} disabled={policy === Routing.Fixed && ids.length !== 1}>{policy === Routing.Priority ? copy("agent-worker-wizard.inOrder") : policy}</option>)}</select></label><ol>{value.accounts.map((account, position) => <li key={text(account.id)}><strong>{known[text(account.id)] ? resourceName(known[text(account.id)]) : text(account.id)}</strong><label>{copy("agent-worker-wizard.weightForAccount", { v0: position + 1 })}<input type="number" min={1} max={1000} value={Number(account.weight)} onChange={event => update(value.key, { accounts: value.accounts.map((item, i) => i === position ? { ...item, weight: Number(event.target.value) } : item) })} /></label><button type="button" disabled={position === 0} aria-label={copy("agent-worker-wizard.moveAccountUp", { v0: position + 1 })} onClick={() => { const accounts = [...value.accounts]; [accounts[position - 1], accounts[position]] = [accounts[position], accounts[position - 1]]; update(value.key, { accounts }); }}>{copy("agent-worker-wizard.up")}</button></li>)}</ol></details>
    </div>
    <div hidden={step !== Step.Model}>
      <p>{copy("agent-worker-wizard.searchSavedCatalog")}</p>
      <div className="worker-model-combobox"><label htmlFor={`${listID}-input`}>{copy("agent-worker-wizard.modelForSource", { v0: label })}</label><input id={`${listID}-input`} data-wizard-field="model" role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-controls={listID} aria-activedescendant={popup && highlight >= 0 && highlight < suggestions.length + (value.input.trim() ? 1 : 0) ? `${listID}-${highlight}` : undefined} value={value.input} autoComplete="off" onFocus={() => setPopup(true)} onBlur={() => setPopup(false)} onChange={event => { update(value.key, { input: event.target.value, model: undefined, modelID: "" }); setModelPage(""); setSavedModels({ key: "", rows: {} }); setPopup(true); setHighlight(-1); }} onKeyDown={event => {
        if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); const count = suggestions.length + (value.input.trim() ? 1 : 0); setPopup(true); setHighlight(position => count === 0 ? -1 : event.key === "ArrowDown" ? Math.min(position + 1, count - 1) : position <= 0 ? count - 1 : position - 1); }
        else if (event.key === "Escape" && popup) { event.preventDefault(); event.stopPropagation(); setPopup(false); }
        else if (event.key === "Enter" && popup) { event.preventDefault(); pick(suggestions[highlight]); }
      }} />{popup ? <ul id={listID} role="listbox" aria-label={copy("agent-worker-wizard.modelsForSource", { v0: label })}>{suggestions.map((row, position) => <li key={`${row.kind}:${row.resource?.id ?? row.nativeId}`} id={`${listID}-${position}`} role="option" aria-selected={highlight === position} onMouseDown={event => event.preventDefault()} onClick={() => pick(row)}><div className="worker-model-heading"><strong>{row.name}</strong><span className="worker-model-origin">{copy(row.kind === SuggestionKind.Known ? "agent-worker-wizard.knownModelOrigin" : "agent-worker-wizard.savedModelOrigin")}</span></div><small>{row.nativeId}{row.resource && document(row.resource).hidden === true ? copy("agent-worker-wizard.hiddenSuffix") : ""}</small></li>)}{value.input.trim() ? <li id={`${listID}-${suggestions.length}`} role="option" aria-selected={highlight === suggestions.length} onMouseDown={event => event.preventDefault()} onClick={() => pick()}>{copy("agent-worker-wizard.useExactModelId", { v0: value.input.trim() })}</li> : null}</ul> : null}</div>
      {value.modelID && initialModel.isLoading ? <p role="status">{copy("agent-worker-wizard.checkingModelRevision")}</p> : null}<Problem error={models.error || knownModels.error || initialModel.error} />{models.isLoading || knownModels.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingModelCatalog")}</p> : models.data && !catalogValid ? <p role="alert">{copy("agent-worker-wizard.invalidModelPage")}</p> : suggestions.length === 0 && !models.isLoading && !knownModels.isLoading ? <p>{copy("agent-worker-wizard.noSavedModels")}</p> : null}{models.error || knownModels.error ? <p>{copy("agent-worker-wizard.catalogLookupFailed", { v0: models.data || knownModels.data ? copy("agent-worker-wizard.staleResults") : "" })}</p> : null}
      <nav aria-label={copy("agent-worker-wizard.sourceModelPages", { v0: index + 1 })}><button type="button" disabled={!modelPage || models.isFetching} onClick={() => setModelPage("")}>{copy("agent-worker-wizard.firstModelPage")}</button><button type="button" disabled={!models.data?.nextPageToken || models.isFetching} onClick={() => setModelPage(models.data!.nextPageToken)}>{copy("agent-worker-wizard.nextModelPage")}</button><button type="button" disabled={models.isFetching || knownModels.isFetching} onClick={() => { setSavedModels({ key: "", rows: {} }); setModelPage(""); void models.refetch(); if (source?.kind === SourceKind.Subscription && knownSupported) void knownModels.refetch(); if (value.modelID) void initialModel.refetch(); }}>{copy("agent-worker-wizard.reloadSavedCatalog")}</button></nav>
      {source?.kind === SourceKind.Subscription ? <>{knownValid ? <p>{copy("agent-worker-wizard.knownModelsCatalogUpdated", { v0: catalogDate, v1: knownModels.data!.source === KnownSubscriptionModelCatalogSource.CACHE ? copy("agent-worker-wizard.cachedCatalog") : knownModels.data!.source === KnownSubscriptionModelCatalogSource.BUNDLED ? copy("agent-worker-wizard.builtInCatalog") : "" })}</p> : knownModels.data ? <p role="alert">{copy("agent-worker-wizard.knownCatalogUnavailable")}</p> : null}{status.data && !knownSupported ? <p>{copy("agent-worker-wizard.updateServerForKnownModels")}</p> : null}<p>{copy("agent-worker-wizard.subscriptionModelAvailability")}</p></> : <><button type="button" disabled={locked || pending || !refreshAccount || document(provider.data?.resource).discovery !== true} onClick={() => { if (refreshAccount) void discovery.send({ mutation: { requestId: newRequestId(), id: refreshAccount.id, expectedRevision: refreshAccount.revision } }); }}>{copy("agent-worker-wizard.refreshModelsEndpoint")}</button><p>{copy("agent-worker-wizard.discoveryUsesFirstAccount")}</p></>}
      <Problem error={discovery.error} />
      {harness === Harness.Codex && source?.kind === SourceKind.Api ? <><NativeModelSettings active={active && step === Step.Model} selectedAccounts={selectedRows} pendingOperation={setNativePending} createModel={model => update(value.key, { input: text(model.native_id), modelID: "", model: undefined })} />{document(provider.data?.resource).protocol !== "openai-responses" ? <p>{copy("agent-worker-wizard.codexResponses")}</p> : null}</> : null}
      <p>{copy("agent-worker-wizard.modelReadiness")}</p>
    </div></fieldset>
    {discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>{copy("agent-worker-wizard.retryModelRefresh")}</button> : null}
  </section>;
}

export function AgentWorkerWizard({ initial, active, saved, cancel }: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : newConfiguration(EntityKind.AGENT));
  const [routes, setRoutes] = useState<Draft[]>(() => initial && items(document(initial).routes).length ? items(document(initial).routes).map(item => draft(object(item))) : [draft(initial ? { model_id: document(initial).model_id, accounts: document(initial).accounts, routing: document(initial).routing } : { routing: Routing.Priority })]);
  const [evidence, setEvidence] = useState<Record<string, Evidence>>({});
  const [step, setStep] = useState(Step.Harness);
  useLocale();
  const [problem, setProblem] = useProductMessage("");
  const [refresh, setRefresh] = useState(0);
  const [focusKey, setFocusKey] = useState("");
  const [focusField, setFocusField] = useState("");
  const [focusAttempt, setFocusAttempt] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null), form = useRef<HTMLFormElement>(null);
  const notifications = useNotifications();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.AGENT_WORKER_WIZARD_V1) === true && status.data.capabilities.includes(SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.AGENT, id: initial?.id ?? "" }, { enabled: active && supported && Boolean(initial), refetchInterval: active && supported ? 5000 : false });
  const update = useCallback((key: string, patch: Partial<Draft>) => setRoutes(previous => previous.map(route => route.key === key ? { ...route, ...patch } : route)), []);
  const report = useCallback((key: string, value: Evidence) => setEvidence(previous => { const old = previous[key]; return old && old.accountError === value.accountError && old.modelError === value.modelError && old.pending === value.pending && old.label === value.label && old.accounts.join("\u0000") === value.accounts.join("\u0000") ? previous : { ...previous, [key]: value }; }), []);
  const mutation = useRetainedMutation(`agent-worker-wizard:${initial?.id ?? "new"}`, ConfigurationQuery.saveAgentWorker, (_result, request) => { notifications.notify({ kind: ToastKind.Success, message: copy("agent-worker-wizard.agentWorkerSaved"), id: request.mutation?.requestId }); saved(); }, (result, request) => result.requestId === request.mutation?.requestId && result.resource?.kind === EntityKind.AGENT && supportsResourceSchema(result.resource) && (!initial || result.resource.id === initial.id));
  const blocked = !supported || mutation.busy || mutation.uncertain || routes.some(route => evidence[route.key]?.pending);
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  useLayoutEffect(() => {
    if (!active) return;
    const container = focusKey ? form.current?.querySelector<HTMLElement>(`[data-source-group="${focusKey}"]`) : form.current;
    const control = focusField ? container?.querySelector<HTMLElement>(focusField === "name" ? ".agent-core input" : `[data-wizard-field="${focusField}"]`) : undefined;
    if (control) { const disclosure = control.closest<HTMLDetailsElement>("details"); if (disclosure) disclosure.open = true; const parent = control.closest<HTMLElement>("[hidden]"); if (parent) parent.hidden = false; control.focus(); } else heading.current?.focus();
  }, [active, step, focusKey, focusField, focusAttempt]);
  const focus = (key = "", field = "") => { setFocusKey(key); setFocusField(field); setFocusAttempt(value => value + 1); };
  const fail = (target: Step, message: OwnedMessage, key = "", field = "") => { setStep(target); setProblem(message); focus(key, field); return false; };
  const validate = (through: Step) => {
    if (!Object.values(Harness).includes(data.harness as Harness)) return fail(Step.Harness, ownedMessage("agent-worker-wizard.chooseSupportedHarness"), "", "harness");
    const keys = routes.map(route => sourceKey(route.source));
    if (through >= Step.Accounts) {
      const count = routes.reduce((count, route) => count + route.accounts.length, 0), ids = routes.flatMap(route => route.accounts.map(account => text(account.id)));
      if (count > 1000 || new Set(ids).size !== count) return fail(Step.Accounts, ownedMessage("agent-worker-wizard.accountLimit"));
      for (const route of routes) if (!evidence[route.key] || evidence[route.key].accountError || keys.filter(key => key === sourceKey(route.source)).length !== 1) return fail(Step.Accounts, ownedMessage(evidence[route.key]?.accountError || "agent-worker-wizard.distinctSources"), route.key, "source");
    }
    if (through >= Step.Model) for (const route of routes) if (!evidence[route.key] || evidence[route.key].modelError) return fail(Step.Model, ownedMessage(evidence[route.key]?.modelError || "agent-worker-wizard.modelEverySource"), route.key, "model");
    if (through >= Step.Configure && (!text(data.name).trim() || new TextEncoder().encode(text(data.name)).byteLength > 256)) return fail(Step.Configure, ownedMessage("agent-worker-wizard.workerNameByteLimit"), "", "name");
    return true;
  };
  const chooseHarness = (harness: Harness) => {
    if (blocked || !active || step !== Step.Harness) return false;
    if (harness === data.harness) return true;
    const next = { ...data, harness, accounts: [], model_id: "" };
    if (encode(next).byteLength > 1 << 20) { setProblem(ownedMessage("agent-worker-wizard.configurationTooLarge")); return false; }
    setData(next); setRoutes([draft({ routing: Routing.Priority })]); setProblem("");
    return true;
  };
  useEffect(() => {
    if (!active || !mutation.error || mutation.uncertain) return;
    setRefresh(value => value + 1);
    if (initial) void current.refetch();
  }, [active, mutation.error, mutation.uncertain, initial?.id, current.refetch]);
  useEffect(() => {
    if (!active || !mutation.error || mutation.uncertain || step !== Step.Configure) return;
    const account = routes.find(route => evidence[route.key]?.accountError);
    const model = routes.find(route => evidence[route.key]?.modelError);
    if (account) fail(Step.Accounts, ownedMessage("agent-worker-wizard.selectedAccountChanged"), account.key, "source");
    else if (model) fail(Step.Model, ownedMessage(evidence[model.key].modelError as MessageKey), model.key, "model");
  }, [active, mutation.error, mutation.uncertain, step, evidence, routes]);
  const advance = () => { if (validate(step)) { setStep(step + 1); setProblem(""); focus(); } };
  const move = (key: string, offset: number) => { setRoutes(previous => { const next = [...previous], index = next.findIndex(route => route.key === key); [next[index], next[index + offset]] = [next[index + offset], next[index]]; return next; }); setProblem(""); focus(key, "source-heading"); };
  return <form ref={form} className="agent-configuration worker-wizard worker-source-wizard" noValidate onInvalidCapture={revealAgentInvalidControl} onSubmit={event => {
    event.preventDefault(); if (!active || blocked || step === Step.Harness) return; if (step !== Step.Configure) { advance(); return; }
    if (!validate(Step.Configure) || stale || initial && (!current.data?.resource || current.error)) return;
    if (!form.current?.checkValidity()) { revealAgentInvalidControl(event); form.current?.querySelector<HTMLElement>("input:invalid, select:invalid, textarea:invalid")?.focus(); return; }
    const next: Document = { ...data }; delete next.accounts; delete next.model_id; delete next.routing; delete next.routes;
    const selections = routes.map(route => ({ selection: route.model ? { case: "modelId" as const, value: route.model.id } : { case: "nativeId" as const, value: route.input.trim() }, expectedModelRevision: route.model?.revision ?? 0n }));
    const sourceRoutes = routes.map(route => { const value: Document = { ...route.original, model_id: route.model?.id ?? "", accounts: route.accounts }; if (route.routing) value.routing = route.routing; else delete value.routing; return value; });
    const expanded = initial?.schemaVersion === 3 || routes.length > 1;
    if (expanded) next.routes = sourceRoutes; else Object.assign(next, sourceRoutes[0]);
    if (encode(next).byteLength > 1 << 20) { fail(Step.Configure, ownedMessage("agent-worker-wizard.configurationTooLarge")); return; }
    void mutation.send({ mutation: { requestId: newRequestId(), id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n }, schemaVersion: expanded ? 3 : 1, documentJson: encode(next), ...(expanded ? { routeModels: selections } : { model: selections[0] }) });
  }}>
    <h2>{initial ? copy("agent-worker-wizard.editAgentWorker") : copy("agent-worker-wizard.newAgentWorker")}</h2><ol className="worker-steps" aria-label={copy("agent-worker-wizard.workerConfigurationSteps")}>{[Step.Harness, Step.Accounts, Step.Model, Step.Configure].map(value => <li key={value} aria-current={step === value ? "step" : undefined} data-completed={value < step}><span>{value}</span><span>{stepName(value)}</span></li>)}</ol><h3 ref={heading} tabIndex={-1}>{stepName(step)}</h3>
    {status.isLoading ? <p role="status">{copy("agent-worker-wizard.checkingServerSupport")}</p> : !supported ? <p role="alert">{copy("agent-worker-wizard.updateSourceServer")}</p> : null}<Problem error={status.error} />
    <fieldset disabled={blocked}>
      <section hidden={step !== Step.Harness}><WorkerHarnessPicker value={data.harness} disabled={blocked || !active} change={chooseHarness} confirm={harness => { if (!chooseHarness(harness)) return; setStep(Step.Accounts); setProblem(""); focus(); }} /></section>
      <div hidden={step !== Step.Accounts}><p>{harnessNames[data.harness as Harness]} <button type="button" disabled={blocked} onClick={() => { setStep(Step.Harness); focus(); }}>{copy("agent-worker-wizard.changeHarness")}</button></p><h4>{copy("agent-worker-wizard.accountSources")}</h4><p>{copy("agent-worker-wizard.subscriptionFirst")}</p></div>
      {routes.map((route, index) => <SourceGroup key={route.key} value={route} index={index} count={routes.length} step={step} harness={data.harness as Harness} active={active && supported} locked={blocked} refresh={refresh} duplicateSources={routes.filter(other => other.key !== route.key).map(other => sourceKey(other.source))} update={update} report={report} move={move} remove={key => { setRoutes(previous => previous.filter(route => route.key !== key)); focus(); }} />)}
      <div hidden={step !== Step.Accounts}><button type="button" disabled={blocked || routes.length >= 1000} onClick={() => { const next = draft({ routing: Routing.Priority }); setRoutes(previous => [...previous, next]); focus(next.key, "source"); }}>{copy("agent-worker-wizard.addSource")}</button><p>{copy("agent-worker-wizard.newSessions")}</p><p>{copy("agent-worker-wizard.nextModels")}</p></div>
      <fieldset hidden={step !== Step.Configure} disabled={blocked || step !== Step.Configure}><ConfigurationFields kind={EntityKind.AGENT} data={data} change={setData} existing={Boolean(initial)} active={active && step === Step.Configure} /><section className="worker-summary" aria-label={copy("agent-worker-wizard.workerConfigurationSummary")}><h4>{copy("agent-worker-wizard.reviewConfiguration")}</h4><p>{harnessNames[data.harness as Harness]}</p><ol>{routes.map(route => <li key={route.key}><strong>{evidence[route.key]?.label}</strong><p>{copy("agent-worker-wizard.modelSummary", { v0: route.input })}</p><p>{copy("agent-worker-wizard.accountsSummary", { v0: evidence[route.key]?.accounts.join(", ") })}</p><p>{copy("agent-worker-wizard.routingSummary", { v0: route.routing === Routing.Priority ? copy("agent-worker-wizard.inOrder") : route.routing || copy("agent-worker-wizard.serverDefault") })}</p></li>)}</ol><p>{copy("agent-worker-wizard.savedCompatibility")}</p></section></fieldset>
    </fieldset>
    {routes.some(route => evidence[route.key]?.modelError === "agent-worker-wizard.selectedModelUnavailable") ? <p role="status">{copy("agent-worker-wizard.selectedModelChanged")}</p> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error || current.error} />{stale ? <p role="alert">{copy("agent-worker-wizard.workerChangedElsewhere")}</p> : null}
    <div className="worker-footer"><button type="button" disabled={blocked} onClick={cancel}>{copy("agent-worker-wizard.cancel")}</button>{step > Step.Harness ? <div><button type="button" disabled={blocked} onClick={() => { setStep(step - 1); setProblem(""); focus(); }}>{copy("agent-worker-wizard.back")}</button><button type="submit" className="primary" disabled={!active || blocked || step === Step.Configure && (stale || Boolean(initial && (!current.data?.resource || current.error)))}>{mutation.busy ? copy("agent-worker-wizard.saving") : step === Step.Configure ? copy("agent-worker-wizard.saveAgentWorker") : copy("agent-worker-wizard.next")}</button></div> : null}</div>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("agent-worker-wizard.retryWorkerSave")}</button> : null}
  </form>;
}
