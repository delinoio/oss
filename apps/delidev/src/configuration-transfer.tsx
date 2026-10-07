import { productError, ProductError,  ownedMessage, useProductMessage, LocalizedText, copy, useLocale   } from "./localization";
import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ResourceQuery, SystemQuery, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { useQueryClient } from "@tanstack/react-query";
import { document, items, object, text, type Document } from "./documents";
import { ResourceChoice, TextField } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { useSettingsOpening } from "./settings-lifetime";
import { JobState } from "./jobs";
import { ServiceProblem, Problem  } from "./ui";
import "./configuration-transfer.css";

enum TransferStage { Load = 1, Map = 2, Review = 3 }
const transferStages = [
  { stage: TransferStage.Load, get label() { return copy("configuration-transfer.extra.f7bf946df96b"); } },
  { stage: TransferStage.Map, get label() { return copy("configuration-transfer.extra.8cb2c8cb8b4b"); } },
  { stage: TransferStage.Review, get label() { return copy("configuration-transfer.extra.61666b10759b"); } },
];

enum ImportAction { Create = "create", Reuse = "reuse", Replace = "replace" }
const kinds: Record<string, EntityKind> = { provider: EntityKind.PROVIDER, model: EntityKind.MODEL, account: EntityKind.ACCOUNT, template: EntityKind.TEMPLATE, agent: EntityKind.AGENT, repository: EntityKind.REPOSITORY, project: EntityKind.PROJECT, settings: EntityKind.SETTINGS };
const canonicalId = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const encoder = new TextEncoder(), decoder = new TextDecoder("utf-8", { fatal: true });
const bundleLimit = 384 << 10;
interface Entry { id: string; kind: string; document: Document }
interface Machine { id: string; name: string; os: string; architecture: string }
interface Bundle { version: number; entries: Entry[]; machines: Machine[] }
interface Binding { action: ImportAction; target: string; revision?: bigint }
interface Preview { bytes: Uint8Array; changes: Document[]; machines: Document[] }
const name = (entry: Entry) => text(entry.document.name) || text(entry.document.alias) || copy("configuration-transfer.extra.eba66b2c00bb");
function readBundle(raw: string): Bundle {
  if (encoder.encode(raw).byteLength > bundleLimit) throw new ProductError("validation.0c01933238f8");
  const value = object(JSON.parse(raw));
  if (![1, 2, 3, 4].includes(value.version as number) || !Array.isArray(value.entries) || !Array.isArray(value.machines) || value.entries.length > 256 || value.machines.length > 64) throw new ProductError("validation.71aacc919010");
  const ids = new Set<string>();
  for (const item of value.entries) {
    const entry = object(item), id = text(entry.id);
    if (!canonicalId.test(id) || ids.has(id) || !Object.hasOwn(kinds, text(entry.kind)) || !entry.document || typeof entry.document !== "object" || Array.isArray(entry.document)) throw new ProductError("validation.c2538b95da40");
    const data = object(entry.document);
    if (value.version === 1 && (data.type === "subscription" || data.source_kind === "subscription" || data.protocol === "native-subscription" || data.subscription_service !== undefined)) throw new ProductError("validation.3944ec33203a");
    if (Number(value.version) < 4 && (data.api_formats !== undefined || data.api_protocol !== undefined)) throw new ProductError("validation.71aacc919010");
    if (Number(value.version) < 3 && entry.kind === "agent" && data.routes !== undefined) throw new ProductError("validation.sourceRoutesVersion");
    ids.add(id);
  }
  for (const item of value.machines) {
    const machine = object(item), id = text(machine.id);
    if (!canonicalId.test(id) || ids.has(id) || !text(machine.name) || !["darwin", "linux", "windows"].includes(text(machine.os)) || !["arm64", "amd64"].includes(text(machine.architecture))) throw new ProductError("validation.1661a2b58b48");
    ids.add(id);
  }
  let checkouts = 0;
  for (const item of value.entries) {
    const entry = object(item), data = object(entry.document);
    if (entry.kind !== "repository") continue;
    if (!Array.isArray(data.checkouts)) throw new ProductError("validation.261f35fd3668");
    checkouts += data.checkouts.length;
    if (checkouts > 64) throw new ProductError("validation.33fa4516c746");
    const seen = new Set<string>();
    for (const checkout of data.checkouts) {
      const id = text(object(checkout).machine_id);
      if (!canonicalId.test(id) || seen.has(id)) throw new ProductError("validation.1ee6ecb287eb");
      seen.add(id);
    }
  }
  return value as unknown as Bundle;
}
function readPreview(bytes: Uint8Array): Preview {
  if (bytes.byteLength > 1 << 20) throw new ProductError("validation.6eb6dd2fc3cb");
  const value = object(JSON.parse(decoder.decode(bytes))), plan = object(value.plan);
  if (!text(value.token) || ![1, 2, 3, 4].includes(plan.version as number) || !Array.isArray(plan.changes) || !plan.changes.length || plan.changes.length > 256 || !Array.isArray(plan.machines)) throw new ProductError("validation.9b79652ebc21");
  for (const item of plan.changes) {
    const change = object(item);
    if (!canonicalId.test(text(change.id)) || !canonicalId.test(text(change.source_id)) || !Object.hasOwn(kinds, text(change.kind)) || !Object.values(ImportAction).includes(change.action as ImportAction) || !change.after || typeof change.after !== "object" || Array.isArray(change.after)) throw new ProductError("validation.028484655c91");
  }
  return { bytes: bytes.slice(), changes: plan.changes.map(object), machines: plan.machines.map(object) };
}

// Formatting changes whitespace only. JSON.stringify on parsed preview data
// would round large integer literals and misrepresent the reviewed change.
export function formatConfigurationReview(raw: string): string {
  const tokens = raw.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:]+/g) ?? [];
  let depth = 0, result = "";
  const indent = () => "  ".repeat(depth);
  for (const token of tokens) {
    if (token === "{" || token === "[") { depth++; result += token + "\n" + indent(); }
    else if (token === "}" || token === "]") { depth = Math.max(0, depth - 1); result += "\n" + indent() + token; }
    else if (token === ",") result += ",\n" + indent();
    else if (token === ":") result += ": ";
    else result += token;
    if (result.length > 4 << 20) return raw;
  }
  return result;
}

// The original JSON bytes, not a JS number round trip, are the export/import
// authority. Parsed objects below are display-only; revisions use bigint.
export function ConfigurationTransfer({ active, showCategoryIntro = true, onWorkflowReadyChange }: { active: boolean; showCategoryIntro?: boolean; onWorkflowReadyChange?: (active: boolean) => void }) {
  useLocale();
  const [exported, setExported] = useState("");
  const [draft, setDraft] = useState("");
  const [loaded, setLoaded] = useState<{ raw: string; bundle: Bundle }>();
  const [bindings, setBindings] = useState<Record<string, Binding>>({});
  const [machines, setMachines] = useState<Record<string, string>>({});
  const [paths, setPaths] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview>();
  const [report, setReport] = useState<Document>();
  const formattedPreview = useMemo(() => preview ? formatConfigurationReview(decoder.decode(preview.bytes)) : "", [preview]);
  const [problem, setProblem] = useProductMessage("");
  const [loading, setLoading] = useState(false);
  const alive = useRef(false), generation = useRef(0), gate = useRef(false);
  const exportText = useRef<HTMLTextAreaElement>(null);
  const queryClient = useQueryClient();
  const opening = useSettingsOpening();
  // Legacy exports contain checkout-backed repositories without the URL-only
  // field. Only imports that contain a remote repository need capability 37.
  const containsRemoteRepositories = loaded?.bundle.entries.some(entry => entry.kind === "repository" && typeof entry.document.remote_url === "string" && entry.document.remote_url !== "") === true;
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && containsRemoteRepositories, retry: false });
  const remoteUnsupported = containsRemoteRepositories && status.data !== undefined && !status.data.capabilities.includes(SystemCapability.REMOTE_REPOSITORIES_V1);
  const statusPending = containsRemoteRepositories && status.data === undefined && !status.error;
  const statusFailed = containsRemoteRepositories && Boolean(status.error);
  const exportRead = useMutation(ConfigurationQuery.exportConfiguration, { retry: false, gcTime: 0, meta: opening?.mutationMeta });
  const previewRead = useMutation(ConfigurationQuery.previewConfigurationImport, { retry: false, gcTime: 0, meta: opening?.mutationMeta });
  useEffect(() => { alive.current = true; return () => { alive.current = false; generation.current++; }; }, []);
  const mutation = useRetainedMutation("configuration-import", ConfigurationQuery.applyConfigurationImport, (result) => {
    setReport({ state: "unknown" });
    const value = object(JSON.parse(decoder.decode(result.resultJson)));
    if (!canonicalId.test(text(value.job_id)) || !Object.values(JobState).includes(value.state as JobState)) { setReport({ state: "unknown" }); return; }
    setReport(value);
    void queryClient.invalidateQueries({ refetchType: "active" });
  });
  const jobId = text(report?.job_id);
  const job = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: jobId }, { enabled: active && Boolean(jobId), gcTime: 0, refetchInterval: (query) => active && ![JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(text(document(query.state.data?.resource).state) as JobState) ? 2000 : false });
  const jobValue = job.data?.resource?.id === jobId && job.data.resource.kind === EntityKind.JOB ? document(job.data.resource) : undefined;
  const reportedState = text(report?.state);
  const state = [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(reportedState as JobState) ? reportedState : text(jobValue?.state) || reportedState;
  const importProblem = object(jobValue?.problem ?? report?.problem);
  const blocked = loading || exportRead.isPending || previewRead.isPending || mutation.busy || mutation.uncertain || Boolean(report) || statusPending || statusFailed || remoteUnsupported;
  useEffect(() => {
    onWorkflowReadyChange?.(Boolean(draft || loaded || preview || report || problem || loading || exportRead.isPending || previewRead.isPending || mutation.busy || mutation.uncertain));
    return () => onWorkflowReadyChange?.(false);
  }, [draft, exportRead.isPending, loaded, loading, mutation.busy, mutation.uncertain, onWorkflowReadyChange, preview, previewRead.isPending, problem, report]);
  useEffect(() => { if (state === JobState.Succeeded) void queryClient.invalidateQueries({ refetchType: "active" }); }, [state, queryClient]);
  const invalidate = () => { generation.current++; setPreview(undefined); setProblem(""); };
  const load = (raw: string) => {
    invalidate(); setLoaded(undefined);
    try { const bundle = readBundle(raw); setLoaded({ raw, bundle }); setBindings({}); setMachines({}); setPaths({}); }
    catch (error) { setProblem(productError(error, "configuration-transfer.extra.33bf406051e7")); }
  };
  const exportNow = async () => {
    if (gate.current || blocked || !active) return;
    gate.current = true; setProblem("");
    try { const result = await exportRead.mutateAsync({}); if (alive.current) { const raw = decoder.decode(result.documentJson); readBundle(raw); setExported(raw); } }
    catch (error) { if (alive.current) setProblem(productError(error, "configuration-transfer.extra.307cfd717ce3")); }
    finally { exportRead.reset(); gate.current = false; }
  };
  const inspect = async () => {
    if (gate.current || blocked || !loaded || !active || remoteUnsupported) return;
    gate.current = true; setProblem(""); setPreview(undefined);
    const original = generation.current;
    try {
      const selectedBindings = loaded.bundle.entries.flatMap((entry) => {
        const binding = bindings[entry.id];
        if (!binding || binding.action === ImportAction.Create) return [];
        if (!binding.target || binding.revision === undefined) throw new ProductError("validation.082d543863cb");
        return [`{"source_id":${JSON.stringify(entry.id)},"action":${JSON.stringify(binding.action)},"target_id":${JSON.stringify(binding.target)},"expected_revision":${binding.revision.toString()}}`];
      });
      const selectedMachines = loaded.bundle.machines.map((machine) => ({ source_id: machine.id, target_id: machines[machine.id] ?? "" }));
      const checkouts = loaded.bundle.entries.filter((entry) => entry.kind === "repository").flatMap((entry) => items(entry.document.checkouts).map(object).map((checkout) => ({ repository_id: entry.id, machine_id: text(checkout.machine_id), path: paths[`${entry.id}:${text(checkout.machine_id)}`] ?? "" })));
      const selection = `{"bundle":${loaded.raw},"bindings":[${selectedBindings.join(",")}],"machines":${JSON.stringify(selectedMachines)},"checkouts":${JSON.stringify(checkouts)}}`;
      const result = await previewRead.mutateAsync({ selectionJson: encoder.encode(selection) });
      if (alive.current && generation.current === original) setPreview(readPreview(result.previewJson));
    } catch (error) { if (alive.current && generation.current === original) setProblem(productError(error, "configuration-transfer.extra.8f5998bb6b15")); }
    finally { previewRead.reset(); gate.current = false; }
  };
  const stage = preview || report || mutation.uncertain ? TransferStage.Review : loaded ? TransferStage.Map : TransferStage.Load;
  return <section className="configuration-transfer" aria-label={copy("configuration-transfer.portableConfiguration_ca3e8a")}>
    {showCategoryIntro ? <header className="transfer-intro"><div><h1 aria-live="polite" aria-atomic="true">{copy("configuration-transfer.importExport_6e061f")}</h1><p>{copy("configuration-transfer.moveConfigurationBetweenDelidevServers_749ed7")}</p></div></header> : null}
    <section className="transfer-panel" aria-labelledby="transfer-export-heading">
      <header className="transfer-export-header">
        <div><h2 id="transfer-export-heading">{copy("configuration-transfer.exportConfiguration_0657bc")}</h2><p>{copy("configuration-transfer.copyAPortableJsonDocumentFrom_777321")}</p></div>
        <button disabled={blocked || !active} onClick={() => void exportNow()}>{copy("configuration-transfer.exportConfiguration_0657bc")}</button>
      </header>
      <p className="transfer-scope">{copy("configuration-transfer.includesProvidersModelsAccountPreferencesAgent_57f3ec")}</p>
      {exported ? <><label>{copy("configuration-transfer.exportedConfiguration_9e0e54")}<textarea ref={exportText} readOnly value={exported} rows={6} spellCheck={false} /></label><button onClick={() => { exportText.current?.focus(); exportText.current?.select(); }}>{copy("configuration-transfer.selectExportForCopying_2d205e")}</button></> : null}
    </section>
    <section className="transfer-panel" aria-labelledby="transfer-import-heading">
      <h2 id="transfer-import-heading">{copy("configuration-transfer.importConfiguration_8a507f")}</h2>
      <p>{copy("configuration-transfer.chooseAJsonFileOrPaste_3d11c9")}</p>
      <ol className="transfer-stages" aria-label={copy("configuration-transfer.configurationImportStages_9980b1")}>
        {transferStages.map((item) => <li key={item.stage} aria-current={stage === item.stage ? "step" : undefined}>
          <span className="transfer-stage-number" aria-hidden="true">{item.stage}</span><span>{item.label}</span>
          {stage === item.stage ? <span className="transfer-current-stage">{copy("configuration-transfer.currentStage_91766a")}</span> : null}
        </li>)}
      </ol>
      <fieldset disabled={blocked}>
        <legend className="transfer-input-legend">{copy("configuration-transfer.chooseAnExport_270d3d")}</legend>
        <label>{copy("configuration-transfer.configurationFile_f1c216")}<input type="file" accept=".json,application/json" aria-describedby="transfer-file-help" onChange={(event) => {
          const file = event.target.files?.[0]; event.target.value = "";
          if (!file || gate.current) return;
          invalidate(); setLoaded(undefined);
          if (file.size > bundleLimit) { setProblem(ownedMessage("configuration-transfer.extra.0c01933238f8")); return; }
          gate.current = true; setLoading(true);
          void file.arrayBuffer().then((buffer) => { if (alive.current) { const raw = decoder.decode(buffer); setDraft(raw); load(raw); } }).catch(() => { if (alive.current) setProblem(ownedMessage("configuration-transfer.extra.1724f7337bd3")); }).finally(() => { gate.current = false; if (alive.current) setLoading(false); });
        }} /></label>
        <p id="transfer-file-help" className="transfer-helper">{copy("configuration-transfer.jsonUtf8UpTo384_4d57fb")}</p>
        <label>{copy("configuration-transfer.configurationJson_1d1271")}<textarea className="transfer-json-input" value={draft} rows={6} spellCheck={false} placeholder={copy("configuration-transfer.pasteADelidevConfigurationExport_fcd57c")} onChange={(event) => { if (encoder.encode(event.target.value).byteLength > bundleLimit) { setProblem(ownedMessage("configuration-transfer.extra.0c01933238f8")); return; } invalidate(); setLoaded(undefined); setDraft(event.target.value); }} /></label>
        <button className="primary" disabled={!draft} onClick={() => load(draft)}>{copy("configuration-transfer.loadConfigurationDocument_afae0a")}</button>
      </fieldset>
      <p className="transfer-load-guidance">{copy("configuration-transfer.youWillMapResourcesAndReview_44f7cd")}</p>
    </section>
    {loading || exportRead.isPending || previewRead.isPending || mutation.busy ? <p role="status">{loading ? copy("configuration-transfer.readingConfigurationFile_9ae6ad") : exportRead.isPending ? copy("configuration-transfer.exportingConfiguration_340dcb") : previewRead.isPending ? copy("configuration-transfer.loadingConfigurationChangePreview_7826df") : copy("configuration-transfer.sendingConfigurationImportRequest_f2dc3b")}</p> : null}
    {remoteUnsupported ? <p role="status">Update the selected server before importing repositories by URL.</p> : null}
    {status.error ? <Problem error={status.error} /> : null}
    {status.error ? <button type="button" disabled={status.isFetching} onClick={() => void status.refetch()}>Retry server capability check</button> : null}
    {loaded ? <fieldset className="transfer-panel transfer-mapping" disabled={blocked}>
      <legend>{copy("configuration-transfer.mapImportedConfiguration_d0218d")}</legend>
      <p>{copy("configuration-transfer.newEntriesKeepTheirOriginalContents_63b0dc")}</p>
      {loaded.bundle.machines.map((machine) => <section key={machine.id}><p><LocalizedText id="configuration-transfer.sourceMachine_b09a79" components={{ s0: <>{machine.name}</>, s1: <>{machine.os}</>, s2: <>{machine.architecture}</> }} /></p><ResourceChoice label={copy("configuration-transfer.targetFor_183f6f", { v0: machine.name })} kind={EntityKind.MACHINE} value={machines[machine.id] ?? ""} required active={active} disabled={blocked} change={(id) => { invalidate(); setMachines((values) => ({ ...values, [machine.id]: id })); }} /></section>)}
      {loaded.bundle.entries.map((entry) => {
        const binding = bindings[entry.id] ?? { action: ImportAction.Create, target: "" };
        return <section key={entry.id}><h4>{name(entry)} · {entry.kind}</h4>
          {entry.kind === "account" ? <p>{copy("configuration-transfer.aNewDisconnectedAccountWillBe_cd1542")}</p> : <><label>{copy("configuration-transfer.actionFor_428793", { v0: name(entry) })}<select value={binding.action} onChange={(event) => { invalidate(); setBindings((values) => ({ ...values, [entry.id]: { action: event.target.value as ImportAction, target: "" } })); }}><option value={ImportAction.Create}>{copy("configuration-transfer.createNew_9f3df7")}</option><option value={ImportAction.Reuse}>{copy("configuration-transfer.reuseIdenticalExistingEntry_7bdfe4")}</option>{entry.kind === "settings" ? <option value={ImportAction.Replace}>{copy("configuration-transfer.replaceExistingServerPreferences_ff7572")}</option> : null}</select></label>{binding.action !== ImportAction.Create ? <ResourceChoice label={copy("configuration-transfer.existing_35e8bd", { v0: name(entry) })} kind={kinds[entry.kind]!} value={binding.target} required active={active} disabled={blocked} change={(id, _data, resource) => { invalidate(); setBindings((values) => ({ ...values, [entry.id]: { action: binding.action, target: id, revision: resource?.revision } })); }} /> : null}</>}
          {entry.kind === "repository" ? items(entry.document.checkouts).map(object).map((checkout) => {
            const source = text(checkout.machine_id), key = `${entry.id}:${source}`;
            const machine = loaded.bundle.machines.find((value) => value.id === source);
            return <div key={source}><p><LocalizedText id="configuration-transfer.sourceCheckout_dc8c12" components={{ s0: <>{text(checkout.path)}</> }} /></p><TextField label={copy("configuration-transfer.targetCheckoutForOn_27ac75", { v0: name(entry), v1: machine?.name ?? source })} value={paths[key] ?? ""} max={4096} required change={(path) => { invalidate(); setPaths((values) => ({ ...values, [key]: path })); }} /></div>;
          }) : null}
        </section>;
      })}
       <button disabled={!active || blocked || remoteUnsupported} onClick={() => void inspect()}>{copy("configuration-transfer.previewConfigurationChanges_97d7ba")}</button>
    </fieldset> : null}
     {preview ? <section className="transfer-panel" aria-label={copy("configuration-transfer.configurationChangePreview_226628")}><h3>{copy("configuration-transfer.reviewChangesBeforeApplying_294dcf")}</h3><p>{copy("configuration-transfer.newRepositoryPathsMustPassValidation_178644")}</p>{preview.machines.map((machine) => <p key={text(machine.id)}><LocalizedText id="configuration-transfer.targetWorker_aa1b09" components={{ s0: <>{text(machine.name)}</>, s1: <>{text(machine.os)}</>, s2: <>{text(machine.architecture)}</>, s3: <>{text(machine.id)}</> }} /></p>)}{preview.changes.map((change) => <article key={text(change.id)}><h4>{text(change.action)} · {text(object(change.after).name) || text(object(change.after).alias) || copy("configuration-transfer.extra.eba66b2c00bb")} · {text(change.kind)}</h4><p><LocalizedText id="configuration-transfer.target_8daa2b" components={{ s0: <>{text(change.id)}</> }} /></p><p>{change.before ? copy("configuration-transfer.currentAndImportedValuesAreBoth_99d4dd") : copy("configuration-transfer.newValuesAreIncludedInThe_88d082")}</p></article>)}<label>{copy("configuration-transfer.completeChangeDetails_79b757")}<textarea readOnly value={formattedPreview} rows={12} spellCheck={false} /></label><button className="primary" disabled={blocked || !active || remoteUnsupported} onClick={() => { if (!blocked && !remoteUnsupported) void mutation.send({ requestId: newRequestId(), previewJson: preview.bytes }); }}>{copy("configuration-transfer.applyReviewedConfiguration_1229a2")}</button></section> : null}
    {mutation.uncertain ? <section className="transfer-panel" aria-label={copy("configuration-transfer.uncertainConfigurationImport_4b21c6")}><button disabled={mutation.busy || !active} onClick={mutation.retry}>{copy("configuration-transfer.retryTheSameConfigurationImport_9d3291")}</button></section> : null}
    {report ? <section className="transfer-panel" aria-label={copy("configuration-transfer.configurationImportResult_7c1375")}><p role="status">{state === JobState.Succeeded ? copy("configuration-transfer.configurationImportCompletedConnectEachImported_2af629") : state === JobState.Failed || state === JobState.Canceled ? copy("configuration-transfer.configurationImportFailedExistingConfigurationWas_c7e5ad") : state === JobState.Queued || state === JobState.Claimed ? copy("configuration-transfer.importAcceptedWaitingForConfirmationOf_8ed561") : copy("configuration-transfer.theImportOutcomeIsUnavailableInspect_7a2ebb")}</p>{jobId ? <><small>{jobId}</small><button disabled={job.isFetching || !active} onClick={() => void job.refetch()}>{copy("configuration-transfer.refreshConfigurationImport_e42bc3")}</button></> : null}{text(importProblem.message) ? <ServiceProblem code={text(importProblem.code) || text(importProblem.problem_code)}><p role="alert">{text(importProblem.message)} {text(importProblem.guidance)}</p></ServiceProblem> : null}<Problem error={job.error} />{[JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button onClick={() => { setReport(undefined); invalidate(); }}>{copy("configuration-transfer.returnToRetainedImportDocument_9a833a")}</button> : null}</section> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
    <aside className="transfer-guidance" aria-labelledby="transfer-guidance-heading">
      <h2 id="transfer-guidance-heading">{copy("configuration-transfer.beforeYouTransfer_1de385")}</h2>
      <ul><li>{copy("configuration-transfer.importedAccountsAreDisconnectedAndNeed_2a3422")}</li><li>{copy("configuration-transfer.authenticationDeviceRegistrationsObservedQuotasDiscovered_57d141")}</li></ul>
    </aside>
  </section>;
}
