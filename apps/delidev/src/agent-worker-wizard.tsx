// SPDX-License-Identifier: Apache-2.0
import { useCallback, useDeferredValue, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountTypeFilter, ConfigurationQuery, EntityKind, ProviderQuery, ResourceQuery, SubscriptionServiceId, SubscriptionServiceIdentity, SystemCapability, SystemQuery, newRequestId, subscriptionService, subscriptionServiceHarnesses, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationFields, Harness, Routing, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { NativeModelSettings } from "./native-model-settings";
import { Problem } from "./ui";
import { revealAgentInvalidControl } from "./agent-configuration";
import { ToastKind, useNotifications } from "./toast-notifications";
import "./agent-worker-wizard.css";
import { SourceKind, SelectedAccount, fromKey, modelSource, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { AgentWorkerSourceWizard } from "./agent-worker-source-wizard";

enum Step { Harness = 1, Accounts, Model, Configure }
const steps = [Step.Harness, Step.Accounts, Step.Model, Step.Configure];
const stepNames = { [Step.Harness]: "Harness", [Step.Accounts]: "Accounts", [Step.Model]: "Model", [Step.Configure]: "Configure" };
const harnessNames: Record<Harness, string> = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };

function LegacyAgentWorkerWizard({ initial, active, saved, cancel }: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : newConfiguration(EntityKind.AGENT));
  const [step, setStep] = useState(Step.Harness);
  const [source, setSource] = useState<Source>();
  const [providerPage, setProviderPage] = useState("");
  const [accountPage, setAccountPage] = useState("");
  const [modelPage, setModelPage] = useState("");
  const [knownAccounts, setKnownAccounts] = useState<Record<string, Resource | undefined>>({});
  const [accountRefresh, setAccountRefresh] = useState(0);
  const [model, setModel] = useState<Resource>();
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
  const accountRows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: accountPage }, providerId: source?.kind === SourceKind.Api ? source.id : "", accountType: source?.kind === SourceKind.Api ? AccountTypeFilter.API : AccountTypeFilter.SUBSCRIPTION, subscriptionService: wireService(source) }, { enabled: active && supported && Boolean(source) });
  const query = useDeferredValue(input);
  const models = useQuery(ProviderQuery.searchModels, { query, providerId: source?.kind === SourceKind.Api ? source.id : "", subscriptionService: wireService(source), includeHidden: true, enabledProvidersOnly: true, pageSize: 50, pageToken: modelPage }, { enabled: active && supported && Boolean(source) && step === Step.Model });
  const currentModel = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: model?.id ?? "" }, { enabled: active && supported && Boolean(model), refetchInterval: active && model ? 5000 : false });
  const accountPageValid = accountRows.data?.resources.every(row => sameSource(row, source));
  const modelPageValid = models.data?.models.every(row => supportsResourceSchema(row) && sourceKey(modelSource(row)) === sourceKey(source));
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
    notifications.notify({ kind: ToastKind.Success, message: "Agent Worker saved.", id: request.mutation?.requestId }); saved();
  }, (result, request) => result.requestId === request.mutation?.requestId && result.resource?.kind === EntityKind.AGENT && supportsResourceSchema(result.resource) && (!initial || result.resource.id === initial.id));
  const discovery = useRetainedMutation(`agent-worker-wizard:discovery:${sourceKey(source)}`, ProviderQuery.discoverModels, result => {
    if (result.account) remember(result.account.id, result.account);
    setModelPage(""); void models.refetch(); void accountRows.refetch();
  }, (result, request) => result.requestId === request.mutation?.requestId && result.account?.id === request.mutation?.id);
  const blocked = nativePending || mutation.busy || mutation.uncertain || discovery.busy || discovery.uncertain;
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const change = (value: Document) => { if (encode(value).byteLength > 1 << 20) { setProblem("This configuration is too large."); return; } setData(value); setProblem(""); };
  const clearModel = () => { setModel(undefined); setInput(""); setModelPage(""); setPopup(false); setHighlight(-1); };
  const chooseSource = (value: string) => {
    initialized.current = true;
    setSource(fromKey(value)); setAccountPage(""); clearModel();
    change({ ...data, accounts: [], model_id: "" });
    setProblem("Select accounts and a model for this source.");
  };
  const sourceLabel = source?.kind === SourceKind.Subscription ? subscriptionServiceNames[source.id as SubscriptionServiceId] : selectedProvider.data?.resource ? resourceName(selectedProvider.data.resource) : source?.id ?? "";
  const selectedRows = ids.flatMap(id => knownAccounts[id] ? [knownAccounts[id]!] : []);
  const fail = (target: Step, message: string, field = "") => { setStep(target); setProblem(message); setFocusField(field); setFocusAttempt(value => value + 1); };
  const validate = (through: Step): boolean => {
    if (!Object.values(Harness).includes(data.harness as Harness)) { fail(Step.Harness, "Choose a supported harness.", "harness"); return false; }
    if (through >= Step.Accounts) {
      if (!source || source.kind === SourceKind.Subscription && subscriptionServiceHarnesses[source.id as SubscriptionServiceId] !== data.harness) { fail(Step.Accounts, "Choose an account source for this harness.", "source"); return false; }
      if (!ids.length || ids.length > 1000 || ids.some(id => !knownAccounts[id] || !sameSource(knownAccounts[id]!, source))) { fail(Step.Accounts, "Select at least one current account from this source. Refresh unavailable selections.", "source"); return false; }
      if (data.routing === Routing.Fixed && ids.length !== 1 || links.some(link => !Number.isInteger(link.weight) || Number(link.weight) < 1 || Number(link.weight) > 1000)) { fail(Step.Accounts, "Fixed routing requires one account. Weights must be integers from 1 to 1,000.", "routing"); return false; }
    }
    if (through >= Step.Model && (!input.trim() || new TextEncoder().encode(input.trim()).byteLength > 256 || model && sourceKey(modelSource(model)) !== sourceKey(source))) { fail(Step.Model, "Select a model from this source or enter its exact model ID.", "model"); return false; }
    if (through >= Step.Model && model && (currentModel.error || through === Step.Configure && !currentModel.data?.resource || currentModel.data?.resource && (currentModel.data.resource.revision !== model.revision || sourceKey(modelSource(currentModel.data.resource)) !== sourceKey(source)))) { fail(Step.Model, "The selected model is unavailable or changed. Reload the catalog and explicitly select its current revision, or enter an exact model ID.", "model"); return false; }
    if (through >= Step.Configure && (!text(data.name).trim() || new TextEncoder().encode(text(data.name)).byteLength > 256)) { fail(Step.Configure, "Enter a name of at most 256 UTF-8 bytes.", "name"); return false; }
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
    if (ids.some(id => !knownAccounts[id] || !sameSource(knownAccounts[id]!, source))) fail(Step.Accounts, "A selected account is unavailable or changed source. Refresh and explicitly select current accounts.", "source");
    else if (model && (currentModel.error || currentModel.data?.resource && currentModel.data.resource.revision !== model.revision)) fail(Step.Model, "The selected model is unavailable or changed. Reload the catalog and explicitly select its current revision, or enter an exact model ID.", "model");
  }, [active, mutation.error, mutation.uncertain, step, data.accounts, knownAccounts, source, model, currentModel.data, currentModel.error]);
  const pick = (row?: Resource) => { setModelPage(""); if (row && row.id === model?.id) void currentModel.refetch(); setModel(row); if (row) setInput(text(document(row).native_id)); setPopup(false); setHighlight(-1); setProblem(""); };
  const suggestions = modelPageValid ? models.data!.models : [];
  useLayoutEffect(() => {
    if (active && popup && highlight >= 0) (suggestionList.current?.children.item(highlight) as HTMLElement | null)?.scrollIntoView?.({ block: "nearest" });
  }, [active, popup, highlight, models.data]);
  const refreshAccount = selectedRows.find(row => Boolean(document(row).connection) && document(row).enabled !== false && !document(row).removal);
  const providerEntries = providers.data?.entries.filter(entry => entry.enabled && entry.providerId) ?? [];
  const advance = () => { if (validate(step)) { setStep(step + 1); setFocusField(""); setProblem(""); } };
  return <form ref={form} className="agent-configuration worker-wizard" noValidate onSubmit={event => {
    event.preventDefault(); if (blocked || !active || !supported) return;
    if (step !== Step.Configure) { advance(); return; }
    if (!validate(Step.Configure) || stale || Boolean(initial && (current.error || !current.data?.resource))) return;
    if (!form.current?.checkValidity()) { const invalid = form.current?.querySelector<HTMLInputElement>("input:invalid, select:invalid, textarea:invalid"); if (invalid) { const details = invalid.closest("details"); if (details) details.open = true; invalid.focus(); } return; }
    const next: Document = { ...data, model_id: model?.id ?? "" };
    delete next.reconfiguration_required;
    void mutation.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(next), model: { selection: model ? { case: "modelId", value: model.id } : { case: "nativeId", value: input.trim() }, expectedModelRevision: model?.revision ?? 0n } });
  }} onInvalidCapture={revealAgentInvalidControl}>
    <h2>{initial ? "Edit Agent Worker" : "New Agent Worker"}</h2>
    <ol className="worker-steps" aria-label="Worker configuration steps">{steps.map(value => <li key={value} aria-current={step === value ? "step" : undefined} data-completed={value < step}><span>{value}</span><span>{stepNames[value]}</span></li>)}</ol>
    <h3 ref={heading} tabIndex={-1}>{stepNames[step]}</h3>
    <Problem error={status.error} />
    {status.isLoading ? <p role="status">Checking server support…</p> : status.data && !supported ? <p role="alert">Update the server to configure Agent Workers with this wizard.</p> : null}
    {initial && data.reconfiguration_required === true ? <p role="status">This Worker needs explicit account and model reconfiguration. Historical executions keep their original attribution.</p> : null}
    <fieldset disabled={blocked || !supported}>
      <section hidden={step !== Step.Harness}>
        <p>Choose the tool that runs this Worker.</p>
        <label>Harness<select data-wizard-field="harness" value={text(data.harness)} onChange={event => { initialized.current = true; setSource(undefined); clearModel(); change({ ...data, harness: event.target.value, accounts: [], model_id: "" }); }}>{Object.values(Harness).map(value => <option key={value} value={value}>{harnessNames[value]}</option>)}</select></label>
      </section>
      <section hidden={step !== Step.Accounts} aria-label="Choose accounts">
        <p>{harnessNames[data.harness as Harness]} <button type="button" onClick={() => { setStep(Step.Harness); setFocusField(""); }}>Change harness</button></p>
        <h4>Choose accounts</h4><p>Select at least one account. You can select several from the same source.</p>
        <label>Account source<select data-wizard-field="source" value={sourceKey(source)} onChange={event => chooseSource(event.target.value)}><option value="">Select source</option>{Object.values(SubscriptionServiceId).filter(value => subscriptionServiceHarnesses[value] === data.harness).map(value => <option key={value} value={`${SourceKind.Subscription}:${value}`}>{subscriptionServiceNames[value]} subscription</option>)}{source?.kind === SourceKind.Api && !providerEntries.some(entry => entry.providerId === source.id) ? <option value={sourceKey(source)}>{sourceLabel}{document(selectedProvider.data?.resource).enabled === false ? " · Off" : " · retained source"}</option> : null}{providerEntries.map(entry => <option key={entry.providerId} value={`${SourceKind.Api}:${entry.providerId}`}>{entry.displayName || resourceName(entry.provider)}</option>)}</select></label>
        <Problem error={providers.error || selectedProvider.error} />
        {providers.isLoading ? <p role="status">Loading API sources…</p> : null}
        <nav aria-label="API source pages"><button type="button" disabled={!providerPage || providers.isFetching} onClick={() => setProviderPage("")}>First source page</button><button type="button" disabled={!providers.data?.nextPageToken || providers.isFetching} onClick={() => setProviderPage(providers.data!.nextPageToken)}>Next source page</button></nav>
        <Problem error={accountRows.error} />
        {source && accountRows.isLoading ? <p role="status">Loading accounts…</p> : null}
        {accountRows.error && accountRows.data ? <p role="status">Refresh failed. Showing the last successfully loaded accounts.</p> : null}
        {accountRows.data && !accountPageValid ? <p role="alert">This account page includes unsupported or mismatched source data. Refresh accounts before selecting it.</p> : null}
        <div className="worker-account-list">{accountPageValid ? accountRows.data!.resources.map(row => { const value = document(row); return <label className="worker-account-row" key={row.id}><input type="checkbox" checked={ids.includes(row.id)} onChange={event => change({ ...data, accounts: event.target.checked ? [...links, { id: row.id, weight: 1 }] : links.filter(link => link.id !== row.id) })} /><span><strong>{resourceName(row)}</strong><small>Connection: {value.connection ? "Connected" : "Disconnected"} · Health: {text(value.health) || "Unavailable"}</small><small>Execution eligibility: {value.enabled === false ? "Disabled" : value.removal ? "Removal pending" : "Checked when execution starts"}</small></span></label>; }) : null}</div>
        {source && accountRows.data?.resources.length === 0 && !accountRows.error ? <p>{accountPage ? "No accounts on this page." : "No accounts for this source. Add an account in AI Subscription or AI API Keys."}</p> : null}
        {source ? <nav aria-label="Account pages"><button type="button" disabled={!accountPage || accountRows.isFetching} onClick={() => setAccountPage("")}>First account page</button><button type="button" disabled={!accountRows.data?.nextPageToken || accountRows.isFetching} onClick={() => setAccountPage(accountRows.data!.nextPageToken)}>Next account page</button><button type="button" disabled={accountRows.isFetching} onClick={() => void accountRows.refetch()}>Refresh accounts</button></nav> : null}
        <p>{ids.length} accounts selected</p><p>All selected accounts must use the same subscription service or API provider.</p>
        {ids.map(id => <SelectedAccount key={id} id={id} active={active && supported} refresh={accountRefresh} read={remember} />)}
        <details className="worker-routing"><summary>Routing options <small>{text(data.routing) || "Server default"} · {links.every(link => link.weight === 1) ? "equal weights" : "custom weights"}</small></summary>
          <label>Account routing<select data-wizard-field="routing" value={text(data.routing)} onChange={event => { const next = { ...data }; if (event.target.value) next.routing = event.target.value; else delete next.routing; change(next); }}><option value="">Server default</option>{Object.values(Routing).map(value => <option key={value} value={value} disabled={value === Routing.Fixed && ids.length !== 1}>{value}</option>)}</select></label>
          <ol>{links.map((link, index) => <li key={text(link.id)}><strong>{knownAccounts[text(link.id)] ? resourceName(knownAccounts[text(link.id)]) : text(link.id)}</strong><label>Weight for account {index + 1}<input type="number" min={1} max={1000} value={Number(link.weight)} onChange={event => change({ ...data, accounts: links.map((value, i) => i === index ? { ...value, weight: Number(event.target.value) } : value) })} /></label><div className="actions"><button type="button" disabled={index === 0} aria-label={`Move account ${index + 1} up`} onClick={() => { const next = [...links]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; change({ ...data, accounts: next }); }}>Up</button><button type="button" aria-label={`Remove account ${index + 1}`} onClick={() => change({ ...data, accounts: links.filter((_, i) => i !== index) })}>Remove</button></div></li>)}</ol>
        </details>
      </section>
      <section hidden={step !== Step.Model}>
        <p>{harnessNames[data.harness as Harness]} · {sourceLabel} · {ids.length} accounts</p>
        <h4>Choose a model</h4><p>Search the saved catalog or enter an exact model ID.</p>
        <div className="worker-model-combobox"><label htmlFor={`${listID}-input`}>Model</label><input id={`${listID}-input`} data-wizard-field="model" role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-controls={listID} aria-activedescendant={popup && highlight >= 0 && highlight < suggestions.length + (input.trim() ? 1 : 0) ? `${listID}-${highlight}` : undefined} value={input} maxLength={256} autoComplete="off" onFocus={() => setPopup(true)} onBlur={() => setPopup(false)} onChange={event => { setInput(event.target.value); setModel(undefined); setModelPage(""); setPopup(true); setHighlight(-1); setProblem(""); }} onKeyDown={event => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); setPopup(true); const count = suggestions.length + (input.trim() ? 1 : 0); setHighlight(value => count === 0 ? -1 : event.key === "ArrowDown" ? Math.min(value + 1, count - 1) : value <= 0 ? count - 1 : value - 1); }
          else if (event.key === "Escape" && popup) { event.preventDefault(); event.stopPropagation(); setPopup(false); }
          else if (event.key === "Enter" && popup) { event.preventDefault(); pick(suggestions[highlight]); }
        }} />
          {popup ? <ul ref={suggestionList} id={listID} role="listbox" aria-label="Model suggestions">{suggestions.map((row, index) => <li id={`${listID}-${index}`} key={row.id} role="option" aria-selected={highlight >= 0 ? highlight === index : model?.id === row.id} onMouseDown={event => event.preventDefault()} onClick={() => pick(row)}><strong>{resourceName(row)}</strong><small>{text(document(row).native_id)}{document(row).hidden === true ? " · Hidden" : ""}</small></li>)}{input.trim() ? <li id={`${listID}-${suggestions.length}`} role="option" aria-selected={highlight === suggestions.length} onMouseDown={event => event.preventDefault()} onClick={() => pick()}>Use exact ID “{input.trim()}”</li> : null}</ul> : null}
        </div>
        {models.isLoading ? <p role="status">Loading model catalog…</p> : models.isFetching ? <p role="status">Refreshing saved catalog…</p> : null}<Problem error={models.error} />
        {models.data && !modelPageValid ? <p role="alert">This catalog page includes unsupported or mismatched source data. Reload the saved catalog or enter an exact model ID.</p> : null}
        {models.error ? <p role="status">Catalog lookup failed. {models.data ? "The displayed results may be stale. " : ""}You can enter an exact model ID.</p> : models.data?.models.length === 0 ? <p>{modelPage ? "No models on this page." : "No saved models match. Enter an exact model ID."}</p> : null}
        <nav aria-label="Model catalog pages"><button type="button" disabled={!modelPage || models.isFetching} onClick={() => setModelPage("")}>First model page</button><button type="button" disabled={!models.data?.nextPageToken || models.isFetching} onClick={() => setModelPage(models.data!.nextPageToken)}>Next model page</button><button type="button" disabled={models.isFetching} onClick={() => { void models.refetch(); if (model) void currentModel.refetch(); }}>Reload saved catalog</button></nav>
        {source?.kind === SourceKind.Subscription ? <p>Subscription model discovery is unsupported. Use the saved catalog or enter an exact model ID.</p> : <><button type="button" disabled={blocked || !refreshAccount || document(selectedProvider.data?.resource).discovery !== true || document(selectedProvider.data?.resource).enabled === false} onClick={() => { if (refreshAccount) void discovery.send({ mutation: { requestId: newRequestId(), id: refreshAccount.id, expectedRevision: refreshAccount.revision } }); }}>Refresh models from endpoint</button><p>Uses the first connected selected account. Discovery is separate from execution eligibility.</p></>}
        <Problem error={discovery.error} />{discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>Retry the same model refresh</button> : null}
        {data.harness === Harness.Codex && source?.kind === SourceKind.Api ? <NativeModelSettings active={active && step === Step.Model} selectedAccounts={selectedRows} pendingOperation={setNativePending} createModel={value => { setInput(text(value.native_id)); setModel(undefined); setModelPage(""); setPopup(false); setHighlight(-1); setProblem(""); }} /> : null}
        <p>Model availability and saving this configuration do not establish execution readiness.</p>
        <Problem error={currentModel.error} />
      </section>
      <fieldset hidden={step !== Step.Configure} disabled={step !== Step.Configure}>
        <ConfigurationFields kind={EntityKind.AGENT} data={data} change={change} active={active && step === Step.Configure} existing={Boolean(initial)} workerWizard />
        <section className="worker-summary" aria-label="Worker configuration summary"><h4>Review configuration</h4><p>{harnessNames[data.harness as Harness]} · {sourceLabel}</p><p>Model: {input || "None selected"}</p><ol>{ids.map(id => <li key={id}>{knownAccounts[id] ? resourceName(knownAccounts[id]) : id}</li>)}</ol><p>Routing: {text(data.routing) || "Server default"}</p><p>Saved compatibility is a configuration declaration. Execution checks the current accounts and installed harness.</p></section>
      </fieldset>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error || current.error || originalModel.error} />
    {stale ? <p role="alert">This Worker changed elsewhere. Your draft is retained. Cancel and reopen the current Worker before saving.</p> : null}
    {model && currentModel.data?.resource && currentModel.data.resource.revision !== model.revision ? <p role="status">The selected model changed. Return to Model and explicitly reselect it.</p> : null}
    {model && currentModel.isLoading ? <p role="status">Checking the selected model revision…</p> : null}
    <div className="worker-footer"><button type="button" disabled={blocked} onClick={cancel}>Cancel</button><div>{step > Step.Harness ? <button type="button" disabled={blocked} onClick={() => { setStep(step - 1); setFocusField(""); setProblem(""); }}>Back</button> : null}<button type="submit" className="primary" disabled={blocked || !active || !supported || step === Step.Configure && (stale || currentModel.isLoading || Boolean(initial && (current.error || !current.data?.resource)))}>{mutation.busy ? "Saving…" : step === Step.Configure ? "Save Agent Worker" : "Next"}</button></div></div>
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same Worker save</button> : null}
  </form>;
}

export function AgentWorkerWizard(props: { initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: props.active });
  if (status.data?.capabilities.includes(SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1)) return <AgentWorkerSourceWizard {...props} />;
  if (props.initial?.schemaVersion === 3) return <><p role="alert">Update the server to edit this Worker's account sources.</p><button onClick={props.cancel}>Cancel</button></>;
  return <LegacyAgentWorkerWizard {...props} />;
}
