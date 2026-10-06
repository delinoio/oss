import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ResourceQuery, SystemQuery, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { useQueryClient } from "@tanstack/react-query";
import { document, items, object, text, type Document } from "./documents";
import { ResourceChoice, TextField } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { useSettingsOpening } from "./settings-lifetime";
import { JobState } from "./jobs";
import { Problem } from "./ui";
import "./configuration-transfer.css";

enum TransferStage { Load = 1, Map = 2, Review = 3 }
const transferStages = [
  { stage: TransferStage.Load, label: "Load document" },
  { stage: TransferStage.Map, label: "Map configuration" },
  { stage: TransferStage.Review, label: "Review & apply" },
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
const name = (entry: Entry) => text(entry.document.name) || text(entry.document.alias) || "Server preferences";
function readBundle(raw: string): Bundle {
  if (encoder.encode(raw).byteLength > bundleLimit) throw new Error("Use an export of at most 384 KiB.");
  const value = object(JSON.parse(raw));
  if (![1, 2].includes(value.version as number) || !Array.isArray(value.entries) || !Array.isArray(value.machines) || value.entries.length > 256 || value.machines.length > 64) throw new Error("Use a version 1 API-only or version 2 DeliDev configuration export.");
  const ids = new Set<string>();
  for (const item of value.entries) {
    const entry = object(item), id = text(entry.id);
    if (!canonicalId.test(id) || ids.has(id) || !Object.hasOwn(kinds, text(entry.kind)) || !entry.document || typeof entry.document !== "object" || Array.isArray(entry.document)) throw new Error("The exported entries are not readable. Validate the original document before continuing.");
    const data = object(entry.document);
    if (value.version === 1 && (data.type === "subscription" || data.source_kind === "subscription" || data.protocol === "native-subscription" || data.subscription_service !== undefined)) throw new Error("Service-native subscription configuration requires a version 2 export. Version 1 imports support API configuration only.");
    ids.add(id);
  }
  for (const item of value.machines) {
    const machine = object(item), id = text(machine.id);
    if (!canonicalId.test(id) || ids.has(id) || !text(machine.name) || !["darwin", "linux", "windows"].includes(text(machine.os)) || !["arm64", "amd64"].includes(text(machine.architecture))) throw new Error("The exported machine references are not readable.");
    ids.add(id);
  }
  let checkouts = 0;
  for (const item of value.entries) {
    const entry = object(item), data = object(entry.document);
    if (entry.kind !== "repository") continue;
    if (!Array.isArray(data.checkouts)) throw new Error("Repository checkouts must be an explicit list.");
    checkouts += data.checkouts.length;
    if (checkouts > 64) throw new Error("Use at most 64 repository checkouts.");
    const seen = new Set<string>();
    for (const checkout of data.checkouts) {
      const id = text(object(checkout).machine_id);
      if (!canonicalId.test(id) || seen.has(id)) throw new Error("Repository checkout machines must be distinct references.");
      seen.add(id);
    }
  }
  return value as unknown as Bundle;
}
function readPreview(bytes: Uint8Array): Preview {
  if (bytes.byteLength > 1 << 20) throw new Error("The change preview exceeds its limit.");
  const value = object(JSON.parse(decoder.decode(bytes))), plan = object(value.plan);
  if (!text(value.token) || ![1, 2].includes(plan.version as number) || !Array.isArray(plan.changes) || !plan.changes.length || plan.changes.length > 256 || !Array.isArray(plan.machines)) throw new Error("The server did not return a readable change preview.");
  for (const item of plan.changes) {
    const change = object(item);
    if (!canonicalId.test(text(change.id)) || !canonicalId.test(text(change.source_id)) || !Object.hasOwn(kinds, text(change.kind)) || !Object.values(ImportAction).includes(change.action as ImportAction) || !change.after || typeof change.after !== "object" || Array.isArray(change.after)) throw new Error("The server change preview is incomplete.");
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
  const [exported, setExported] = useState("");
  const [draft, setDraft] = useState("");
  const [loaded, setLoaded] = useState<{ raw: string; bundle: Bundle }>();
  const [bindings, setBindings] = useState<Record<string, Binding>>({});
  const [machines, setMachines] = useState<Record<string, string>>({});
  const [paths, setPaths] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview>();
  const [report, setReport] = useState<Document>();
  const formattedPreview = useMemo(() => preview ? formatConfigurationReview(decoder.decode(preview.bytes)) : "", [preview]);
  const [problem, setProblem] = useState("");
  const [loading, setLoading] = useState(false);
  const alive = useRef(false), generation = useRef(0), gate = useRef(false);
  const exportText = useRef<HTMLTextAreaElement>(null);
  const queryClient = useQueryClient();
  const opening = useSettingsOpening();
  // Legacy exports contain checkout-backed repositories without the URL-only
  // field. Those documents use the pre-capability save/import contract and
  // must remain available on older servers; only the new remote shape needs
  // capability 37.
  const containsRemoteRepositories = loaded?.bundle.entries.some(entry => entry.kind === "repository" && Object.hasOwn(entry.document, "remote_url")) === true;
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && containsRemoteRepositories, retry: false });
  // Unsupported is a successful status response without capability 37. A
  // failed status read is a separate recoverable condition and must expose its
  // error/retry path instead of presenting misleading upgrade guidance.
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
    catch (error) { setProblem(error instanceof Error ? error.message : "The configuration document could not be read."); }
  };
  const exportNow = async () => {
    if (gate.current || blocked || !active) return;
    gate.current = true; setProblem("");
    try { const result = await exportRead.mutateAsync({}); if (alive.current) { const raw = decoder.decode(result.documentJson); readBundle(raw); setExported(raw); } }
    catch (error) { if (alive.current) setProblem(error instanceof Error ? error.message : "Export failed."); }
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
        if (!binding.target || binding.revision === undefined) throw new Error("Select each existing entry explicitly before previewing.");
        return [`{"source_id":${JSON.stringify(entry.id)},"action":${JSON.stringify(binding.action)},"target_id":${JSON.stringify(binding.target)},"expected_revision":${binding.revision.toString()}}`];
      });
      const selectedMachines = loaded.bundle.machines.map((machine) => ({ source_id: machine.id, target_id: machines[machine.id] ?? "" }));
      const checkouts = loaded.bundle.entries.filter((entry) => entry.kind === "repository").flatMap((entry) => items(entry.document.checkouts).map(object).map((checkout) => ({ repository_id: entry.id, machine_id: text(checkout.machine_id), path: paths[`${entry.id}:${text(checkout.machine_id)}`] ?? "" })));
      const selection = `{"bundle":${loaded.raw},"bindings":[${selectedBindings.join(",")}],"machines":${JSON.stringify(selectedMachines)},"checkouts":${JSON.stringify(checkouts)}}`;
      const result = await previewRead.mutateAsync({ selectionJson: encoder.encode(selection) });
      if (alive.current && generation.current === original) setPreview(readPreview(result.previewJson));
    } catch (error) { if (alive.current && generation.current === original) setProblem(error instanceof Error ? error.message : "Preview failed."); }
    finally { previewRead.reset(); gate.current = false; }
  };
  const stage = preview || report || mutation.uncertain ? TransferStage.Review : loaded ? TransferStage.Map : TransferStage.Load;
  return <section className="configuration-transfer" aria-label="Portable configuration">
    {showCategoryIntro ? <header className="transfer-intro"><div><h1 aria-live="polite" aria-atomic="true">Import / Export</h1><p>Move configuration between DeliDev servers.</p></div></header> : null}
    <section className="transfer-panel" aria-labelledby="transfer-export-heading">
      <header className="transfer-export-header">
        <div><h2 id="transfer-export-heading">Export configuration</h2><p>Copy a portable JSON document from the selected server.</p></div>
        <button disabled={blocked || !active} onClick={() => void exportNow()}>Export configuration</button>
      </header>
      <p className="transfer-scope">Includes providers, models, account preferences, Agent Workers, instructions, repositories, projects and server preferences.</p>
      {exported ? <><label>Exported configuration<textarea ref={exportText} readOnly value={exported} rows={6} spellCheck={false} /></label><button onClick={() => { exportText.current?.focus(); exportText.current?.select(); }}>Select export for copying</button></> : null}
    </section>
    <section className="transfer-panel" aria-labelledby="transfer-import-heading">
      <h2 id="transfer-import-heading">Import configuration</h2>
      <p>Choose a JSON file or paste an exported configuration.</p>
      <ol className="transfer-stages" aria-label="Configuration import stages">
        {transferStages.map((item) => <li key={item.stage} aria-current={stage === item.stage ? "step" : undefined}>
          <span className="transfer-stage-number" aria-hidden="true">{item.stage}</span><span>{item.label}</span>
          {stage === item.stage ? <span className="transfer-current-stage">Current stage</span> : null}
        </li>)}
      </ol>
      <fieldset disabled={blocked}>
        <legend className="transfer-input-legend">Choose an export</legend>
        <label>Configuration file<input type="file" accept=".json,application/json" aria-describedby="transfer-file-help" onChange={(event) => {
          const file = event.target.files?.[0]; event.target.value = "";
          if (!file || gate.current) return;
          invalidate(); setLoaded(undefined);
          if (file.size > bundleLimit) { setProblem("Use an export of at most 384 KiB."); return; }
          gate.current = true; setLoading(true);
          void file.arrayBuffer().then((buffer) => { if (alive.current) { const raw = decoder.decode(buffer); setDraft(raw); load(raw); } }).catch(() => { if (alive.current) setProblem("The configuration file could not be read as UTF-8."); }).finally(() => { gate.current = false; if (alive.current) setLoading(false); });
        }} /></label>
        <p id="transfer-file-help" className="transfer-helper">JSON · UTF-8 · Up to 384 KiB</p>
        <label>Configuration JSON<textarea className="transfer-json-input" value={draft} rows={6} spellCheck={false} placeholder="Paste a DeliDev configuration export…" onChange={(event) => { if (encoder.encode(event.target.value).byteLength > bundleLimit) { setProblem("Use an export of at most 384 KiB."); return; } invalidate(); setLoaded(undefined); setDraft(event.target.value); }} /></label>
        <button className="primary" disabled={!draft} onClick={() => load(draft)}>Load configuration document</button>
      </fieldset>
      <p className="transfer-load-guidance">You will map resources and review changes before applying.</p>
    </section>
    {loading || exportRead.isPending || previewRead.isPending || mutation.busy ? <p role="status">{loading ? "Reading configuration file…" : exportRead.isPending ? "Exporting configuration…" : previewRead.isPending ? "Loading configuration change preview…" : "Sending configuration import request…"}</p> : null}
    {remoteUnsupported ? <p role="status">Update the selected server before importing repositories by URL.</p> : null}
    {status.error ? <Problem error={status.error} /> : null}
    {status.error ? <button type="button" disabled={status.isFetching} onClick={() => void status.refetch()}>Retry server capability check</button> : null}
    {loaded ? <fieldset className="transfer-panel transfer-mapping" disabled={blocked}>
      <legend>Map imported configuration</legend>
      <p>New entries keep their original contents and relationships. Reuse requires identical values after mapping. Only server preferences can replace an existing entry, and replacement requires its current revision.</p>
      {loaded.bundle.machines.map((machine) => <section key={machine.id}><p>Source machine: {machine.name} · {machine.os} / {machine.architecture}</p><ResourceChoice label={`Target for ${machine.name}`} kind={EntityKind.MACHINE} value={machines[machine.id] ?? ""} required active={active} disabled={blocked} change={(id) => { invalidate(); setMachines((values) => ({ ...values, [machine.id]: id })); }} /></section>)}
      {loaded.bundle.entries.map((entry) => {
        const binding = bindings[entry.id] ?? { action: ImportAction.Create, target: "" };
        return <section key={entry.id}><h4>{name(entry)} · {entry.kind}</h4>
          {entry.kind === "account" ? <p>A new, disconnected account will be created. Authentication must be configured again.</p> : <><label>{`Action for ${name(entry)}`}<select value={binding.action} onChange={(event) => { invalidate(); setBindings((values) => ({ ...values, [entry.id]: { action: event.target.value as ImportAction, target: "" } })); }}><option value={ImportAction.Create}>Create new</option><option value={ImportAction.Reuse}>Reuse identical existing entry</option>{entry.kind === "settings" ? <option value={ImportAction.Replace}>Replace existing server preferences</option> : null}</select></label>{binding.action !== ImportAction.Create ? <ResourceChoice label={`Existing ${name(entry)}`} kind={kinds[entry.kind]!} value={binding.target} required active={active} disabled={blocked} change={(id, _data, resource) => { invalidate(); setBindings((values) => ({ ...values, [entry.id]: { action: binding.action, target: id, revision: resource?.revision } })); }} /> : null}</>}
          {entry.kind === "repository" ? items(entry.document.checkouts).map(object).map((checkout) => {
            const source = text(checkout.machine_id), key = `${entry.id}:${source}`;
            const machine = loaded.bundle.machines.find((value) => value.id === source);
            return <div key={source}><p>Source checkout: {text(checkout.path)}</p><TextField label={`Target checkout for ${name(entry)} on ${machine?.name ?? source}`} value={paths[key] ?? ""} max={4096} required change={(path) => { invalidate(); setPaths((values) => ({ ...values, [key]: path })); }} /></div>;
          }) : null}
        </section>;
      })}
      <button disabled={!active || blocked || remoteUnsupported} onClick={() => void inspect()}>Preview configuration changes</button>
    </fieldset> : null}
    {preview ? <section className="transfer-panel" aria-label="Configuration change preview"><h3>Review changes before applying</h3><p>New repository paths must pass validation on every selected Worker before any settings are applied. Conflicts preserve existing configuration. Review full access permissions, provider endpoints and instruction contents below.</p>{preview.machines.map((machine) => <p key={text(machine.id)}>Target Worker: {text(machine.name)} · {text(machine.os)} / {text(machine.architecture)} · {text(machine.id)}</p>)}{preview.changes.map((change) => <article key={text(change.id)}><h4>{text(change.action)} · {text(object(change.after).name) || text(object(change.after).alias) || "Server preferences"} · {text(change.kind)}</h4><p>Target: {text(change.id)}</p><p>{change.before ? "Current and imported values are both included in the complete change details." : "New values are included in the complete change details."}</p></article>)}<label>Complete change details<textarea readOnly value={formattedPreview} rows={12} spellCheck={false} /></label><button className="primary" disabled={blocked || !active || remoteUnsupported} onClick={() => { if (!blocked && !remoteUnsupported) void mutation.send({ requestId: newRequestId(), previewJson: preview.bytes }); }}>Apply reviewed configuration</button></section> : null}
    {mutation.uncertain ? <section className="transfer-panel" aria-label="Uncertain configuration import"><button disabled={mutation.busy || !active} onClick={mutation.retry}>Retry the same configuration import</button></section> : null}
    {report ? <section className="transfer-panel" aria-label="Configuration import result"><p role="status">{state === JobState.Succeeded ? "Configuration import completed. Connect each imported account before execution." : state === JobState.Failed || state === JobState.Canceled ? "Configuration import failed. Existing configuration was preserved." : state === JobState.Queued || state === JobState.Claimed ? "Import accepted. Waiting for confirmation of every repository validation." : "The import outcome is unavailable. Inspect the original operation before trying another import."}</p>{jobId ? <><small>{jobId}</small><button disabled={job.isFetching || !active} onClick={() => void job.refetch()}>Refresh configuration import</button></> : null}{text(importProblem.message) ? <p role="alert">{text(importProblem.message)} {text(importProblem.guidance)}</p> : null}<Problem error={job.error} />{[JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button onClick={() => { setReport(undefined); invalidate(); }}>Return to retained import document</button> : null}</section> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
    <aside className="transfer-guidance" aria-labelledby="transfer-guidance-heading">
      <h2 id="transfer-guidance-heading">Before you transfer</h2>
      <ul><li>Imported accounts are disconnected and need a new connection.</li><li>Authentication, device registrations, observed quotas, discovered model evidence and session history are excluded.</li></ul>
    </aside>
  </section>;
}
