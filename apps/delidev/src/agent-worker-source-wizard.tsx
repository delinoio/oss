// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { WorkerSourceOrder } from "./worker-source-order";
import { WizardAccountNavigation } from "./wizard-account-navigation";
import { useProviderPages } from "./model-pagination";
import { useWizardAccounts } from "./wizard-pagination";
import { ScrollPicker } from "./scroll-picker";
import { revealWorkerModelOption } from "./worker-model-results";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useCallback, useDeferredValue, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { FailureCode, ApiProtocol, ProviderInventoryCapability, KnownSubscriptionModelCatalogSource, SystemCapability, SystemQuery, ConfigurationQuery, EntityKind, ProviderQuery, ResourceQuery, SubscriptionServiceId, newRequestId, subscriptionServiceHarnesses, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationFields, Harness, Routing, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { SourceKind, SelectedAccount, accountFormatMatches, harnessAPIProtocol, fromKey, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { useRetainedMutation } from "./mutation";
import { NativeModelSettings } from "./native-model-settings";
import { Failure, Problem } from "./ui";
import { revealAgentInvalidControl } from "./agent-configuration";
import { ToastKind, useNotifications } from "./toast-notifications";
import { WorkerHarnessPicker } from "./worker-harness-picker";
import { copy, displayLocale, ownedMessage, useLocale, useProductMessage, type MessageKey, type OwnedMessage } from "./localization";
import { SettingsTaskActions } from "./settings-task";
import { useInSettingsTask } from "./settings-task-context";
import { statusLabel } from "./product-status";

// Groups remain mounted through step changes and reordering. Their stable keys
// own pagination/discovery receipts independently of their priority position.
enum Step { Harness = 1, Accounts, Model, Configure }
const stepName = (step: Step) => copy(step === Step.Harness ? "agent-worker-wizard.harnessStep" : step === Step.Accounts ? "agent-worker-wizard.accountsStep" : step === Step.Model ? "agent-worker-wizard.modelStep" : "agent-worker-wizard.configureStep");
enum SuggestionKind { Known = "known", Endpoint = "endpoint" }
interface ModelSuggestion { nativeId: string; name: string; kind: SuggestionKind; metadata?: Document; hidden?: boolean }
const harnessNames = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };
interface Draft { original: Document; key: string; source?: Source; accounts: Document[]; routing?: string; model?: Document; input: string }
interface Evidence { accountError: MessageKey | ""; modelError: MessageKey | ""; pending: boolean; label: string; accounts: string[] }
function draft(value: Document = {}): Draft {
 const model=object(value.model);const service=text(model.subscription_service),provider=text(model.provider_id);
 const source:Source|undefined=service?{kind:SourceKind.Subscription,id:service}:provider?{kind:SourceKind.Api,id:provider}:undefined;
 return {original:value,key:newRequestId(),source,accounts:items(value.accounts).map(object),routing:text(value.routing)||undefined,model:Object.keys(model).length?model:undefined,input:text(model.native_id)};
}
function showAccountChoice(row: Resource) {
  const data = document(row);
  return data.connection !== null && typeof data.connection === "object" && !Array.isArray(data.connection) && !data.removal && (data.health === "ready" || data.health === "unverified");
}
interface AccountDisplayStatus { connected: boolean; subscription: boolean; health: string; exhausted: boolean; quotaFailed: boolean }
function accountDisplayStatus(data: Document): AccountDisplayStatus {
  return { connected: Boolean(data.connection), subscription: data.type === "subscription", health: text(data.health), exhausted: data.confirmed_exhausted === true, quotaFailed: object(data.subscription).quota_state === "failed" };
}
function accountStatus(data: AccountDisplayStatus) {
  const connection = copy(data.connected ? "agent-worker-wizard.connected" : "agent-worker-wizard.disconnected");
  const health = data.health;
  if (!data.subscription) return copy("agent-worker-wizard.connectionHealth", { v0: connection, v1: statusLabel(health) });
  const quota = copy(data.exhausted ? "agent-worker-wizard.quotaExhausted" : data.quotaFailed ? "agent-worker-wizard.quotaFailed" : "agent-worker-wizard.quotaUnknown");
  return copy("agent-worker-wizard.subscriptionStatus", { v0: connection, v1: health && !["ready", "unverified", "disconnected"].includes(health) ? copy("agent-worker-wizard.healthSuffix", { v0: statusLabel(health) }) : "", v2: quota });
}

function SourceGroup({ value, index, count, step, displayed, harness, active, locked, duplicateSources, update, report, remove, openAccounts }: {
  value: Draft; index: number; count: number; step: Step; displayed: boolean; harness: Harness; active: boolean; locked: boolean; duplicateSources: string[];
  update: (key: string, patch: Partial<Draft>) => void; report: (key: string, value: Evidence) => void; openAccounts?: (source: Source) => void; remove: (key: string) => void;
}) {
  useLocale();
  const [known, setKnown] = useState<Record<string, Resource | undefined>>({});
  const [popup, setPopup] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const [nativePending, setNativePending] = useState(false);
  const listID = useId();
  const group = useRef<HTMLElement>(null), accountRoot = useRef<HTMLDivElement>(null), modelRoot = useRef<HTMLUListElement>(null), modelInput = useRef<HTMLInputElement>(null);
  const source = value.source, ids = value.accounts.map(account => text(account.id));
  const sourceID = sourceKey(source);
  const query = useDeferredValue(value.input);
  const providers = useProviderPages("", active && step === Step.Accounts, true);
  const provider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: source?.kind === SourceKind.Api ? source.id : "" }, { enabled: active && source?.kind === SourceKind.Api });
  const rows = useWizardAccounts(source, source?.kind === SourceKind.Api && providers.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) ? harnessAPIProtocol(harness) : ApiProtocol.UNSPECIFIED, active && Boolean(source) && step === Step.Accounts);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const knownSupported = source?.id !== SubscriptionServiceId.OpenCodeGo && status.data?.capabilities.includes(SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1) === true;
  const knownModels = useQuery(ProviderQuery.listKnownSubscriptionModels, { subscriptionService: wireService(source) }, { enabled: active && knownSupported && source?.kind === SourceKind.Subscription && step === Step.Model });
  const date = knownModels.data?.updatedAt ?? "";
  const parsedDate = new Date(`${date}T00:00:00Z`);
  const knownValid = knownSupported && source?.kind === SourceKind.Subscription && knownModels.data?.subscriptionService === wireService(source) && knownModels.data.models.length <= 200 && /^sha256:[a-f0-9]{64}$/.test(knownModels.data.catalogVersion) && /^\d{4}-\d{2}-\d{2}$/.test(date) && Number.isFinite(parsedDate.getTime()) && parsedDate.toISOString().slice(0, 10) === date && [KnownSubscriptionModelCatalogSource.BUNDLED, KnownSubscriptionModelCatalogSource.CACHE, KnownSubscriptionModelCatalogSource.ONLINE].includes(knownModels.data.source) && knownModels.data.models.every(row => row.nativeId && row.displayName) && new Set(knownModels.data.models.map(row => row.nativeId)).size === knownModels.data.models.length;
  const [labels, setLabels] = useState<Record<string, { name: string; status: AccountDisplayStatus }>>({});
  const selectedIds = ids.join("\u0000");
  const retainDisplay = useCallback((row: Resource) => setLabels(previous => ({ ...previous, [row.id]: { name: resourceName(row), status: accountDisplayStatus(document(row)) } })), []);
  const remember = useCallback((id: string, row?: Resource) => { if (selectedIds.split("\u0000").includes(id)) { setKnown(previous => previous[id] === row ? previous : { ...previous, [id]: row }); if (row) retainDisplay(row); } }, [selectedIds, retainDisplay]);
  useEffect(() => { setKnown(previous => Object.fromEntries(Object.entries(previous).filter(([id]) => ids.includes(id)))); setLabels(previous => Object.fromEntries(Object.entries(previous).filter(([id]) => ids.includes(id)))); }, [selectedIds]);
  const pageValid = rows.loaded;
  const label = source?.kind === SourceKind.Subscription ? `${subscriptionServiceNames[source.id as SubscriptionServiceId]} ${copy("agent-worker-wizard.subscription")}` : provider.data?.resource ? resourceName(provider.data.resource) : source?.id || copy("agent-worker-wizard.chooseSource");
  const selectedRows = ids.flatMap(id => known[id] ? [known[id]!] : []);
  const refreshAccount = selectedRows.find(row => document(row).connection && document(row).enabled !== false && !document(row).removal);
  const endpoint = useQuery(ProviderQuery.listEndpointModels,{accountId:refreshAccount?.id??"",expectedAccountRevision:refreshAccount?.revision??0n,expectedProviderRevision:provider.data?.resource?.revision??0n},{enabled:active && step===Step.Model && source?.kind===SourceKind.Api && Boolean(refreshAccount && provider.data?.resource)});
  const endpointValid=source?.kind===SourceKind.Api && endpoint.data?.accountId===refreshAccount?.id && endpoint.data?.accountRevision===refreshAccount?.revision && endpoint.data?.providerId===source.id && endpoint.data?.providerRevision===provider.data?.resource?.revision && endpoint.data?.connectionId===text(object(document(refreshAccount).connection).id) && endpoint.data.models.length<=10000 && endpoint.data.models.every(row=>row.nativeId && new TextEncoder().encode(row.nativeId).length<=256 && row.inputModalities.every(value=>["text","image"].includes(value))) && new Set(endpoint.data.models.map(row=>row.nativeId)).size===endpoint.data.models.length;
  const pending = nativePending;
  const accountError: MessageKey | "" = !source || source.kind === SourceKind.Subscription && subscriptionServiceHarnesses[source.id as SubscriptionServiceId] !== harness ? "agent-worker-wizard.chooseAccountSourceForHarness"
    : duplicateSources.includes(sourceID) ? "agent-worker-wizard.duplicateSource"
    : !ids.length || ids.some(id => !known[id] || !sameSource(known[id]!, source)) ? "agent-worker-wizard.selectCurrentAccount"
    : selectedRows.some(row => !accountFormatMatches(row, provider.data?.resource, harness)) ? "agent-worker-wizard.accountApiFormatMismatch"
    : value.routing === Routing.Fixed && ids.length !== 1 || value.accounts.some(account => !Number.isInteger(account.weight) || Number(account.weight) < 1 || Number(account.weight) > 1000) ? "agent-worker-wizard.fixedRoutingWeights" : "";
  const modelError: MessageKey | "" = !value.input.trim() || new TextEncoder().encode(value.input.trim()).byteLength > 256 ? "agent-worker-wizard.selectModelFromSource" : "";
  const accountNames = selectedRows.map(resourceName).join("\u0000");
  useEffect(() => { report(value.key, { accountError, modelError, pending, label, accounts: accountNames.split("\u0000").filter(Boolean) }); }, [value.key, accountError, modelError, pending, label, accountNames, report]);
  useEffect(() => { if (step !== Step.Model) { setPopup(false); setHighlight(-1); } }, [step]);
  useLayoutEffect(() => { if (active && step === Step.Model && popup && highlight >= 0) revealWorkerModelOption(modelRoot.current?.children.item(highlight) as HTMLElement | null); }, [active, step, popup, highlight]);
  const chooseSource = (key: string) => { if (key !== sourceID) update(value.key, { source: fromKey(key), accounts: [], model: undefined, input: "" }); group.current?.querySelector<HTMLElement>('[data-wizard-field="source-heading"]')?.focus(); };
  const search=query.trim().toLocaleLowerCase();
  const suggestions:ModelSuggestion[]=[];
  if(endpointValid)for(const row of endpoint.data!.models){if(!search || `${row.displayName} ${row.nativeId}`.toLocaleLowerCase().includes(search))suggestions.push({nativeId:row.nativeId,name:row.displayName||row.nativeId,kind:SuggestionKind.Endpoint,metadata:{name:row.displayName||row.nativeId,context_limit:row.contextLimit===undefined || row.contextLimit>BigInt(Number.MAX_SAFE_INTEGER)?undefined:Number(row.contextLimit),input_modalities:row.inputModalities,metadata_source:"user-declared"}});if(suggestions.length===200)break;}
  if(knownValid)for(const row of knownModels.data!.models){if(!search || `${row.displayName} ${row.nativeId}`.toLocaleLowerCase().includes(search))suggestions.push({nativeId:row.nativeId,name:row.displayName,kind:SuggestionKind.Known});}
  const catalogDate=knownValid?new Intl.DateTimeFormat(displayLocale(),{year:"numeric",month:"short",day:"numeric",timeZone:"UTC"}).format(parsedDate):"";
  const pick=(row?:ModelSuggestion)=>{if(!active || locked || step!==Step.Model)return;update(value.key,{model:row?.metadata,input:row?.nativeId??value.input});setPopup(false);setHighlight(-1);};
  const entries = providers.rows.filter(entry => entry.enabled && entry.providerId);
  const changeRouting = (routing: string) => update(value.key, { routing: routing || undefined });
  return <section ref={group} className="worker-source-group" data-source-group={value.key} hidden={step !== Step.Accounts && step !== Step.Model || step === Step.Accounts && !displayed} aria-label={copy("agent-worker-wizard.sourceLabel", { v0: index + 1 })}>
    <fieldset disabled={locked}><header><h4 tabIndex={-1} data-wizard-field="source-heading">{index + 1} · {label}</h4>{step === Step.Accounts ? <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Delete} type="button" disabled={count === 1} aria-label={copy("agent-worker-wizard.removeSource", { v0: index + 1 })} onClick={() => remove(value.key)}>{copy("agent-worker-wizard.remove")}</SettingsActionButton></div> : null}</header>
    {ids.map(id => <SelectedAccount key={id} id={id} active={active} refresh={0} read={remember} />)}
    <div hidden={step !== Step.Accounts}>

      <p>{copy(source?.kind === SourceKind.Subscription ? "agent-worker-wizard.subscription" : "agent-worker-wizard.apiProvider")}</p><div>
        <div data-wizard-field="source"><ScrollPicker label={copy("agent-worker-wizard.sourceLabel", { v0: index + 1 })} value={sourceID} selectedLabel={label} placeholder={copy("agent-worker-wizard.selectSource")} active={active && step === Step.Accounts && displayed} disabled={locked} query={providers} change={chooseSource} options={[
          ...Object.values(SubscriptionServiceId).filter(service => subscriptionServiceHarnesses[service] === harness).map(service => ({ id: `${SourceKind.Subscription}:${service}`, label: `${subscriptionServiceNames[service]} ${copy("agent-worker-wizard.subscription")}`, disabled: duplicateSources.includes(`${SourceKind.Subscription}:${service}`) })),
          ...(source?.kind === SourceKind.Api && !entries.some(entry => entry.providerId === source.id) ? [{ id: sourceID, label: label + copy("agent-worker-wizard.retainedSourceSuffix") }] : []),
          ...entries.map(entry => ({ id: `${SourceKind.Api}:${entry.providerId}`, label: entry.displayName, disabled: duplicateSources.includes(`${SourceKind.Api}:${entry.providerId}`) })),
        ]} /></div>
        <Failure failure={providers.error?.failure} />{providers.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingApiSources")}</p> : providers.loaded && entries.length === 0 ? <p>{copy("agent-worker-wizard.noApiSources")}</p> : null}
      </div>
        <Problem error={provider.error} />{provider.error ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || locked || provider.isFetching} onClick={() => void provider.refetch()}>{copy("agent-worker-wizard.retrySourceDetails")}</SettingsActionButton> : null}
        <Failure failure={rows.error?.failure} />{source && rows.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingAccounts")}</p> : null}{rows.error && rows.data ? <p role="status">{copy("agent-worker-wizard.refreshFailedStaleAccounts")}</p> : null}{rows.data && !pageValid ? <p role="alert">{copy("agent-worker-wizard.invalidAccountPage")}</p> : null}
        <div ref={accountRoot} className="worker-account-list">{ids.map(id => <label className="worker-account-row" key={id}><input type="checkbox" data-wizard-field="account" aria-label={copy("agent-worker-wizard.selectAccount", { v0: known[id] ? resourceName(known[id]) : labels[id]?.name || id })} checked onChange={() => update(value.key, { accounts: value.accounts.filter(account => account.id !== id) })} /><span><strong>{known[id] ? resourceName(known[id]) : labels[id]?.name || id}</strong><small>{known[id] ? accountStatus(accountDisplayStatus(document(known[id]))) : labels[id] ? accountStatus(labels[id].status) : undefined}</small><small>{copy("agent-worker-wizard.executionCheck")}</small></span></label>)}<ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={rows} root={accountRoot} active={active && step === Step.Accounts && displayed && !locked}>{payload => payload.filter(row => !ids.includes(row.id) && showAccountChoice(row) && accountFormatMatches(row, provider.data?.resource, harness)).map(row => <label className="worker-account-row" key={row.id}><input type="checkbox" data-wizard-field="account" checked={false} disabled={Boolean(rows.error)} onChange={() => { retainDisplay(row); update(value.key, { accounts: [...value.accounts, { id: row.id, weight: 1 }] }); }} /><span><strong>{resourceName(row)}</strong><small>{accountStatus(accountDisplayStatus(document(row)))}</small><small>{copy("agent-worker-wizard.executionCheck")}</small></span></label>)}</ScrollPayloadWindow><ScrollContinuation showErrors={false} query={rows} root={accountRoot} active={active && step === Step.Accounts && displayed && !locked && Boolean(source)} label={copy("agent-worker-wizard.sourceAccountPages", { v0: index + 1 })} /></div>
        {source && rows.loaded && !provider.error && !provider.isFetching && !rows.rows.length && !rows.nextPageToken && !rows.error && !rows.isFetching ? <div className="worker-account-empty"><p>{copy("agent-worker-wizard.noAccountsForSource")}</p><WizardAccountNavigation source={source} active={active && displayed && !locked} navigate={openAccounts} /></div> : null}
        {source && rows.error ? <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={!active || locked || rows.isFetching} onClick={rows.error.stalled || rows.error.failure.code === FailureCode.CursorExpired ? rows.reload : rows.retry}>{copy(rows.error.stalled || rows.error.failure.code === FailureCode.CursorExpired ? "pagination.reload" : "agent-worker-wizard.retryAccounts")}</SettingsActionButton> : null}
      <p>{copy("agent-worker-wizard.accountsSelected", { v0: ids.length })}</p>
      <section className="worker-routing"><h4>{copy("agent-worker-wizard.routingOptions")}</h4>{!ids.length ? <p>{copy("agent-worker-wizard.routingEmpty")}</p> : null}<label>{copy("agent-worker-wizard.accountRouting")}<select data-wizard-field="routing" value={value.routing || ""} onChange={event => changeRouting(event.target.value)}><option value="">{copy("agent-worker-wizard.serverDefault")}</option>{Object.values(Routing).map(policy => <option key={policy} value={policy} disabled={policy === Routing.Fixed && ids.length !== 1}>{policy === Routing.Priority ? copy("agent-worker-wizard.inOrder") : policy}</option>)}</select></label><ol>{value.accounts.map((account, position) => <li key={text(account.id)}><strong>{known[text(account.id)] ? resourceName(known[text(account.id)]) : text(account.id)}</strong><label>{copy("agent-worker-wizard.weightForAccount", { v0: position + 1 })}<input data-wizard-field="weight" type="number" min={1} max={1000} value={Number(account.weight)} onChange={event => update(value.key, { accounts: value.accounts.map((item, i) => i === position ? { ...item, weight: Number(event.target.value) } : item) })} /></label><SettingsActionButton icon={SettingsActionIcon.Up} type="button" disabled={position === 0} aria-label={copy("agent-worker-wizard.moveAccountUp", { v0: position + 1 })} onClick={() => { const accounts = [...value.accounts]; [accounts[position - 1], accounts[position]] = [accounts[position], accounts[position - 1]]; update(value.key, { accounts }); }}>{copy("agent-worker-wizard.up")}</SettingsActionButton></li>)}</ol></section>
    </div>
    <div hidden={step !== Step.Model}>
      <p>{copy("agent-worker-wizard.endpointSuggestions")}</p>
      <div className="worker-model-combobox"><label htmlFor={`${listID}-input`}>{copy("agent-worker-wizard.modelForSource", { v0: label })}</label><input ref={modelInput} id={`${listID}-input`} data-wizard-field="model" role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-controls={listID} aria-activedescendant={popup && highlight >= 0 && highlight < suggestions.length + (value.input.trim() ? 1 : 0) ? `${listID}-${highlight}` : undefined} value={value.input} autoComplete="off" onFocus={() => setPopup(true)} onBlur={event => { if (!modelRoot.current?.closest(".worker-model-results")?.contains(event.relatedTarget as Node | null)) setPopup(false); }} onChange={event => { update(value.key, { input: event.target.value, model: undefined }); setPopup(true); setHighlight(-1); }} onKeyDown={event => {
        if (event.nativeEvent.isComposing) return;
        if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); const count = suggestions.length + (value.input.trim() ? 1 : 0); setPopup(true); setHighlight(position => count === 0 ? -1 : event.key === "ArrowDown" ? Math.min(position + 1, count - 1) : position <= 0 ? count - 1 : position - 1); }
        else if (popup && ["Home", "End"].includes(event.key)) { event.preventDefault(); setHighlight(event.key === "Home" ? 0 : suggestions.length + (value.input.trim() ? 1 : 0) - 1); }
        else if (event.key === "Escape" && popup) { event.preventDefault(); event.stopPropagation(); setPopup(false); }
        else if (event.key === "Enter" && popup) { event.preventDefault(); pick(suggestions[highlight]); }
      }} /><div className="worker-model-results" onKeyDown={event => { if (event.key === "Escape" && popup && !event.nativeEvent.isComposing) { event.preventDefault(); event.stopPropagation(); (event.currentTarget.previousElementSibling as HTMLInputElement | null)?.focus({ preventScroll: true }); setPopup(false); } }} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null) && event.relatedTarget !== event.currentTarget.previousElementSibling) setPopup(false); }} role="region" tabIndex={0} aria-label={copy("agent-worker-wizard.modelsForSource", { v0: label })}>{popup ? <ul ref={modelRoot} onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); modelInput.current?.focus(); setPopup(false); } }} id={listID} role="listbox" aria-label={copy("agent-worker-wizard.modelsForSource", { v0: label })}>{suggestions.map((row, position) => <li key={`${row.kind}:${row.nativeId}`} id={`${listID}-${position}`} role="option" aria-selected={highlight === position} onMouseDown={event => event.preventDefault()} onClick={() => pick(row)}><div className="worker-model-heading"><strong>{row.name}</strong><span className="worker-model-origin">{copy(row.kind === SuggestionKind.Known ? "agent-worker-wizard.knownModelOrigin" : "agent-worker-wizard.endpointModelOrigin")}</span></div><small>{row.nativeId}{row.hidden === true ? copy("agent-worker-wizard.hiddenSuffix") : ""}</small></li>)}{value.input.trim() ? <li id={`${listID}-${suggestions.length}`} role="option" aria-selected={highlight === suggestions.length} onMouseDown={event => event.preventDefault()} onClick={() => pick()}>{copy("agent-worker-wizard.useExactModelId", { v0: value.input.trim() })}</li> : null}</ul> : null}
      <Problem error={endpoint.error || knownModels.error} />{endpoint.isFetching || knownModels.isFetching?<p role="status">{copy("agent-worker-wizard.loadingModelCatalog")}</p>:null}{source?.kind===SourceKind.Api && endpoint.data && !endpointValid?<p role="alert">{copy("agent-worker-wizard.invalidModelPage")}</p>:null}
      {source?.kind === SourceKind.Subscription ? <>{knownValid ? <p>{copy("agent-worker-wizard.knownModelsCatalogUpdated", { v0: catalogDate, v1: knownModels.data!.source === KnownSubscriptionModelCatalogSource.CACHE ? copy("agent-worker-wizard.cachedCatalog") : knownModels.data!.source === KnownSubscriptionModelCatalogSource.BUNDLED ? copy("agent-worker-wizard.builtInCatalog") : "" })}</p> : knownModels.data ? <p role="alert">{copy("agent-worker-wizard.knownCatalogUnavailable")}</p> : null}{status.data && !knownSupported ? <p>{copy("agent-worker-wizard.updateServerForKnownModels")}</p> : null}</> : null}
      </div></div>
      <SettingsActionButton icon={SettingsActionIcon.Refresh} type="button" disabled={locked || endpoint.isFetching || knownModels.isFetching || source?.kind===SourceKind.Api && !refreshAccount} onClick={()=>{if(source?.kind===SourceKind.Api && refreshAccount)void endpoint.refetch();if(source?.kind===SourceKind.Subscription && knownSupported)void knownModels.refetch();}}>{copy("agent-worker-wizard.refreshModelsEndpoint")}</SettingsActionButton>
      <p>{copy(source?.kind===SourceKind.Subscription?"agent-worker-wizard.subscriptionModelAvailability":"agent-worker-wizard.endpointReadOnly")}</p>
      {harness === Harness.Codex && source?.kind === SourceKind.Api ? <><NativeModelSettings active={active && step === Step.Model} selectedAccounts={selectedRows} pendingOperation={setNativePending} createModel={model => update(value.key, { input: text(model.native_id), model: {name:text(model.name),input_modalities:items(model.input_modalities),context_limit:model.context_limit,metadata_source:"user-declared"} })} />{selectedRows.some(row => !accountFormatMatches(row, provider.data?.resource, harness)) ? <p>{copy("agent-worker-wizard.codexResponses")}</p> : null}</> : null}
      <p>{copy("agent-worker-wizard.modelReadiness")}</p>
    </div></fieldset>

  </section>;
}

export function AgentWorkerSourceWizard({ initial, active, saved, cancel, openAccounts }: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void; openAccounts?: (source: Source) => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : newConfiguration(EntityKind.AGENT));
  const [routes, setRoutes] = useState<Draft[]>(() => initial && items(document(initial).routes).length ? items(document(initial).routes).map(item => draft(object(item))) : [draft({routing:Routing.Priority})]);
  const [selectedKey, setSelectedKey] = useState("");
  const formID = useId();
  const inTask = useInSettingsTask();
  const [evidence, setEvidence] = useState<Record<string, Evidence>>({});
  const [step, setStep] = useState(Step.Harness);
  useLocale();
  const [problem, setProblem] = useProductMessage("");
  const [focusKey, setFocusKey] = useState("");
  const [focusField, setFocusField] = useState("");
  const [focusAttempt, setFocusAttempt] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null), form = useRef<HTMLFormElement>(null);
  const displayedKey = routes.some(route => route.key === selectedKey) ? selectedKey : routes[0]?.key;
  const notifications = useNotifications();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.AGENT, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const update = useCallback((key: string, patch: Partial<Draft>) => setRoutes(previous => previous.map(route => route.key === key ? { ...route, ...patch } : route)), []);
  const report = useCallback((key: string, value: Evidence) => setEvidence(previous => { const old = previous[key]; return old && old.accountError === value.accountError && old.modelError === value.modelError && old.pending === value.pending && old.label === value.label && old.accounts.join("\u0000") === value.accounts.join("\u0000") ? previous : { ...previous, [key]: value }; }), []);
  const mutation = useRetainedMutation(`agent-worker-wizard:${initial?.id ?? "new"}`, ConfigurationQuery.saveAgentWorker, (_result, request) => { notifications.notify({ kind: ToastKind.Success, message: copy("agent-worker-wizard.agentWorkerSaved"), id: request.mutation?.requestId }); saved(); }, (result, request) => result.requestId === request.mutation?.requestId && result.resource?.kind === EntityKind.AGENT && supportsResourceSchema(result.resource) && (!initial || result.resource.id === initial.id));
  const blocked = mutation.busy || mutation.uncertain || routes.some(route => evidence[route.key]?.pending);
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  useLayoutEffect(() => {
    if (!active) return;
    const container = focusKey ? form.current?.querySelector<HTMLElement>(`[data-source-group="${focusKey}"]`) : form.current;
    const control = (focusField === "weight" ? container?.querySelector<HTMLElement>('[data-wizard-field="weight"]:invalid') : undefined) ?? (focusField ? container?.querySelector<HTMLElement>(focusField === "name" ? ".agent-core input" : `[data-wizard-field="${focusField}"]`) : undefined);
    if (control) { const disclosure = control.closest<HTMLDetailsElement>("details"); if (disclosure) disclosure.open = true; const parent = control.closest<HTMLElement>("[hidden]"); if (parent) parent.hidden = false; (control.querySelector<HTMLElement>('[role="combobox"]') ?? control).focus(); } else heading.current?.focus();
  }, [active, step, focusKey, focusField, focusAttempt]);
  const focus = (key = "", field = "") => { if (key) setSelectedKey(key); setFocusKey(key); setFocusField(field); setFocusAttempt(value => value + 1); };
  const fail = (target: Step, message: OwnedMessage, key = "", field = "") => { setStep(target); setProblem(message); focus(key, field); return false; };
  const validate = (through: Step) => {
    if (!Object.values(Harness).includes(data.harness as Harness)) return fail(Step.Harness, ownedMessage("agent-worker-wizard.chooseSupportedHarness"), "", "harness");
    const keys = routes.map(route => sourceKey(route.source));
    if (through >= Step.Accounts) {
      const count = routes.reduce((count, route) => count + route.accounts.length, 0), ids = routes.flatMap(route => route.accounts.map(account => text(account.id)));
      if (count > 1000 || new Set(ids).size !== count) return fail(Step.Accounts, ownedMessage("agent-worker-wizard.accountLimit"));
      for (const route of routes) if (!evidence[route.key] || evidence[route.key].accountError || keys.filter(key => key === sourceKey(route.source)).length !== 1) return fail(Step.Accounts, ownedMessage(evidence[route.key]?.accountError || "agent-worker-wizard.distinctSources"), route.key, !route.source || evidence[route.key]?.accountError === "agent-worker-wizard.duplicateSource" ? "source" : route.accounts.some(account => !Number.isInteger(account.weight) || Number(account.weight) < 1 || Number(account.weight) > 1000) ? "weight" : evidence[route.key]?.accountError === "agent-worker-wizard.fixedRoutingWeights" ? "routing" : "account");
    }
    if (through >= Step.Model) for (const route of routes) if (!evidence[route.key] || evidence[route.key].modelError) return fail(Step.Model, ownedMessage(evidence[route.key]?.modelError || "agent-worker-wizard.modelEverySource"), route.key, "model");
    if (through >= Step.Configure && (!text(data.name).trim() || new TextEncoder().encode(text(data.name)).byteLength > 256)) return fail(Step.Configure, ownedMessage("agent-worker-wizard.workerNameByteLimit"), "", "name");
    return true;
  };
  const chooseHarness = (harness: Harness) => {
    if (blocked || !active || step !== Step.Harness || harness === data.harness) return;
    setData(previous => ({ ...previous, harness })); setRoutes([draft({ routing: Routing.Priority })]); setProblem("");
  };
  const advance = () => { if (validate(step)) { setStep(step + 1); setProblem(""); focus(); } };

  return <form id={formID} ref={form} className="agent-configuration worker-wizard worker-source-wizard" data-wizard-step={step} noValidate onInvalidCapture={revealAgentInvalidControl} onSubmit={event => {
    event.preventDefault(); if (!active || blocked || step === Step.Harness) return; if (step !== Step.Configure) { advance(); return; }
    if (!validate(Step.Configure) || stale || initial && (!current.data?.resource || current.error)) return;
    if (!form.current?.checkValidity()) { revealAgentInvalidControl(event); form.current?.querySelector<HTMLElement>("input:invalid, select:invalid, textarea:invalid")?.focus(); return; }
    const next: Document = { ...data }; delete next.reconfiguration_required; delete next.accounts; delete next.model_id; delete next.routing; delete next.routes;
    const selections=routes.map(route=>({selection:{case:"nativeId" as const,value:route.input.trim()}}));
    next.routes=routes.map(route=>{const model:Document={...route.model,native_id:route.input.trim(),metadata_source:route.model?.metadata_source??"unknown",input_modalities:route.model?.input_modalities??["text"],...(route.source?.kind===SourceKind.Api?{provider_id:route.source.id}:{subscription_service:route.source?.id})};const result:Document={model,accounts:route.accounts};if(route.routing)result.routing=route.routing;return result;});

    if (encode(next).byteLength > 1 << 20) { fail(Step.Configure, ownedMessage("agent-worker-wizard.configurationTooLarge")); return; }
    void mutation.send({ mutation: { requestId: newRequestId(), id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n }, schemaVersion:4,documentJson:encode(next),routeModels:selections });
  }}>
    {!inTask ? <h2>{initial ? copy("agent-worker-wizard.editAgentWorker") : copy("agent-worker-wizard.newAgentWorker")}</h2> : null}<ol className="worker-steps" aria-label={copy("agent-worker-wizard.workerConfigurationSteps")}>{[Step.Harness, Step.Accounts, Step.Model, Step.Configure].map(value => <li key={value} aria-current={step === value ? "step" : undefined} data-completed={value < step}><span>{value}</span><span>{stepName(value)}</span></li>)}</ol><h3 ref={heading} tabIndex={-1}>{stepName(step)}</h3>
    <fieldset disabled={mutation.busy || mutation.uncertain}>
      <section hidden={step !== Step.Harness}><WorkerHarnessPicker value={data.harness} disabled={blocked || !active} change={chooseHarness} confirm={harness => { chooseHarness(harness); setStep(Step.Accounts); setProblem(""); focus(); }} /></section>
      <div hidden={step !== Step.Accounts}><p>{harnessNames[data.harness as Harness]} <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={blocked} onClick={() => { setStep(Step.Harness); focus(); }}>{copy("agent-worker-wizard.changeHarness")}</SettingsActionButton></p></div>
      <div className={step === Step.Accounts ? "worker-accounts-workspace" : undefined}>
        <aside className="worker-source-list" hidden={step !== Step.Accounts} aria-label={copy("agent-worker-wizard.accountSources")}><h4>{copy("agent-worker-wizard.accountSources")}</h4><p>{copy("agent-worker-wizard.usedInOrder")}</p><WorkerSourceOrder ids={routes.map(route => route.key)} names={new Map(routes.map(route => [route.key, evidence[route.key]?.label || copy("agent-worker-wizard.chooseSource")]))} counts={new Map(routes.map(route => [route.key, route.accounts.length]))} selected={displayedKey ?? ""} select={setSelectedKey} active={active && step === Step.Accounts} editable={!blocked} change={order => { setRoutes(previous => order.map(key => previous.find(route => route.key === key)!)); setProblem(""); }} /><SettingsActionButton icon={SettingsActionIcon.Add} decorativePrefix="+ " type="button" disabled={blocked || routes.length >= 1000} onClick={() => { const next = draft({ routing: Routing.Priority }); setRoutes(previous => [...previous, next]); focus(next.key, "source"); }}>{copy("agent-worker-wizard.addSource")}</SettingsActionButton></aside>
        <div className="worker-source-details">
      {routes.map((route, index) => <SourceGroup key={route.key} value={route} index={index} count={routes.length} step={step} displayed={displayedKey === route.key} harness={data.harness as Harness} active={active} locked={blocked} duplicateSources={routes.filter(other => other.key !== route.key).map(other => sourceKey(other.source))} update={update} report={report} openAccounts={openAccounts} remove={key => { const position = routes.findIndex(route => route.key === key); const remaining = routes.filter(route => route.key !== key); setRoutes(remaining); focus(remaining[Math.min(position, remaining.length - 1)]?.key, "source-heading"); }} />)}
        </div>
      </div><div hidden={step !== Step.Accounts}><p>{copy("agent-worker-wizard.newSessions")}</p><p>{copy("agent-worker-wizard.nextModels")}</p></div>
      <fieldset hidden={step !== Step.Configure} disabled={blocked || step !== Step.Configure}><ConfigurationFields disabled={blocked} kind={EntityKind.AGENT} data={data} change={setData} existing={Boolean(initial)} active={active && step === Step.Configure} workerWizard /><section className="worker-summary" aria-label={copy("agent-worker-wizard.workerConfigurationSummary")}><h4>{copy("agent-worker-wizard.reviewConfiguration")}</h4><p>{harnessNames[data.harness as Harness]}</p><ol>{routes.map(route => <li key={route.key}><strong>{evidence[route.key]?.label}</strong><p>{copy("agent-worker-wizard.modelSummary", { v0: route.input })}</p><p>{copy("agent-worker-wizard.accountsSummary", { v0: evidence[route.key]?.accounts.join(", ") })}</p><p>{copy("agent-worker-wizard.routingSummary", { v0: route.routing === Routing.Priority ? copy("agent-worker-wizard.inOrder") : route.routing || copy("agent-worker-wizard.serverDefault") })}</p></li>)}</ol><p>{copy("agent-worker-wizard.savedCompatibility")}</p></section></fieldset>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}{initial && data.reconfiguration_required === true ? <p role="status">{copy("agent-worker-wizard.reconfigurationRequired")}</p> : null}<Problem error={mutation.error} /><Problem error={current.error} actions={<SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || blocked || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</SettingsActionButton>} />{stale ? <p role="alert">{copy("agent-worker-wizard.workerChangedElsewhere")}</p> : null}
    {step > Step.Harness || !inTask ? <SettingsTaskActions className="worker-footer" form={formID}>{!inTask ? <SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" disabled={blocked} onClick={cancel}>{copy("agent-worker-wizard.cancel")}</SettingsActionButton> : null}{step > Step.Harness ? <div><SettingsActionButton icon={SettingsActionIcon.Back} type="button" disabled={blocked} onClick={() => { setStep(step - 1); setProblem(""); focus(); }}>{copy("agent-worker-wizard.back")}</SettingsActionButton><SettingsActionButton icon={step === Step.Configure ? SettingsActionIcon.Save : SettingsActionIcon.Next} type="submit" form={formID} className="primary" disabled={!active || blocked || step === Step.Configure && (stale || Boolean(initial && (!current.data?.resource || current.error)))}>{mutation.busy ? copy("agent-worker-wizard.saving") : step === Step.Configure ? copy("agent-worker-wizard.saveAgentWorker") : copy("agent-worker-wizard.next")}</SettingsActionButton></div> : null}</SettingsTaskActions> : null}{mutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("agent-worker-wizard.retryWorkerSave")}</SettingsActionButton> : null}
  </form>;
}
