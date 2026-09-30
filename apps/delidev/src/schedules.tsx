import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, ScheduleAction, ScheduleQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, Mode, object, text, Workspace, type Document } from "./documents";
import { ReferenceFields, ResourceChoice, TextField } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import { Icon as SidebarIcon } from "./sidebar";
import { ScheduleCreation, type ScheduleCreationProps } from "./schedule-creation";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";

enum Overlap { Overlap = "overlap", Skip = "skip", Wait = "wait" }
enum EnabledFilter { All = "all", Enabled = "enabled", Paused = "paused" }
const ignoreProtectedChange = (_protectedState: boolean) => undefined;
const scheduleName = (row?: Resource) => text(object(document(row).definition).name) || "Unnamed schedule";
const emptyDefinition = (): Document => ({ name: "", prompt: "", enabled: false, project_id: "", agent_id: "", machine_id: "", workspace: Workspace.Worktree, mode: Mode.Execute, cron: "0 9 * * 1-5", timezone: "UTC", overlap: Overlap.Overlap });

export function StartingReferences({ project, starting, change, active }: { project: string; starting: unknown[]; change: (value: unknown[]) => void; active: boolean }) {
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: project }, { enabled: active && Boolean(project) });
  const [selected, setSelected] = useState("");
  const references = starting.map(object);
  return <fieldset><legend>Starting reference overrides</legend><p>Omitted repositories use their saved starting reference. Base comparison references stay unchanged.</p><label>Add repository override<select value={selected} onChange={(event) => setSelected(event.target.value)}><option value="">Select a project repository</option>{items(document(result.data?.resource).repositories).map(text).filter((id) => !references.some((row) => row.repository_id === id)).map((id) => <option key={id} value={id}>{id}</option>)}</select></label><button type="button" disabled={!selected || references.length >= 1000} onClick={() => { change([...starting, { repository_id: selected, reference: { type: "local-branch", name: "" } }]); setSelected(""); }}>Add starting override</button><Problem error={result.error} />{references.map((row) => <fieldset key={text(row.repository_id)}><legend>{text(row.repository_id)}</legend><ReferenceFields label={`Starting ${text(row.repository_id)}`} value={row.reference} change={(reference) => change(reference.type ? starting.map((value) => object(value).repository_id === row.repository_id ? { ...row, reference } : value) : starting.filter((value) => object(value).repository_id !== row.repository_id))} /><button type="button" onClick={() => change(starting.filter((value) => object(value).repository_id !== row.repository_id))}>Remove starting override</button></fieldset>)}</fieldset>;
}

export function ScheduleEditor({ initial, active, saved, cancel, readLocalWorker, protectedChange = ignoreProtectedChange }: { initial?: Resource; active: boolean; saved: (resource?: Resource) => void; cancel: () => void; readLocalWorker?: ReadLocalWorkerProof; protectedChange?: (protectedState: boolean) => void }) {
  const localProof = useLocalWorkerProof(readLocalWorker);
  const [definition, setDefinition] = useState<Document>(() => initial ? object(document(initial).definition) : emptyDefinition());
  const [limit, setLimit] = useState("");
  const current = useQuery(ScheduleQuery.getSchedule, { id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`schedule-save:${initial?.id ?? "new"}`, ScheduleQuery.saveSchedule, (response) => saved(response.schedule));
  const blocked = mutation.busy || mutation.uncertain || localProof.busy;
  useEffect(() => protectedChange(true), [protectedChange]);
  const stale = Boolean(initial && current.data?.schedule && initial.revision !== current.data.schedule.revision);
  const local = definition.workspace === Workspace.Local;
  const change = (next: Document) => {
    if (encode(next).byteLength > 1 << 20 || new TextEncoder().encode(text(next.prompt)).byteLength > 256 << 10) { setLimit("The schedule or prompt is too large. The previous draft is retained."); return false; }
    setDefinition(next); setLimit(""); return true;
  };
  const field = (key: string) => (value: unknown) => change({ ...definition, [key]: value });
  const submit = async () => {
    if (blocked || stale || (initial && current.error)) return;
    const original = object(document(initial).definition);
    const retainedLocal = local && original.workspace === Workspace.Local && original.machine_id === definition.machine_id;
    const input = { mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, definitionJson: encode(definition) };
    const proof = local && !retainedLocal ? await localProof.load(text(definition.machine_id)) : undefined;
    if (local && !retainedLocal && !proof) return;
    void mutation.send({ ...input, localWorkerToken: proof?.token });
  };
  if (!initial) {
    const props: ScheduleCreationProps = { definition, change, active, blocked, cancel, submit,
      localAvailable: localProof.available,
      selectLocal: () => { void localProof.load().then((proof) => { if (proof) change({ ...definition, workspace: Workspace.Local, machine_id: proof.machineId, starting: [] }); }); },
      references: local ? null : <StartingReferences key={text(definition.project_id)} project={text(definition.project_id)} starting={items(definition.starting)} change={field("starting")} active={active} />,
      errors: <>{limit ? <p role="alert">{limit}</p> : null}{localProof.problem ? <p role="alert">{localProof.problem}</p> : null}<Problem error={mutation.error} /></>,
      retry: mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same schedule</button> : null };
    return <ScheduleCreation {...props} />;
  }
  return <section><h3>{initial ? "Edit schedule" : "New schedule"}</h3><form onSubmit={(event) => { event.preventDefault(); void submit(); }}><fieldset disabled={blocked}>
    <TextField label="Schedule name" value={definition.name} required change={field("name")} /><label className="checkbox"><input type="checkbox" checked={definition.enabled === true} onChange={(event) => field("enabled")(event.target.checked)} />Enable future scheduled runs</label><ResourceChoice label="Project" kind={EntityKind.PROJECT} value={text(definition.project_id)} active={active} required change={(project_id) => setDefinition({ ...definition, project_id, starting: [] })} /><ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={text(definition.agent_id)} active={active} required change={field("agent_id")} /><ResourceChoice label="Execution Worker" kind={EntityKind.MACHINE} value={text(definition.machine_id)} active={active} disabled={local} required change={field("machine_id")} />
    <div className="actions"><button type="button" aria-pressed={!local} onClick={() => setDefinition({ ...definition, workspace: Workspace.Worktree })}>Use separate Worktrees</button><button type="button" disabled={!localProof.available} aria-pressed={local} onClick={() => { void localProof.load().then((proof) => { if (proof) setDefinition({ ...definition, workspace: Workspace.Local, machine_id: proof.machineId, starting: [] }); }); }}>Use this computer's Local checkouts</button></div><p>Workspace: {local ? "Local computer · originating Worker selected" : "Worktree · separate detached checkouts"}</p>{local ? <p>Existing Local schedules retain their authenticated Worker when unchanged. Selecting this computer explicitly supplies its private Worker proof. Existing checkouts are shared as-is without fetch or starting-reference overrides.</p> : <StartingReferences key={text(definition.project_id)} project={text(definition.project_id)} starting={items(definition.starting)} change={field("starting")} active={active} />}
    <label>Execution mode<select value={text(definition.mode)} onChange={(event) => field("mode")(event.target.value)}>{Object.values(Mode).map((mode) => <option key={mode} value={mode}>{mode}</option>)}</select></label><label>Scheduled prompt<textarea required rows={6} maxLength={262144} value={text(definition.prompt)} onChange={(event) => field("prompt")(event.target.value)} /></label><TextField label="Cron expression" value={definition.cron} required max={512} change={field("cron")} /><p>Five fields: minute, hour, day of month, month, weekday. The server validates the calendar and computes the next UTC run.</p><TextField label="IANA timezone" value={definition.timezone} required change={field("timezone")} /><label>When a previous occurrence is still active<select value={text(definition.overlap)} onChange={(event) => field("overlap")(event.target.value)}><option value={Overlap.Overlap}>Overlap · independent sessions</option><option value={Overlap.Skip}>Skip the new occurrence</option><option value={Overlap.Wait}>Wait in FIFO order for confirmed cleanup</option></select></label><p>The server continues scheduling when the desktop closes. Offline due times are recorded as skipped; no missed-run burst is created.</p></fieldset>
    {stale ? <p role="alert">This schedule changed elsewhere. Your draft is retained; reopen its latest revision before saving.</p> : null}{limit ? <p role="alert">{limit}</p> : null}{localProof.problem ? <p role="alert">{localProof.problem}</p> : null}<Problem error={current.error || mutation.error} /><div className="actions"><button className="primary" disabled={blocked || stale || Boolean(initial && current.error)}>Save schedule</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same schedule</button> : null}<button type="button" disabled={blocked} onClick={cancel}>Cancel schedule edit</button></div></form></section>;
}

function Occurrence({ resource, open }: { resource: Resource; open: (id: string) => void }) {
  const data = document(resource), selection = object(data.selection), problem = object(data.problem);
  return <article className="result"><h4>{text(data.trigger)} · {text(data.state)}</h4><p>Due: {text(data.due_at)} · Accepted: {text(data.accepted_at)}</p>{text(data.reason) ? <p>Reason: {text(data.reason)}</p> : null}{text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}{text(data.session_id) ? <button onClick={() => open(text(data.session_id))}>Open occurrence session</button> : <p>No session has been created for this occurrence.</p>}<details><summary>Original accepted selection</summary><p>{text(selection.name)} · {text(selection.workspace)} · {text(selection.mode)}</p><p>Agent {text(selection.agent_id)} · Worker {text(selection.machine_id)}</p><p>Overlap: {text(data.overlap)}</p><pre>{text(selection.prompt)}</pre><small>{resource.id}</small></details></article>;
}
export function ScheduleHistory({ id, active, open }: { id: string; active: boolean; open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const result = useQuery(ScheduleQuery.listScheduleOccurrences, { scheduleId: id, pageSize: 50, pageToken: page }, { enabled: active, refetchInterval: active && !page ? 5000 : false });
  return <section><header><h3>Occurrence history</h3><button onClick={() => { setPage(""); void result.refetch(); }}>Refresh history</button></header><p>History remains available after schedule configuration deletion. Occurrence completion and the current session state are separate.</p><Problem error={result.error} />{result.data?.occurrences.map((row) => <Occurrence resource={row} key={row.id} open={open} />)}{result.data?.occurrences.length === 0 ? <p>No retained occurrences.</p> : null}<nav aria-label="Schedule history pages"><button disabled={!page || result.isFetching} onClick={() => setPage("")}>First history page</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next history page</button></nav></section>;
}

export function ScheduleDetails({ initial, active, open, close, edit, protectedChange = ignoreProtectedChange }: { initial: Resource; active: boolean; open: (id: string) => void; close: () => void; edit: (row: Resource) => void; protectedChange?: (protectedState: boolean) => void }) {
  const [deleted, setDeleted] = useState(false), [confirm, setConfirm] = useState<"delete" | "run">();
  const [acknowledged, setAcknowledged] = useState<Resource>(), [occurrence, setOccurrence] = useState<Resource>();
  const occurrenceResult = useQuery(ScheduleQuery.getScheduleOccurrence, { scheduleId: initial.id, id: occurrence?.id ?? "" }, { enabled: active && Boolean(occurrence), refetchInterval: active && occurrence ? 5000 : false });
  const result = useQuery(ScheduleQuery.getSchedule, { id: initial.id }, { enabled: active && !deleted, refetchInterval: active && !deleted ? 5000 : false });
  const current = [initial, result.data?.schedule, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current), definition = object(data.definition), problem = object(data.problem);
  const control = useRetainedMutation(`schedule-control:${initial.id}`, ScheduleQuery.controlSchedule, (response) => { setAcknowledged(response.schedule); void result.refetch(); });
  const remove = useRetainedMutation(`schedule-delete:${initial.id}`, ScheduleQuery.deleteSchedule, () => { setDeleted(true); setConfirm(undefined); });
  const run = useRetainedMutation(`schedule-run:${initial.id}`, ScheduleQuery.runScheduleNow, (response) => { setOccurrence(response.occurrence); setConfirm(undefined); void result.refetch(); });
  const operations = [control, remove, run], blocked = operations.some((operation) => operation.busy || operation.uncertain);
  useEffect(() => { protectedChange(blocked || Boolean(confirm)); }, [blocked, confirm, protectedChange]);
  const mutation = () => ({ id: initial.id, expectedRevision: current.revision, requestId: newRequestId() });
  return <section><header><h2>{scheduleName(current)}</h2><button disabled={blocked || Boolean(confirm)} onClick={close}>Back to schedules</button></header><small>{initial.id}</small>{deleted ? <p>Schedule configuration deleted. Retained occurrences and sessions remain.</p> : <><p>{definition.enabled === true ? "Enabled" : "Paused"} · {text(definition.cron)} · {text(definition.timezone)}</p><p>Next run (UTC): {text(data.next_run_at) || "None scheduled"}</p><p>Overlap policy: {text(definition.overlap)}</p>{text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}<div className="actions"><button disabled={blocked || current.schemaVersion !== 1} onClick={() => edit(current)}>Edit schedule</button><button disabled={blocked} onClick={() => void control.send({ mutation: mutation(), action: definition.enabled === true ? ScheduleAction.PAUSE : ScheduleAction.RESUME })}>{definition.enabled === true ? "Pause future runs" : "Resume future runs"}</button><button disabled={blocked} onClick={() => setConfirm("run")}>Run now</button><button disabled={blocked} onClick={() => setConfirm("delete")}>Delete schedule</button></div>{confirm ? <div className="notice"><p>{confirm === "run" ? "Accept one independent occurrence now using the configured overlap policy, even while future runs are paused. Current execution eligibility still applies." : "Delete future scheduling configuration. Already accepted sessions and retained occurrence history remain."}</p><button disabled={blocked} onClick={() => confirm === "run" ? void run.send({ mutation: mutation() }) : void remove.send({ mutation: mutation() })}>{confirm === "run" ? "Confirm Run now" : "Confirm schedule deletion"}</button><button disabled={blocked} onClick={() => setConfirm(undefined)}>Cancel</button></div> : null}<Problem error={result.error} /></>}
    {operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}>Retry the same {index === 0 ? "schedule control" : index === 1 ? "schedule deletion" : "Run now"}</button> : null}</div>)}{occurrence ? <section><h3>Run now accepted</h3><Problem error={occurrenceResult.error} /><Occurrence resource={occurrenceResult.data?.occurrence && occurrenceResult.data.occurrence.revision >= occurrence.revision ? occurrenceResult.data.occurrence : occurrence} open={open} /></section> : null}<ScheduleHistory id={initial.id} active={active} open={open} />
  </section>;
}

export function Schedules({ active, open, readLocalWorker }: { active: boolean; open: (id: string) => void; readLocalWorker?: ReadLocalWorkerProof }) {
  const [page, setPage] = useState(""), [filter, setFilter] = useState(EnabledFilter.All);
  const [selected, setSelected] = useState<Resource>(), [editing, setEditing] = useState<{ initial?: Resource; key: string }>();
  const [historyDraft, setHistoryDraft] = useState(""), [history, setHistory] = useState("");
  const [projectId, setProjectId] = useState("");
  const [historyExpanded, setHistoryExpanded] = useState(false);
  const historyRegionId = useId();
  const historyInput = useRef<HTMLInputElement>(null);
  const focusHistoryOnExpansion = useRef(false);
  const [protectedWorkflow, setProtectedWorkflow] = useState(false);
  const closeDrawer = useCloseSidebarDrawer();
  const locked = protectedWorkflow || Boolean(editing);
  useEffect(() => {
    // Only an explicit disclosure expansion owns focus; navigation, reconnects
    // and portal placement must leave the user's current focus untouched.
    if (!focusHistoryOnExpansion.current) return;
    focusHistoryOnExpansion.current = false;
    if (historyExpanded && active && !locked) historyInput.current?.focus();
  }, [historyExpanded, active, locked]);
  const result = useQuery(ScheduleQuery.listSchedules, { projectId, pageSize: 50, pageToken: page, ...(filter === EnabledFilter.All ? {} : { enabled: filter === EnabledFilter.Enabled }) }, { enabled: active });
  const selectSchedule = (row: Resource) => { if (locked) return; setSelected(row); setHistory(""); closeDrawer(); };
  const newSchedule = () => { if (locked) return; setSelected(undefined); setHistory(""); setEditing({ key: newRequestId() }); closeDrawer(); };
  const openHistory = (event: FormEvent) => { event.preventDefault(); if (locked || !historyDraft.trim()) return; setSelected(undefined); setHistory(historyDraft.trim()); closeDrawer(); };
  return <>
    <SidebarSurface active={active} title="Schedules" className="schedules-sidebar">
      <button className="primary sidebar-action" disabled={locked} onClick={newSchedule}><SidebarIcon name="plus" />New schedule</button>
      <div className="sidebar-filter-options" aria-label="Schedule state">{([[EnabledFilter.All, "All schedules"], [EnabledFilter.Enabled, "Enabled"], [EnabledFilter.Paused, "Paused"]] as const).map(([value, label]) => <button key={value} type="button" aria-pressed={filter === value} disabled={locked} onClick={() => { setFilter(value); setPage(""); }}>{label}</button>)}</div>
      <ResourceChoice label="Filter by project" emptyLabel="All projects" kind={EntityKind.PROJECT} value={projectId} change={(id) => { if (!locked) { setProjectId(id); setPage(""); } }} active={active} disabled={locked} />
      <header className="sidebar-list-heading"><h3>Saved schedules</h3><button type="button" disabled={locked || result.isFetching} onClick={() => { setPage(""); void result.refetch(); }}><SidebarIcon name="refresh" />Refresh</button></header>
      <Problem error={result.error} />{result.isPending && active ? <p role="status">Loading schedules…</p> : null}{result.error && result.data ? <p className="sidebar-help">Refresh failed. Showing the previous page.</p> : null}
      {result.data?.schedules.map((row) => <article key={row.id} className="sidebar-schedule-row"><button type="button" disabled={locked} aria-current={selected?.id === row.id ? "true" : undefined} onClick={() => selectSchedule(row)}><span className="schedule-row-title">{scheduleName(row)}</span><span className="schedule-row-state">{object(document(row).definition).enabled === true ? "Enabled" : "Paused"}</span><span className="schedule-row-next">Next run (UTC): <span>{text(document(row).next_run_at) || "None scheduled"}</span></span></button></article>)}
      {!result.error && result.data?.schedules.length === 0 ? <p className="sidebar-help schedules-empty"><SidebarIcon name="schedules" />{page ? "No schedules on this page." : "No saved schedules."}</p> : null}
      <nav className="schedules-pages" aria-label="Schedules pages"><button disabled={locked || !page || result.isFetching} onClick={() => setPage("")}>First</button><button disabled={locked || !result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next</button></nav>
      <button type="button" className="schedules-history-toggle" aria-expanded={historyExpanded} aria-controls={historyRegionId} disabled={locked} onClick={() => { if (locked) return; focusHistoryOnExpansion.current = !historyExpanded; setHistoryExpanded(!historyExpanded); }}><SidebarIcon name="chevron" className="schedules-history-chevron" />Retained history</button>
      <div role="region" id={historyRegionId} aria-label="Retained schedule history lookup" hidden={!historyExpanded}>
        <form className="sidebar-form" onSubmit={openHistory}><label>Retained schedule ID<input ref={historyInput} value={historyDraft} onChange={(event) => setHistoryDraft(event.target.value)} maxLength={36} required disabled={locked} /></label><button aria-label="Open retained history" disabled={locked}>Retained history</button></form>
      </div>
    </SidebarSurface>
    <div hidden={!active} className="page schedule-page">{editing ? <ScheduleEditor readLocalWorker={readLocalWorker} key={editing.key} initial={editing.initial} active={active} saved={(row) => { setEditing(undefined); setProtectedWorkflow(false); if (row) setSelected(row); void result.refetch(); }} cancel={() => { setEditing(undefined); setProtectedWorkflow(false); }} protectedChange={setProtectedWorkflow} /> : selected ? <ScheduleDetails key={selected.id} initial={selected} active={active} open={open} close={() => { setSelected(undefined); setProtectedWorkflow(false); void result.refetch(); }} edit={(initial) => { setSelected(undefined); setEditing({ initial, key: newRequestId() }); }} protectedChange={setProtectedWorkflow} /> : history ? <><button onClick={() => setHistory("")}>Back to schedules</button><ScheduleHistory key={history} id={history} active={active} open={open} /></> : <section><h2>Schedules</h2><p>Select a schedule from the sidebar to inspect its details and retained history.</p></section>}</div>
  </>;
}
