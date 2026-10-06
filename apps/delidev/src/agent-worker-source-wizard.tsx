// SPDX-License-Identifier: Apache-2.0
import { useCallback, useDeferredValue, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountTypeFilter, ConfigurationQuery, EntityKind, ProviderQuery, ResourceQuery, SubscriptionServiceId, newRequestId, subscriptionServiceHarnesses, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationFields, Harness, Routing, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { SourceKind, SelectedAccount, fromKey, modelSource, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { useRetainedMutation } from "./mutation";
import { NativeModelSettings } from "./native-model-settings";
import { Problem } from "./ui";
import { revealAgentInvalidControl } from "./agent-configuration";
import { ToastKind, useNotifications } from "./toast-notifications";
import { WorkerHarnessPicker } from "./worker-harness-picker";

// Groups remain mounted through step changes and reordering. Their stable keys
// own pagination/discovery receipts independently of their priority position.
enum Step { Harness = 1, Accounts, Model, Configure }
const names = ["", "Harness", "Accounts", "Model", "Configure"];
const harnessNames = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };
interface Draft { original: Document; key: string; source?: Source; accounts: Document[]; routing?: string; modelID: string; model?: Resource; input: string }
interface Evidence { accountError: string; modelError: string; pending: boolean; label: string; accounts: string[] }
function draft(value: Document = {}): Draft {
  return { original: value, key: newRequestId(), accounts: items(value.accounts).map(object), routing: text(value.routing) || undefined, modelID: text(value.model_id), input: "" };
}
function accountStatus(data: Document) {
  const connection = data.connection ? "Connected" : "Disconnected";
  const health = text(data.health);
  if (data.type !== "subscription") return `${connection} · Health: ${health || "Unverified"}`;
  const quota = data.confirmed_exhausted === true ? "Quota exhausted" : object(data.subscription).quota_state === "failed" ? "Quota observation failed" : "Quota unknown";
  return `${connection}${health && !["ready", "unverified", "disconnected"].includes(health) ? ` · Health: ${health}` : ""} · ${quota}`;
}

function SourceGroup({ value, index, count, step, harness, active, locked, duplicateSources, update, report, move, remove }: {
  value: Draft; index: number; count: number; step: Step; harness: Harness; active: boolean; locked: boolean; duplicateSources: string[];
  update: (key: string, patch: Partial<Draft>) => void; report: (key: string, value: Evidence) => void; move: (key: string, offset: number) => void; remove: (key: string) => void;
}) {
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
  const initialModel = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: value.modelID }, { enabled: active && Boolean(value.modelID), refetchInterval: active && value.modelID ? 5000 : false });
  const providers = useQuery(ProviderQuery.listProviderInventory, { enabledOnly: true, pageSize: 50, pageToken: providerPage }, { enabled: active && step === Step.Accounts });
  const provider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: source?.kind === SourceKind.Api ? source.id : "" }, { enabled: active && source?.kind === SourceKind.Api });
  const rows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: accountPage }, providerId: source?.kind === SourceKind.Api ? source.id : "", accountType: source?.kind === SourceKind.Api ? AccountTypeFilter.API : AccountTypeFilter.SUBSCRIPTION, subscriptionService: wireService(source) }, { enabled: active && Boolean(source) && step === Step.Accounts });
  const models = useQuery(ProviderQuery.searchModels, { query, providerId: source?.kind === SourceKind.Api ? source.id : "", subscriptionService: wireService(source), includeHidden: true, enabledProvidersOnly: true, pageSize: 50, pageToken: modelPage }, { enabled: active && Boolean(source) && step === Step.Model });
  const remember = useCallback((id: string, row?: Resource) => setKnown(previous => previous[id] === row ? previous : { ...previous, [id]: row }), []);
  const pageValid = rows.data?.resources.every(row => sameSource(row, source));
  useEffect(() => { if (pageValid) for (const row of rows.data?.resources ?? []) remember(row.id, row); }, [pageValid, rows.data, remember]);
  useEffect(() => {
    const row = initialModel.data?.resource;
    if (!value.source && row && modelSource(row)) update(value.key, { source: modelSource(row), model: row, input: text(document(row).native_id) });
  }, [initialModel.data, value.source, value.key, update]);
  const label = source?.kind === SourceKind.Subscription ? `${subscriptionServiceNames[source.id as SubscriptionServiceId]} subscription` : provider.data?.resource ? resourceName(provider.data.resource) : source?.id || "Choose account source";
  const selectedRows = ids.flatMap(id => known[id] ? [known[id]!] : []);
  const refreshAccount = selectedRows.find(row => document(row).connection && document(row).enabled !== false && !document(row).removal);
  const discovery = useRetainedMutation(`agent-worker-routes:discovery:${value.key}:${sourceID}`, ProviderQuery.discoverModels, result => {
    if (result.account) remember(result.account.id, result.account);
    setModelPage(""); void models.refetch(); void rows.refetch();
  }, (result, request) => result.requestId === request.mutation?.requestId && result.account?.id === request.mutation?.id);
  const pending = nativePending || discovery.busy || discovery.uncertain || step >= Step.Model && Boolean(value.modelID) && initialModel.isLoading;
  const accountError = !source || source.kind === SourceKind.Subscription && subscriptionServiceHarnesses[source.id as SubscriptionServiceId] !== harness ? "Choose an account source for this harness."
    : duplicateSources.includes(sourceID) ? "Each account source can appear once. Combine its accounts in one source."
    : !ids.length || ids.some(id => !known[id] || !sameSource(known[id]!, source)) ? "Select at least one current account for this source. Refresh unavailable selections."
    : value.routing === Routing.Fixed && ids.length !== 1 || value.accounts.some(account => !Number.isInteger(account.weight) || Number(account.weight) < 1 || Number(account.weight) > 1000) ? "Fixed routing requires one account. Weights must be integers from 1 to 1,000." : "";
  const modelError = !value.input.trim() || new TextEncoder().encode(value.input.trim()).byteLength > 256 ? "Select a catalog model or enter its exact model ID."
    : value.model && (sourceKey(modelSource(value.model)) !== sourceID || !initialModel.data?.resource || initialModel.error || initialModel.data.resource.revision !== value.model.revision || sourceKey(modelSource(initialModel.data.resource)) !== sourceID) ? "The selected model is unavailable or changed. Explicitly reselect its current revision or enter an exact model ID." : "";
  const accountNames = selectedRows.map(resourceName).join("\u0000");
  useEffect(() => { report(value.key, { accountError, modelError, pending, label, accounts: accountNames.split("\u0000").filter(Boolean) }); }, [value.key, accountError, modelError, pending, label, accountNames, report]);
  useEffect(() => { if (step !== Step.Model) { setPopup(false); setHighlight(-1); } }, [step]);
  const chooseSource = (key: string) => { update(value.key, { source: fromKey(key), accounts: [], modelID: "", model: undefined, input: "" }); setAccountPage(""); setModelPage(""); setChoosing(true); };
  const suggestions = models.data?.models.filter(row => supportsResourceSchema(row) && sourceKey(modelSource(row)) === sourceID) ?? [];
  const catalogValid = suggestions.length === models.data?.models.length;
  const pick = (row?: Resource) => { update(value.key, { model: row, modelID: row?.id ?? "", input: row ? text(document(row).native_id) : value.input }); setPopup(false); setHighlight(-1); setModelPage(""); if (row) void initialModel.refetch(); };
  const entries = providers.data?.entries.filter(entry => entry.enabled && entry.providerId) ?? [];
  const changeRouting = (routing: string) => update(value.key, { routing: routing || undefined });
  return <section ref={group} className="worker-source-group" data-source-group={value.key} hidden={step !== Step.Accounts && step !== Step.Model} aria-label={`Account source ${index + 1}`}>
    <fieldset disabled={locked}><header><h4 tabIndex={-1} data-wizard-field="source-heading">{index + 1} · {label}</h4>{step === Step.Accounts ? <div className="actions"><button type="button" disabled={index === 0} aria-label={`Move source ${index + 1} up`} onClick={() => move(value.key, -1)}>Move up</button><button type="button" disabled={index === count - 1} aria-label={`Move source ${index + 1} down`} onClick={() => move(value.key, 1)}>Move down</button><button type="button" disabled={count === 1} aria-label={`Remove source ${index + 1}`} onClick={() => remove(value.key)}>Remove</button></div> : null}</header>
    {ids.map(id => <SelectedAccount key={id} id={id} active={active} refresh={0} read={remember} />)}
    <div hidden={step !== Step.Accounts}>
      {selectedRows.map(row => { const data = document(row); return <label className="worker-account-row" key={row.id}><input type="checkbox" aria-label={`Select ${resourceName(row)}`} checked onChange={() => update(value.key, { accounts: value.accounts.filter(account => account.id !== row.id) })} /><span><strong>{resourceName(row)}</strong><small>{accountStatus(data)}</small><small>Execution eligibility is checked at start.</small></span></label>; })}
      <button type="button" aria-expanded={choosing} onClick={() => setChoosing(value => !value)}>Choose accounts</button>
      <div hidden={!choosing}>
        <label>Account source<select data-wizard-field="source" aria-label={`Account source ${index + 1}`} value={sourceID} onChange={event => chooseSource(event.target.value)}><option value="">Select source</option>{Object.values(SubscriptionServiceId).filter(service => subscriptionServiceHarnesses[service] === harness).map(service => <option key={service} value={`${SourceKind.Subscription}:${service}`} disabled={duplicateSources.includes(`${SourceKind.Subscription}:${service}`)}>{subscriptionServiceNames[service]} subscription</option>)}{source?.kind === SourceKind.Api && !entries.some(entry => entry.providerId === source.id) ? <option value={sourceID}>{label} · retained source</option> : null}{entries.map(entry => <option key={entry.providerId} value={`${SourceKind.Api}:${entry.providerId}`} disabled={duplicateSources.includes(`${SourceKind.Api}:${entry.providerId}`)}>{entry.displayName || resourceName(entry.provider)}</option>)}</select></label>
        <Problem error={providers.error || provider.error} />{providers.isLoading ? <p role="status">Loading API sources…</p> : providers.data && entries.length === 0 ? <p>No enabled API sources on this page.</p> : null}
        <nav aria-label={`Source ${index + 1} provider pages`}><button type="button" disabled={!providerPage || providers.isFetching} onClick={() => setProviderPage("")}>First source page</button><button type="button" disabled={!providers.data?.nextPageToken || providers.isFetching} onClick={() => setProviderPage(providers.data!.nextPageToken)}>Next source page</button></nav>
        <Problem error={rows.error} />{source && rows.isLoading ? <p role="status">Loading accounts…</p> : null}{rows.data && !pageValid ? <p role="alert">This account page includes unsupported or mismatched source data.</p> : null}
        {pageValid ? rows.data!.resources.filter(row => !ids.includes(row.id)).map(row => <label className="worker-account-row" key={row.id}><input type="checkbox" checked={false} onChange={() => update(value.key, { accounts: [...value.accounts, { id: row.id, weight: 1 }] })} /><span><strong>{resourceName(row)}</strong><small>{accountStatus(document(row))}</small><small>Execution eligibility is checked at start.</small></span></label>) : null}
        {rows.data?.resources.length === 0 && !rows.error ? <p>No accounts for this source. Add accounts in AI Subscription or AI API Keys.</p> : null}
        {source ? <nav aria-label={`Source ${index + 1} account pages`}><button type="button" disabled={!accountPage || rows.isFetching} onClick={() => setAccountPage("")}>First account page</button><button type="button" disabled={!rows.data?.nextPageToken || rows.isFetching} onClick={() => setAccountPage(rows.data!.nextPageToken)}>Next account page</button><button type="button" disabled={rows.isFetching} onClick={() => { void rows.refetch(); }}>Refresh accounts</button></nav> : null}
      </div>
      <details className="worker-routing"><summary>Routing options · {value.routing === Routing.Priority ? "In order" : value.routing || "Server default"}</summary><label>Account routing<select data-wizard-field="routing" value={value.routing || ""} onChange={event => changeRouting(event.target.value)}><option value="">Server default</option>{Object.values(Routing).map(policy => <option key={policy} value={policy} disabled={policy === Routing.Fixed && ids.length !== 1}>{policy === Routing.Priority ? "In order" : policy}</option>)}</select></label><ol>{value.accounts.map((account, position) => <li key={text(account.id)}><strong>{known[text(account.id)] ? resourceName(known[text(account.id)]) : text(account.id)}</strong><label>Weight for account {position + 1}<input type="number" min={1} max={1000} value={Number(account.weight)} onChange={event => update(value.key, { accounts: value.accounts.map((item, i) => i === position ? { ...item, weight: Number(event.target.value) } : item) })} /></label><button type="button" disabled={position === 0} aria-label={`Move account ${position + 1} up`} onClick={() => { const accounts = [...value.accounts]; [accounts[position - 1], accounts[position]] = [accounts[position], accounts[position - 1]]; update(value.key, { accounts }); }}>Up</button></li>)}</ol></details>
    </div>
    <div hidden={step !== Step.Model}>
      <p>Search the saved catalog or enter an exact model ID for this source.</p>
      <div className="worker-model-combobox"><label htmlFor={`${listID}-input`}>Model for {label}</label><input id={`${listID}-input`} data-wizard-field="model" role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-controls={listID} aria-activedescendant={popup && highlight >= 0 ? `${listID}-${highlight}` : undefined} value={value.input} autoComplete="off" onFocus={() => setPopup(true)} onBlur={() => setPopup(false)} onChange={event => { update(value.key, { input: event.target.value, model: undefined, modelID: "" }); setModelPage(""); setPopup(true); setHighlight(-1); }} onKeyDown={event => {
        if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); const count = suggestions.length + (value.input.trim() ? 1 : 0); setPopup(true); setHighlight(position => count === 0 ? -1 : event.key === "ArrowDown" ? Math.min(position + 1, count - 1) : position <= 0 ? count - 1 : position - 1); }
        else if (event.key === "Escape" && popup) { event.preventDefault(); event.stopPropagation(); setPopup(false); }
        else if (event.key === "Enter" && popup) { event.preventDefault(); pick(suggestions[highlight]); }
      }} />{popup ? <ul id={listID} role="listbox" aria-label={`Models for ${label}`}>{suggestions.map((row, position) => <li key={row.id} id={`${listID}-${position}`} role="option" aria-selected={highlight === position} onMouseDown={event => event.preventDefault()} onClick={() => pick(row)}>{resourceName(row)}<small>{text(document(row).native_id)}{document(row).hidden === true ? " · Hidden" : ""}</small></li>)}{value.input.trim() ? <li id={`${listID}-${suggestions.length}`} role="option" aria-selected={highlight === suggestions.length} onMouseDown={event => event.preventDefault()} onClick={() => pick()}>Use exact ID “{value.input.trim()}”</li> : null}</ul> : null}</div>
      {value.modelID && initialModel.isLoading ? <p role="status">Checking selected model revision…</p> : null}<Problem error={models.error || initialModel.error} />{models.isLoading ? <p role="status">Loading model catalog…</p> : models.data && !catalogValid ? <p role="alert">The catalog includes unsupported or mismatched source data.</p> : models.data?.models.length === 0 ? <p>No saved models match. Enter an exact model ID.</p> : null}{models.error ? <p>Catalog lookup failed. You can enter an exact model ID.</p> : null}
      <nav aria-label={`Source ${index + 1} model pages`}><button type="button" disabled={!modelPage || models.isFetching} onClick={() => setModelPage("")}>First model page</button><button type="button" disabled={!models.data?.nextPageToken || models.isFetching} onClick={() => setModelPage(models.data!.nextPageToken)}>Next model page</button><button type="button" disabled={models.isFetching} onClick={() => { void models.refetch(); if (value.modelID) void initialModel.refetch(); }}>Reload saved catalog</button></nav>
      {source?.kind === SourceKind.Subscription ? <p>Subscription model discovery is unsupported. Use the saved catalog or an exact model ID.</p> : <><button type="button" disabled={locked || pending || !refreshAccount || document(provider.data?.resource).discovery !== true} onClick={() => { if (refreshAccount) void discovery.send({ mutation: { requestId: newRequestId(), id: refreshAccount.id, expectedRevision: refreshAccount.revision } }); }}>Refresh models from endpoint</button><p>Uses the first connected selected account. Discovery is separate from execution eligibility.</p></>}
      <Problem error={discovery.error} />
      {harness === Harness.Codex && source?.kind === SourceKind.Api ? <><NativeModelSettings active={active && step === Step.Model} selectedAccounts={selectedRows} pendingOperation={setNativePending} createModel={model => update(value.key, { input: text(model.native_id), modelID: "", model: undefined })} />{document(provider.data?.resource).protocol !== "openai-responses" ? <p>Codex needs a provider configured for Responses. Create a compatible custom provider and connect its account before execution.</p> : null}</> : null}
      <p>Saving a model does not establish execution readiness.</p>
    </div></fieldset>
    {discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>Retry the same model refresh</button> : null}
  </section>;
}

export function AgentWorkerSourceWizard({ initial, active, saved, cancel }: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : newConfiguration(EntityKind.AGENT));
  const [routes, setRoutes] = useState<Draft[]>(() => initial && items(document(initial).routes).length ? items(document(initial).routes).map(item => draft(object(item))) : [draft(initial ? { model_id: document(initial).model_id, accounts: document(initial).accounts, routing: document(initial).routing } : { routing: Routing.Priority })]);
  const [evidence, setEvidence] = useState<Record<string, Evidence>>({});
  const [step, setStep] = useState(Step.Harness);
  const [problem, setProblem] = useState("");
  const [focusKey, setFocusKey] = useState("");
  const [focusField, setFocusField] = useState("");
  const [focusAttempt, setFocusAttempt] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null), form = useRef<HTMLFormElement>(null);
  const notifications = useNotifications();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.AGENT, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const update = useCallback((key: string, patch: Partial<Draft>) => setRoutes(previous => previous.map(route => route.key === key ? { ...route, ...patch } : route)), []);
  const report = useCallback((key: string, value: Evidence) => setEvidence(previous => { const old = previous[key]; return old && old.accountError === value.accountError && old.modelError === value.modelError && old.pending === value.pending && old.label === value.label && old.accounts.join("\u0000") === value.accounts.join("\u0000") ? previous : { ...previous, [key]: value }; }), []);
  const mutation = useRetainedMutation(`agent-worker-wizard:${initial?.id ?? "new"}`, ConfigurationQuery.saveAgentWorker, (_result, request) => { notifications.notify({ kind: ToastKind.Success, message: "Agent Worker saved.", id: request.mutation?.requestId }); saved(); }, (result, request) => result.requestId === request.mutation?.requestId && result.resource?.kind === EntityKind.AGENT && supportsResourceSchema(result.resource) && (!initial || result.resource.id === initial.id));
  const blocked = mutation.busy || mutation.uncertain || routes.some(route => evidence[route.key]?.pending);
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  useLayoutEffect(() => {
    if (!active) return;
    const container = focusKey ? form.current?.querySelector<HTMLElement>(`[data-source-group="${focusKey}"]`) : form.current;
    const control = focusField ? container?.querySelector<HTMLElement>(focusField === "name" ? ".agent-core input" : `[data-wizard-field="${focusField}"]`) : undefined;
    if (control) { const disclosure = control.closest<HTMLDetailsElement>("details"); if (disclosure) disclosure.open = true; const parent = control.closest<HTMLElement>("[hidden]"); if (parent) parent.hidden = false; control.focus(); } else heading.current?.focus();
  }, [active, step, focusKey, focusField, focusAttempt]);
  const focus = (key = "", field = "") => { setFocusKey(key); setFocusField(field); setFocusAttempt(value => value + 1); };
  const fail = (target: Step, message: string, key = "", field = "") => { setStep(target); setProblem(message); focus(key, field); return false; };
  const validate = (through: Step) => {
    if (!Object.values(Harness).includes(data.harness as Harness)) return fail(Step.Harness, "Choose a supported harness.", "", "harness");
    const keys = routes.map(route => sourceKey(route.source));
    if (through >= Step.Accounts) {
      const count = routes.reduce((count, route) => count + route.accounts.length, 0), ids = routes.flatMap(route => route.accounts.map(account => text(account.id)));
      if (count > 1000 || new Set(ids).size !== count) return fail(Step.Accounts, "Select at most 1,000 distinct accounts across all sources.");
      for (const route of routes) if (!evidence[route.key] || evidence[route.key].accountError || keys.filter(key => key === sourceKey(route.source)).length !== 1) return fail(Step.Accounts, evidence[route.key]?.accountError || "Choose distinct sources with current accounts.", route.key, "source");
    }
    if (through >= Step.Model) for (const route of routes) if (!evidence[route.key] || evidence[route.key].modelError) return fail(Step.Model, evidence[route.key]?.modelError || "Choose a model for every source.", route.key, "model");
    if (through >= Step.Configure && (!text(data.name).trim() || new TextEncoder().encode(text(data.name)).byteLength > 256)) return fail(Step.Configure, "Enter a name of at most 256 UTF-8 bytes.", "", "name");
    return true;
  };
  const advance = () => { if (validate(step)) { setStep(step + 1); setProblem(""); focus(); } };
  const move = (key: string, offset: number) => { setRoutes(previous => { const next = [...previous], index = next.findIndex(route => route.key === key); [next[index], next[index + offset]] = [next[index + offset], next[index]]; return next; }); setProblem(""); focus(key, "source-heading"); };
  return <form ref={form} className="agent-configuration worker-wizard worker-source-wizard" noValidate onInvalidCapture={revealAgentInvalidControl} onSubmit={event => {
    event.preventDefault(); if (!active || blocked) return; if (step !== Step.Configure) { advance(); return; }
    if (!validate(Step.Configure) || stale || initial && (!current.data?.resource || current.error)) return;
    if (!form.current?.checkValidity()) { revealAgentInvalidControl(event); form.current?.querySelector<HTMLElement>("input:invalid, select:invalid, textarea:invalid")?.focus(); return; }
    const next: Document = { ...data }; delete next.reconfiguration_required; delete next.accounts; delete next.model_id; delete next.routing; delete next.routes;
    const selections = routes.map(route => ({ selection: route.model ? { case: "modelId" as const, value: route.model.id } : { case: "nativeId" as const, value: route.input.trim() }, expectedModelRevision: route.model?.revision ?? 0n }));
    const sourceRoutes = routes.map(route => { const value: Document = { ...route.original, model_id: route.model?.id ?? "", accounts: route.accounts }; if (route.routing) value.routing = route.routing; else delete value.routing; return value; });
    const expanded = initial?.schemaVersion === 3 || routes.length > 1;
    if (expanded) next.routes = sourceRoutes; else Object.assign(next, sourceRoutes[0]);
    if (encode(next).byteLength > 1 << 20) { fail(Step.Configure, "This configuration is too large."); return; }
    void mutation.send({ mutation: { requestId: newRequestId(), id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n }, schemaVersion: expanded ? 3 : 1, documentJson: encode(next), ...(expanded ? { routeModels: selections } : { model: selections[0] }) });
  }}>
    <h2>{initial ? "Edit Agent Worker" : "New Agent Worker"}</h2><ol className="worker-steps" aria-label="Worker configuration steps">{[Step.Harness, Step.Accounts, Step.Model, Step.Configure].map(value => <li key={value} aria-current={step === value ? "step" : undefined} data-completed={value < step}><span>{value}</span><span>{names[value]}</span></li>)}</ol><h3 ref={heading} tabIndex={-1}>{names[step]}</h3>
    <fieldset disabled={mutation.busy || mutation.uncertain}>
      <section hidden={step !== Step.Harness}><WorkerHarnessPicker value={data.harness} disabled={blocked || !active} change={harness => { if (harness === data.harness) return; setData(previous => ({ ...previous, harness })); setRoutes([draft({ routing: Routing.Priority })]); setProblem(""); }} /></section>
      <div hidden={step !== Step.Accounts}><p>{harnessNames[data.harness as Harness]} <button type="button" disabled={blocked} onClick={() => { setStep(Step.Harness); focus(); }}>Change harness</button></p><h4>Account sources</h4><p>Use subscription accounts first, then API accounts when their quota is exhausted.</p></div>
      {routes.map((route, index) => <SourceGroup key={route.key} value={route} index={index} count={routes.length} step={step} harness={data.harness as Harness} active={active} locked={blocked} duplicateSources={routes.filter(other => other.key !== route.key).map(other => sourceKey(other.source))} update={update} report={report} move={move} remove={key => { setRoutes(previous => previous.filter(route => route.key !== key)); focus(); }} />)}
      <div hidden={step !== Step.Accounts}><button type="button" disabled={blocked || routes.length >= 1000} onClick={() => { const next = draft({ routing: Routing.Priority }); setRoutes(previous => [...previous, next]); focus(next.key, "source"); }}>+ Add account source</button><p>New sessions only. Sources advance only after confirmed quota exhaustion. Recovered subscription quota is preferred again.</p><p>Choose a model for each source in the next step.</p></div>
      <fieldset hidden={step !== Step.Configure} disabled={blocked || step !== Step.Configure}><ConfigurationFields kind={EntityKind.AGENT} data={data} change={setData} existing={Boolean(initial)} active={active && step === Step.Configure} workerWizard /><section className="worker-summary" aria-label="Worker configuration summary"><h4>Review configuration</h4><p>{harnessNames[data.harness as Harness]}</p><ol>{routes.map(route => <li key={route.key}><strong>{evidence[route.key]?.label}</strong><p>Model: {route.input}</p><p>Accounts: {evidence[route.key]?.accounts.join(", ")}</p><p>Routing: {route.routing === Routing.Priority ? "In order" : route.routing || "Server default"}</p></li>)}</ol><p>Execution checks current accounts and the installed harness before the first run.</p></section></fieldset>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error || current.error} />{stale ? <p role="alert">This Worker changed elsewhere. Your draft is retained. Cancel and reopen the current Worker before saving.</p> : null}
    <div className="worker-footer"><button type="button" disabled={blocked} onClick={cancel}>Cancel</button><div>{step > Step.Harness ? <button type="button" disabled={blocked} onClick={() => { setStep(step - 1); setProblem(""); focus(); }}>Back</button> : null}<button type="submit" className="primary" disabled={!active || blocked || step === Step.Configure && (stale || Boolean(initial && (!current.data?.resource || current.error)))}>{mutation.busy ? "Saving…" : step === Step.Configure ? "Save Agent Worker" : "Next"}</button></div></div>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same Worker save</button> : null}
  </form>;
}
