// SPDX-License-Identifier: Apache-2.0
import { useCallback, useDeferredValue, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ApiProtocol, ProviderInventoryCapability, AccountTypeFilter, KnownSubscriptionModelCatalogSource, ConfigurationQuery, EntityKind, ProviderQuery, ResourceQuery, SubscriptionServiceId, SystemCapability, SystemQuery, newRequestId, subscriptionServiceHarnesses, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationFields, Harness, Routing, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { NativeModelSettings } from "./native-model-settings";
import { Problem } from "./ui";
import { revealAgentInvalidControl } from "./agent-configuration";
import { ToastKind, useNotifications } from "./toast-notifications";
import { copy, useLocale } from "./localization";
import "./agent-worker-wizard.css";
import { SourceKind, SelectedAccount, accountFormatMatches, harnessAPIProtocol, fromKey, modelSource, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { WorkerHarnessPicker } from "./worker-harness-picker";
import { AgentWorkerSourceWizard } from "./agent-worker-source-wizard";

enum Step { Harness = 1, Accounts, Model, Configure }
enum AccountHealth { Disconnected = "disconnected", Unverified = "unverified", Ready = "ready", Expired = "expired", Revoked = "revoked", Failed = "failed" }
enum SuggestionKind { Known = "Known", Saved = "Saved" }
interface ModelSuggestion { nativeId: string; name: string; kind: SuggestionKind; resource?: Resource }
const steps = [Step.Harness, Step.Accounts, Step.Model, Step.Configure];
const stepName = (step: Step) => copy(step === Step.Harness ? "agent-worker-wizard.harnessStep" : step === Step.Accounts ? "agent-worker-wizard.accountsStep" : step === Step.Model ? "agent-worker-wizard.modelStep" : "agent-worker-wizard.configureStep");
const harnessNames: Record<Harness, string> = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };
function validCatalogDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T00:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}
// Choice visibility does not change retained selections or execution eligibility.
// Keep the complete source page for validation, pagination and selected reads.
function showAccountChoice(row: Resource) {
  const data = document(row);
  return data.connection !== null && typeof data.connection === "object" && !Array.isArray(data.connection) && !data.removal
    && (data.health === AccountHealth.Ready || data.health === AccountHealth.Unverified);
}

function LegacyAgentWorkerWizard({ initial, active, saved, cancel }: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  useLocale();
  const [data, setData] = useState<Document>(() => initial ? document(initial) : newConfiguration(EntityKind.AGENT));
  const [step, setStep] = useState(Step.Harness);
  const [source, setSource] = useState<Source>();
  const [providerPage, setProviderPage] = useState("");
  const [accountPage, setAccountPage] = useState("");
  const [modelPage, setModelPage] = useState("");
  const [knownAccounts, setKnownAccounts] = useState<Record<string, Resource | undefined>>({});
  const [accountRefresh, setAccountRefresh] = useState(0);
  const [model, setModel] = useState<Resource>();
  const [savedModels, setSavedModels] = useState<{ key: string; rows: Record<string, Resource> }>({ key: "", rows: {} });
  const [input, setInput] = useState("");
  const [popup, setPopup] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const [nativePending, setNativePending] = useState(false);
  const [problem, setProblem] = useState("");
  const [focusField, setFocusField] = useState("");
  const [focusAttempt, setFocusAttempt] = useState(0);
  const initialized = useRef(!initial);
  const heading = useRef<HTMLHeadingElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const suggestionList = useRef<HTMLUListElement>(null);
  const listID = useId();
  const notifications = useNotifications();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.AGENT_WORKER_WIZARD_V1) === true;
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.AGENT, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const originalModel = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: text(document(initial).model_id) }, { enabled: active && Boolean(initial && document(initial).model_id) });
  const providers = useQuery(ProviderQuery.listProviderInventory, { enabledOnly: true, pageSize: 50, pageToken: providerPage }, { enabled: active && supported });
  const selectedProvider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: source?.kind === SourceKind.Api ? source.id : "" }, { enabled: active && supported && source?.kind === SourceKind.Api });
  const accountRows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: accountPage }, providerId: source?.kind === SourceKind.Api ? source.id : "", accountType: source?.kind === SourceKind.Api ? AccountTypeFilter.API : AccountTypeFilter.SUBSCRIPTION, subscriptionService: wireService(source), apiProtocol: source?.kind === SourceKind.Api && providers.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) ? harnessAPIProtocol(text(data.harness)) : ApiProtocol.UNSPECIFIED }, { enabled: active && supported && Boolean(source) });
  const query = useDeferredValue(input);
  const models = useQuery(ProviderQuery.searchModels, { query, providerId: source?.kind === SourceKind.Api ? source.id : "", subscriptionService: wireService(source), includeHidden: true, enabledProvidersOnly: true, pageSize: 50, pageToken: modelPage }, { enabled: active && supported && Boolean(source) && step === Step.Model, placeholderData: previous => previous });
  const knownSupported = status.data?.capabilities.includes(SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1) === true;
  const known = useQuery(ProviderQuery.listKnownSubscriptionModels, { subscriptionService: wireService(source) }, { enabled: active && supported && knownSupported && source?.kind === SourceKind.Subscription && step === Step.Model });
  const knownValid = knownSupported && source?.kind === SourceKind.Subscription && known.data?.subscriptionService === wireService(source) && known.data.models.length <= 200 && /^sha256:[a-f0-9]{64}$/.test(known.data.catalogVersion) && validCatalogDate(known.data.updatedAt) && [KnownSubscriptionModelCatalogSource.BUNDLED, KnownSubscriptionModelCatalogSource.CACHE, KnownSubscriptionModelCatalogSource.ONLINE].includes(known.data.source) && known.data.models.every(row => row.nativeId && row.displayName) && new Set(known.data.models.map(row => row.nativeId)).size === known.data.models.length;
  const currentModel = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: model?.id ?? "" }, { enabled: active && supported && Boolean(model), refetchInterval: active && model ? 5000 : false });
  const accountPageValid = accountRows.data?.resources.every(row => sameSource(row, source));
  const accountChoices = accountPageValid ? accountRows.data!.resources.filter(row => showAccountChoice(row) && accountFormatMatches(row, selectedProvider.data?.resource, text(data.harness))) : [];
  const modelPageValid = models.data?.models.every(row => supportsResourceSchema(row) && sourceKey(modelSource(row)) === sourceKey(source));
  const savedModelKey = `${sourceKey(source)}\u0000${query}`;
  const cachedSavedModels = savedModels.key === savedModelKey ? savedModels.rows : {};
  const links = items(data.accounts).map(object);
  const ids = links.map(link => text(link.id));
  const remember = useCallback((id: string, row?: Resource) => setKnownAccounts(previous => previous[id] === row ? previous : { ...previous, [id]: row }), []);
  useEffect(() => { if (accountPageValid) for (const row of accountRows.data?.resources ?? []) remember(row.id, row); }, [accountRows.data, accountPageValid, remember]);
  useEffect(() => {
    if (initialized.current || !originalModel.data?.resource) return;
    initialized.current = true;
    const row = originalModel.data.resource;
    setSource(modelSource(row));
    if (modelSource(row)) { setModel(row); setInput(text(document(row).native_id)); }
  }, [originalModel.data]);
  useLayoutEffect(() => {
    if (!active) return;
    if (focusField) {
      const control = focusField === "name" ? form.current?.querySelector<HTMLElement>(".agent-core input") : form.current?.querySelector<HTMLElement>(`[data-wizard-field="${focusField}"]`);
      const disclosure = control?.closest<HTMLDetailsElement>("details");
      if (disclosure) disclosure.open = true;
      control?.focus();
    } else heading.current?.focus();
  }, [step, focusField, focusAttempt, active]);
  const mutation = useRetainedMutation(`agent-worker-wizard:${initial?.id ?? "new"}`, ConfigurationQuery.saveAgentWorker, (result, request) => {
    notifications.notify({ kind: ToastKind.Success, message: copy("agent-worker-wizard.agentWorkerSaved"), id: request.mutation?.requestId }); saved();
  }, (result, request) => result.requestId === request.mutation?.requestId && result.resource?.kind === EntityKind.AGENT && supportsResourceSchema(result.resource) && (!initial || result.resource.id === initial.id));
  const discovery = useRetainedMutation(`agent-worker-wizard:discovery:${sourceKey(source)}`, ProviderQuery.discoverModels, result => {
    if (result.account) remember(result.account.id, result.account);
    setModelPage(""); void models.refetch(); void accountRows.refetch();
  }, (result, request) => result.requestId === request.mutation?.requestId && result.account?.id === request.mutation?.id);
  const blocked = nativePending || mutation.busy || mutation.uncertain || discovery.busy || discovery.uncertain;
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const change = (value: Document) => { if (encode(value).byteLength > 1 << 20) { setProblem(copy("agent-worker-wizard.configurationTooLarge")); return false; } setData(value); setProblem(""); return true; };
  const clearModel = () => { setModel(undefined); setInput(""); setModelPage(""); setSavedModels({ key: "", rows: {} }); setPopup(false); setHighlight(-1); };
  const chooseHarness = (harness: Harness) => {
    if (blocked || !active || !supported || step !== Step.Harness) return false;
    if (harness === data.harness) return true;
    if (!change({ ...data, harness, accounts: [], model_id: "" })) return false;
    initialized.current = true; setSource(undefined); clearModel();
    return true;
  };
  const confirmHarness = (harness: Harness) => {
    if (!chooseHarness(harness)) return;
    setStep(Step.Accounts); setFocusField(""); setProblem("");
  };
  const chooseSource = (value: string) => {
    initialized.current = true;
    setSource(fromKey(value)); setAccountPage(""); clearModel();
    change({ ...data, accounts: [], model_id: "" });
    setProblem(copy("agent-worker-wizard.selectAccountsAndModel"));
  };
  const sourceLabel = source?.kind === SourceKind.Subscription ? subscriptionServiceNames[source.id as SubscriptionServiceId] : selectedProvider.data?.resource ? resourceName(selectedProvider.data.resource) : source?.id ?? "";
  const selectedRows = ids.flatMap(id => knownAccounts[id] ? [knownAccounts[id]!] : []);
  const fail = (target: Step, message: string, field = "") => { setStep(target); setProblem(message); setFocusField(field); setFocusAttempt(value => value + 1); };
  const validate = (through: Step): boolean => {
    if (!Object.values(Harness).includes(data.harness as Harness)) { fail(Step.Harness, copy("agent-worker-wizard.chooseSupportedHarness"), "harness"); return false; }
    if (through >= Step.Accounts) {
      if (!source || source.kind === SourceKind.Subscription && subscriptionServiceHarnesses[source.id as SubscriptionServiceId] !== data.harness) { fail(Step.Accounts, copy("agent-worker-wizard.chooseAccountSourceForHarness"), "source"); return false; }
      if (!ids.length || ids.length > 1000 || ids.some(id => !knownAccounts[id] || !sameSource(knownAccounts[id]!, source))) { fail(Step.Accounts, copy("agent-worker-wizard.selectCurrentAccount"), "source"); return false; }
      if (selectedRows.some(row => !accountFormatMatches(row, selectedProvider.data?.resource, text(data.harness)))) { fail(Step.Accounts, copy("agent-worker-wizard.accountApiFormatMismatch"), "source"); return false; }
      if (data.routing === Routing.Fixed && ids.length !== 1 || links.some(link => !Number.isInteger(link.weight) || Number(link.weight) < 1 || Number(link.weight) > 1000)) { fail(Step.Accounts, copy("agent-worker-wizard.fixedRoutingWeights"), "routing"); return false; }
    }
    if (through >= Step.Model && (!input.trim() || new TextEncoder().encode(input.trim()).byteLength > 256 || model && sourceKey(modelSource(model)) !== sourceKey(source))) { fail(Step.Model, copy("agent-worker-wizard.selectModelFromSource"), "model"); return false; }
    if (through >= Step.Model && model && (currentModel.error || through === Step.Configure && !currentModel.data?.resource || currentModel.data?.resource && (currentModel.data.resource.revision !== model.revision || sourceKey(modelSource(currentModel.data.resource)) !== sourceKey(source)))) { fail(Step.Model, copy("agent-worker-wizard.selectedModelUnavailable"), "model"); return false; }
    if (through >= Step.Configure && (!text(data.name).trim() || new TextEncoder().encode(text(data.name)).byteLength > 256)) { fail(Step.Configure, copy("agent-worker-wizard.workerNameByteLimit"), "name"); return false; }
    return true;
  };
  useEffect(() => {
    if (!active || !mutation.error || mutation.uncertain) return;
    if (model) void currentModel.refetch();
    if (initial) void current.refetch();
    setAccountRefresh(value => value + 1);
  }, [active, mutation.error, mutation.uncertain, model?.id, initial?.id, currentModel.refetch, current.refetch]);
  useEffect(() => {
    if (!active || !mutation.error || mutation.uncertain || step !== Step.Configure) return;
    if (ids.some(id => !knownAccounts[id] || !sameSource(knownAccounts[id]!, source))) fail(Step.Accounts, copy("agent-worker-wizard.selectedAccountChanged"), "source");
    else if (model && (currentModel.error || currentModel.data?.resource && currentModel.data.resource.revision !== model.revision)) fail(Step.Model, copy("agent-worker-wizard.selectedModelUnavailable"), "model");
  }, [active, mutation.error, mutation.uncertain, step, data.accounts, knownAccounts, source, model, currentModel.data, currentModel.error]);
  const pick = (row?: ModelSuggestion) => { setModelPage(""); setSavedModels({ key: "", rows: {} }); if (row?.resource && row.resource.id === model?.id) void currentModel.refetch(); setModel(row?.resource); if (row) setInput(row.nativeId); setPopup(false); setHighlight(-1); setProblem(""); };
  const pageSuggestions: ModelSuggestion[] = modelPageValid ? models.data!.models.map(resource => ({ nativeId: text(document(resource).native_id), name: resourceName(resource), kind: SuggestionKind.Saved, resource })) : [];
  useEffect(() => {
    if (!modelPageValid || !models.data || models.isFetching || models.error) return;
    setSavedModels(previous => {
      const current = previous.key === savedModelKey ? previous.rows : {};
      const next = { ...current };
      let changed = false;
      for (const row of models.data.models) {
        const nativeId = text(document(row).native_id);
        const prior = current[nativeId];
        if (!nativeId || prior && prior.id === row.id && prior.revision === row.revision) continue;
        next[nativeId] = row;
        changed = true;
      }
      return changed ? { key: savedModelKey, rows: next } : previous;
    });
  }, [modelPageValid, models.data, models.error, models.isFetching, savedModelKey]);
  const savedIDs = new Set([...Object.keys(cachedSavedModels), ...pageSuggestions.map(row => row.nativeId)]);
  const suggestions: ModelSuggestion[] = [...pageSuggestions];
  const search = query.trim().toLocaleLowerCase();
  // Known candidates are advisory only. Wait for the terminal saved-model page so an
  // unvisited saved duplicate cannot bypass its exact revision check.
  const savedCatalogComplete = Boolean(models.data && !models.isFetching && !models.data.nextPageToken);
  if (knownValid && savedCatalogComplete) for (const row of known.data!.models) {
    if (!savedIDs.has(row.nativeId) && (!search || `${row.displayName} ${row.nativeId}`.toLocaleLowerCase().includes(search))) suggestions.push({ nativeId: row.nativeId, name: row.displayName, kind: SuggestionKind.Known });
  }
  const catalogDate = knownValid ? new Intl.DateTimeFormat("en-US", { year: "numeric", month: "short", day: "numeric", timeZone: "UTC" }).format(new Date(`${known.data!.updatedAt}T00:00:00Z`)) : "";
  useLayoutEffect(() => {
    if (active && popup && highlight >= 0) (suggestionList.current?.children.item(highlight) as HTMLElement | null)?.scrollIntoView?.({ block: "nearest" });
  }, [active, popup, highlight, models.data, known.data]);
  const refreshAccount = selectedRows.find(row => Boolean(document(row).connection) && document(row).enabled !== false && !document(row).removal);
  const providerEntries = providers.data?.entries.filter(entry => entry.enabled && entry.providerId) ?? [];
  const advance = () => { if (validate(step)) { setStep(step + 1); setFocusField(""); setProblem(""); } };
  return <form ref={form} className="agent-configuration worker-wizard" noValidate onSubmit={event => {
    event.preventDefault(); if (blocked || !active || !supported || step === Step.Harness) return;
    if (step !== Step.Configure) { advance(); return; }
    if (!validate(Step.Configure) || stale || Boolean(initial && (current.error || !current.data?.resource))) return;
    if (!form.current?.checkValidity()) { const invalid = form.current?.querySelector<HTMLInputElement>("input:invalid, select:invalid, textarea:invalid"); if (invalid) { const details = invalid.closest("details"); if (details) details.open = true; invalid.focus(); } return; }
    const next: Document = { ...data, model_id: model?.id ?? "" };
    delete next.reconfiguration_required;
    void mutation.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(next), model: { selection: model ? { case: "modelId", value: model.id } : { case: "nativeId", value: input.trim() }, expectedModelRevision: model?.revision ?? 0n } });
  }} onInvalidCapture={revealAgentInvalidControl}>
    <h2>{initial ? copy("agent-worker-wizard.editAgentWorker") : copy("agent-worker-wizard.newAgentWorker")}</h2>
    <ol className="worker-steps" aria-label={copy("agent-worker-wizard.workerConfigurationSteps")}>{steps.map(value => <li key={value} aria-current={step === value ? "step" : undefined} data-completed={value < step}><span>{value}</span><span>{stepName(value)}</span></li>)}</ol>
    <h3 ref={heading} tabIndex={-1}>{stepName(step)}</h3>
    <Problem error={status.error} />
    {status.isLoading ? <p role="status">{copy("agent-worker-wizard.checkingServerSupport")}</p> : status.data && !supported ? <p role="alert">{copy("agent-worker-wizard.updateServerWizard")}</p> : null}
    {initial && data.reconfiguration_required === true ? <p role="status">{copy("agent-worker-wizard.reconfigurationRequired")}</p> : null}
    <fieldset disabled={blocked || !supported}>
      <section hidden={step !== Step.Harness}>
        <WorkerHarnessPicker value={data.harness} disabled={blocked || !active || !supported} change={chooseHarness} confirm={confirmHarness} />
      </section>
      <section hidden={step !== Step.Accounts} aria-label={copy("agent-worker-wizard.chooseAccounts")}>
        <p>{harnessNames[data.harness as Harness]} <button type="button" onClick={() => { setStep(Step.Harness); setFocusField(""); }}>{copy("agent-worker-wizard.changeHarness")}</button></p>
        <h4>{copy("agent-worker-wizard.chooseAccounts")}</h4><p>{copy("agent-worker-wizard.selectAtLeastOneAccount")}</p>
        <label>{copy("agent-worker-wizard.accountSource")}<select data-wizard-field="source" value={sourceKey(source)} onChange={event => chooseSource(event.target.value)}><option value="">{copy("agent-worker-wizard.selectSource")}</option>{Object.values(SubscriptionServiceId).filter(value => subscriptionServiceHarnesses[value] === data.harness).map(value => <option key={value} value={`${SourceKind.Subscription}:${value}`}>{subscriptionServiceNames[value]} {copy("agent-worker-wizard.subscription")}</option>)}{source?.kind === SourceKind.Api && !providerEntries.some(entry => entry.providerId === source.id) ? <option value={sourceKey(source)}>{sourceLabel}{document(selectedProvider.data?.resource).enabled === false ? copy("agent-worker-wizard.offSuffix") : copy("agent-worker-wizard.retainedSourceSuffix")}</option> : null}{providerEntries.map(entry => <option key={entry.providerId} value={`${SourceKind.Api}:${entry.providerId}`}>{entry.displayName || resourceName(entry.provider)}</option>)}</select></label>
        <Problem error={providers.error || selectedProvider.error} />
        {providers.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingApiSources")}</p> : null}
        <nav aria-label={copy("agent-worker-wizard.apiSourcePages")}><button type="button" disabled={!providerPage || providers.isFetching} onClick={() => setProviderPage("")}>{copy("agent-worker-wizard.firstSourcePage")}</button><button type="button" disabled={!providers.data?.nextPageToken || providers.isFetching} onClick={() => setProviderPage(providers.data!.nextPageToken)}>{copy("agent-worker-wizard.nextSourcePage")}</button></nav>
        <Problem error={accountRows.error} />
        {source && accountRows.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingAccounts")}</p> : null}
        {accountRows.error && accountRows.data ? <p role="status">{copy("agent-worker-wizard.refreshFailedStaleAccounts")}</p> : null}
        {accountRows.data && !accountPageValid ? <p role="alert">{copy("agent-worker-wizard.invalidAccountPage")}</p> : null}
        <div className="worker-account-list">{accountChoices.map(row => { const value = document(row); return <label className="worker-account-row" key={row.id}><input type="checkbox" checked={ids.includes(row.id)} onChange={event => change({ ...data, accounts: event.target.checked ? [...links, { id: row.id, weight: 1 }] : links.filter(link => link.id !== row.id) })} /><span><strong>{resourceName(row)}</strong><small>{copy("agent-worker-wizard.connectionHealth", { v0: value.connection ? copy("agent-worker-wizard.connected") : copy("agent-worker-wizard.disconnected"), v1: text(value.health) || copy("agent-worker-wizard.unavailable") })}</small><small>{copy("agent-worker-wizard.executionEligibility", { v0: value.enabled === false ? copy("agent-worker-wizard.disabled") : value.removal ? copy("agent-worker-wizard.removalPending") : copy("agent-worker-wizard.checkedAtExecution") })}</small></span></label>; })}
          {source && accountPageValid && accountRows.data!.resources.length > 0 && !accountRows.error && !accountRows.isFetching && accountChoices.length === 0 ? <div className="worker-account-empty"><p>{copy("agent-worker-wizard.noAccountsToSelectOnThisPage")}</p><p>{copy("agent-worker-wizard.connectAnAccountThenRefresh")}</p></div> : null}
        </div>
        {source && accountRows.data?.resources.length === 0 && !accountRows.error ? <p>{accountPage ? copy("agent-worker-wizard.noAccountsOnPage") : copy("agent-worker-wizard.noAccountsForSource")}</p> : null}
        {source ? <nav aria-label={copy("agent-worker-wizard.accountPages")}><button type="button" disabled={!accountPage || accountRows.isFetching} onClick={() => setAccountPage("")}>{copy("agent-worker-wizard.firstAccountPage")}</button><button type="button" disabled={!accountRows.data?.nextPageToken || accountRows.isFetching} onClick={() => setAccountPage(accountRows.data!.nextPageToken)}>{copy("agent-worker-wizard.nextAccountPage")}</button><button type="button" disabled={accountRows.isFetching} onClick={() => void accountRows.refetch()}>{copy("agent-worker-wizard.refreshAccounts")}</button></nav> : null}
        <p>{copy("agent-worker-wizard.accountsSelected", { v0: ids.length })}</p><p>{copy("agent-worker-wizard.sameSourceRequired")}</p>
        {ids.map(id => <SelectedAccount key={id} id={id} active={active && supported} refresh={accountRefresh} read={remember} />)}
        <details className="worker-routing"><summary>{copy("agent-worker-wizard.routingOptions")} <small>{text(data.routing) || copy("agent-worker-wizard.serverDefault")} · {links.every(link => link.weight === 1) ? copy("agent-worker-wizard.equalWeights") : copy("agent-worker-wizard.customWeights")}</small></summary>
          <label>{copy("agent-worker-wizard.accountRouting")}<select data-wizard-field="routing" value={text(data.routing)} onChange={event => { const next = { ...data }; if (event.target.value) next.routing = event.target.value; else delete next.routing; change(next); }}><option value="">{copy("agent-worker-wizard.serverDefault")}</option>{Object.values(Routing).map(value => <option key={value} value={value} disabled={value === Routing.Fixed && ids.length !== 1}>{value}</option>)}</select></label>
          <ol>{links.map((link, index) => <li key={text(link.id)}><strong>{knownAccounts[text(link.id)] ? resourceName(knownAccounts[text(link.id)]) : text(link.id)}</strong><label>{copy("agent-worker-wizard.weightForAccount", { v0: index + 1 })}<input type="number" min={1} max={1000} value={Number(link.weight)} onChange={event => change({ ...data, accounts: links.map((value, i) => i === index ? { ...value, weight: Number(event.target.value) } : value) })} /></label><div className="actions"><button type="button" disabled={index === 0} aria-label={copy("agent-worker-wizard.moveAccountUp", { v0: index + 1 })} onClick={() => { const next = [...links]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; change({ ...data, accounts: next }); }}>{copy("agent-worker-wizard.up")}</button><button type="button" aria-label={copy("agent-worker-wizard.removeAccount", { v0: index + 1 })} onClick={() => change({ ...data, accounts: links.filter((_, i) => i !== index) })}>{copy("agent-worker-wizard.remove")}</button></div></li>)}</ol>
        </details>
      </section>
      <section hidden={step !== Step.Model}>
        <p>{harnessNames[data.harness as Harness]} · {sourceLabel} · {copy("agent-worker-wizard.accountsSelected", { v0: ids.length })}</p>
        <h4>{copy("agent-worker-wizard.chooseModel")}</h4><p>{copy("agent-worker-wizard.searchSavedCatalog")}</p>
        <div className="worker-model-combobox"><label htmlFor={`${listID}-input`}>{copy("agent-worker-wizard.model")}</label><input id={`${listID}-input`} data-wizard-field="model" role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-controls={listID} aria-activedescendant={popup && highlight >= 0 && highlight < suggestions.length + (input.trim() ? 1 : 0) ? `${listID}-${highlight}` : undefined} value={input} maxLength={256} autoComplete="off" onFocus={() => setPopup(true)} onBlur={() => setPopup(false)} onChange={event => { setInput(event.target.value); setModel(undefined); setModelPage(""); setSavedModels({ key: "", rows: {} }); setPopup(true); setHighlight(-1); setProblem(""); }} onKeyDown={event => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); setPopup(true); const count = suggestions.length + (input.trim() ? 1 : 0); setHighlight(value => count === 0 ? -1 : event.key === "ArrowDown" ? Math.min(value + 1, count - 1) : value <= 0 ? count - 1 : value - 1); }
          else if (event.key === "Escape" && popup) { event.preventDefault(); event.stopPropagation(); setPopup(false); }
          else if (event.key === "Enter" && popup) { event.preventDefault(); pick(suggestions[highlight]); }
        }} />
          {popup ? <ul ref={suggestionList} id={listID} role="listbox" aria-label={copy("agent-worker-wizard.modelSuggestions")}>{suggestions.map((row, index) => <li id={`${listID}-${index}`} key={`${row.kind}:${row.resource?.id ?? row.nativeId}`} role="option" aria-selected={highlight >= 0 ? highlight === index : Boolean(row.resource && model?.id === row.resource.id)} onMouseDown={event => event.preventDefault()} onClick={() => pick(row)}><div className="worker-model-heading"><strong>{row.name}</strong><span className="worker-model-origin">{row.kind === SuggestionKind.Known ? copy("agent-worker-wizard.knownModelOrigin") : copy("agent-worker-wizard.savedModelOrigin")}</span></div><small>{row.nativeId}{row.resource && document(row.resource).hidden === true ? copy("agent-worker-wizard.hiddenSuffix") : ""}</small></li>)}{input.trim() ? <li id={`${listID}-${suggestions.length}`} role="option" aria-selected={highlight === suggestions.length} onMouseDown={event => event.preventDefault()} onClick={() => pick()}>{copy("agent-worker-wizard.useExactModelId", { v0: input.trim() })}</li> : null}</ul> : null}
        </div>
        {models.isLoading || known.isLoading ? <p role="status">{copy("agent-worker-wizard.loadingModelCatalog")}</p> : models.isFetching || known.isFetching ? <p role="status">{copy("agent-worker-wizard.refreshingSavedCatalog")}</p> : null}<Problem error={models.error || known.error} />
        {models.data && !modelPageValid ? <p role="alert">{copy("agent-worker-wizard.invalidModelPage")}</p> : null}
        {models.error || known.error ? <p role="status">{copy("agent-worker-wizard.catalogLookupFailed", { v0: models.data || known.data ? copy("agent-worker-wizard.staleResults") : "" })}</p> : !models.isLoading && !known.isLoading && suggestions.length === 0 ? <p>{modelPage ? copy("agent-worker-wizard.noModelsOnPage") : source?.kind === SourceKind.Subscription ? copy("agent-worker-wizard.noMatchingModels") : copy("agent-worker-wizard.noSavedModels")}</p> : null}
        <nav aria-label={copy("agent-worker-wizard.modelCatalogPages")}>{modelPage ? <button type="button" disabled={models.isFetching} onClick={() => { setSavedModels({ key: "", rows: {} }); setModelPage(""); }}>{copy("agent-worker-wizard.firstModelPage")}</button> : null}{models.data?.nextPageToken ? <button type="button" disabled={models.isFetching} onClick={() => setModelPage(models.data!.nextPageToken)}>{copy("agent-worker-wizard.nextModelPage")}</button> : null}<button type="button" disabled={models.isFetching || known.isFetching} onClick={() => { setSavedModels({ key: "", rows: {} }); setModelPage(""); void models.refetch(); if (source?.kind === SourceKind.Subscription && knownSupported) void known.refetch(); if (model) void currentModel.refetch(); }}>{copy("agent-worker-wizard.reloadSavedCatalog")}</button></nav>
        {source?.kind === SourceKind.Subscription ? <>{knownValid ? <p>{copy("agent-worker-wizard.knownModelsCatalogUpdated", { v0: catalogDate, v1: known.data!.source === KnownSubscriptionModelCatalogSource.CACHE ? copy("agent-worker-wizard.cachedCatalog") : known.data!.source === KnownSubscriptionModelCatalogSource.BUNDLED ? copy("agent-worker-wizard.builtInCatalog") : copy("agent-worker-wizard.onlineCatalog") })}</p> : known.data ? <p role="alert">{copy("agent-worker-wizard.knownCatalogUnavailable")}</p> : null}{status.data && !knownSupported ? <p>{copy("agent-worker-wizard.updateServerForKnownModels")}</p> : null}</> : <><button type="button" disabled={blocked || !refreshAccount || document(selectedProvider.data?.resource).discovery !== true || document(selectedProvider.data?.resource).enabled === false} onClick={() => { if (refreshAccount) void discovery.send({ mutation: { requestId: newRequestId(), id: refreshAccount.id, expectedRevision: refreshAccount.revision } }); }}>{copy("agent-worker-wizard.refreshModelsEndpoint")}</button><p>{copy("agent-worker-wizard.discoveryUsesFirstAccount")}</p></>}
        <Problem error={discovery.error} />{discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>{copy("agent-worker-wizard.retryModelRefresh")}</button> : null}
        {data.harness === Harness.Codex && source?.kind === SourceKind.Api ? <NativeModelSettings active={active && step === Step.Model} selectedAccounts={selectedRows} pendingOperation={setNativePending} createModel={value => { setInput(text(value.native_id)); setModel(undefined); setModelPage(""); setPopup(false); setHighlight(-1); setProblem(""); }} /> : null}
        <p>{copy(source?.kind === SourceKind.Subscription ? "agent-worker-wizard.subscriptionModelAvailability" : "agent-worker-wizard.modelReadiness")}</p>
        <Problem error={currentModel.error} />
      </section>
      <fieldset hidden={step !== Step.Configure} disabled={step !== Step.Configure}>
        <ConfigurationFields kind={EntityKind.AGENT} data={data} change={change} active={active && step === Step.Configure} existing={Boolean(initial)} workerWizard />
        <section className="worker-summary" aria-label={copy("agent-worker-wizard.workerConfigurationSummary")}><h4>{copy("agent-worker-wizard.reviewConfiguration")}</h4><p>{harnessNames[data.harness as Harness]} · {sourceLabel}</p><p>{copy("agent-worker-wizard.modelSummary", { v0: input || copy("agent-worker-wizard.noneSelected") })}</p><ol>{ids.map(id => <li key={id}>{knownAccounts[id] ? resourceName(knownAccounts[id]) : id}</li>)}</ol><p>{copy("agent-worker-wizard.routingSummary", { v0: text(data.routing) || copy("agent-worker-wizard.serverDefault") })}</p><p>{copy("agent-worker-wizard.savedCompatibility")}</p></section>
      </fieldset>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error || current.error || originalModel.error} />
    {stale ? <p role="alert">{copy("agent-worker-wizard.workerChangedElsewhere")}</p> : null}
    {model && currentModel.data?.resource && currentModel.data.resource.revision !== model.revision ? <p role="status">{copy("agent-worker-wizard.selectedModelChanged")}</p> : null}
    {model && currentModel.isLoading ? <p role="status">{copy("agent-worker-wizard.checkingModelRevision")}</p> : null}
    <div className="worker-footer"><button type="button" disabled={blocked} onClick={cancel}>{copy("agent-worker-wizard.cancel")}</button>{step > Step.Harness ? <div><button type="button" disabled={blocked} onClick={() => { setStep(step - 1); setFocusField(""); setProblem(""); }}>{copy("agent-worker-wizard.back")}</button><button type="submit" className="primary" disabled={blocked || !active || !supported || step === Step.Configure && (stale || currentModel.isLoading || Boolean(initial && (current.error || !current.data?.resource)))}>{mutation.busy ? copy("agent-worker-wizard.saving") : step === Step.Configure ? copy("agent-worker-wizard.saveAgentWorker") : copy("agent-worker-wizard.next")}</button></div> : null}</div>
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("agent-worker-wizard.retryWorkerSave")}</button> : null}
  </form>;
}

export function AgentWorkerWizard(props: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: props.active });
  if (status.data?.capabilities.includes(SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1)) return <AgentWorkerSourceWizard {...props} />;
  if (props.initial?.schemaVersion === 3) return <><p role="alert">{copy("agent-worker-wizard.updateSourceServer")}</p><button onClick={props.cancel}>{copy("agent-worker-wizard.cancel")}</button></>;
  return <LegacyAgentWorkerWizard {...props} />;
}
