// SPDX-License-Identifier: Apache-2.0
import { DisclosureButton, DisclosureContent, DisclosureDensity, Disclosure, DisclosureSummary } from "./disclosure";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { useRunnerRemediation } from "./runner-remediation";
import { RunnerWorkflow, useRunnerPreference } from "./runner-device-preferences";
import { Code, ConnectError } from "@connectrpc/connect";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { statusLabel } from "./product-status";
import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, ScheduleAction, ScheduleQuery, WorkerCapability, newRequestId, isEntityId, supportsResourceSchema, type ListSchedulesResponse, type ListScheduleOccurrencesResponse, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, Mode, object, text, Workspace, type Document } from "./documents";
import { ReferenceFields, ResourceChoice, TextField } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { ServiceProblem, Failure, Problem  } from "./ui";
import { Icon as SidebarIcon } from "./sidebar";
import { ScheduleCreation, type ScheduleCreationProps } from "./schedule-creation";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";

enum Overlap { Overlap = "overlap", Skip = "skip", Wait = "wait" }
enum EnabledFilter { All = "all", Enabled = "enabled", Paused = "paused" }
const ignoreProtectedChange = (_protectedState: boolean) => undefined;
const scheduleName = (row?: Resource) => text(object(document(row).definition).name) || copy("schedules.extra.d5ef9d155d1e");
const emptyDefinition = (): Document => ({ name: "", prompt: "", enabled: false, project_id: "", agent_id: "", machine_id: "", workspace: Workspace.Worktree, mode: Mode.Execute, cron: "0 9 * * 1-5", timezone: "UTC", overlap: Overlap.Overlap });

function schedulePage(response: ListSchedulesResponse) {
  if (response.schedules.length > 50 || response.schedules.some(row => !isEntityId(row.id))) throw new ConnectError("Invalid schedule page", Code.DataLoss);
  return { rows: response.schedules.map(row => ({ id: row.id, revision: row.revision })), payload: response.schedules, nextPageToken: response.nextPageToken };
}
function occurrencePage(response: ListScheduleOccurrencesResponse) {
  if (response.occurrences.length > 50 || response.occurrences.some(row => !isEntityId(row.id))) throw new ConnectError("Invalid schedule occurrence page", Code.DataLoss);
  return { rows: response.occurrences.map(row => ({ id: row.id, revision: row.revision })), payload: response.occurrences, nextPageToken: response.nextPageToken };
}

export function StartingReferences({ project, starting, change, active }: { project: string; starting: unknown[]; change: (value: unknown[]) => void; active: boolean }) {
  useLocale();
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: project }, { enabled: active && Boolean(project) });
  const [selected, setSelected] = useState("");
  const references = starting.map(object);
  return <fieldset><legend>{copy("schedules.startingReferenceOverrides_58881e")}</legend><p>{copy("schedules.omittedRepositoriesUseTheirSavedStarting_266784")}</p><label>{copy("schedules.addRepositoryOverride_3564ca")}<select value={selected} onChange={(event) => setSelected(event.target.value)}><option value="">{copy("schedules.selectAProjectRepository_404acc")}</option>{items(document(result.data?.resource).repositories).map(text).filter((id) => !references.some((row) => row.repository_id === id)).map((id) => <option key={id} value={id}>{id}</option>)}</select></label><button type="button" disabled={!selected || references.length >= 1000} onClick={() => { change([...starting, { repository_id: selected, reference: { type: "local-branch", name: "" } }]); setSelected(""); }}>{copy("schedules.addStartingOverride_71ea93")}</button><Problem error={result.error} />{references.map((row) => <fieldset key={text(row.repository_id)}><legend>{text(row.repository_id)}</legend><ReferenceFields label={copy("schedules.starting_23cc8e", { v0: text(row.repository_id) })} value={row.reference} change={(reference) => change(reference.type ? starting.map((value) => object(value).repository_id === row.repository_id ? { ...row, reference } : value) : starting.filter((value) => object(value).repository_id !== row.repository_id))} /><button type="button" onClick={() => change(starting.filter((value) => object(value).repository_id !== row.repository_id))}>{copy("schedules.removeStartingOverride_21a3a5")}</button></fieldset>)}</fieldset>;
}

export function ScheduleEditor({ initial, active, saved, cancel, readLocalWorker, protectedChange = ignoreProtectedChange }: { initial?: Resource; active: boolean; saved: (resource?: Resource) => void; cancel: () => void; readLocalWorker?: ReadLocalWorkerProof; protectedChange?: (protectedState: boolean) => void }) {
  useLocale();
  const localProof = useLocalWorkerProof(readLocalWorker);
  const [definition, setDefinition] = useState<Document>(() => initial ? object(document(initial).definition) : emptyDefinition());
  const runnerProject = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: text(definition.project_id) }, { enabled: active && !initial && Boolean(definition.project_id), retry: false });
  const projectKnown = !definition.project_id || !runnerProject.isFetching && !runnerProject.error && runnerProject.data?.resource?.id === definition.project_id && supportsResourceSchema(runnerProject.data.resource);
  const runnerEligible = (row: Resource) => projectKnown && (definition.workspace !== Workspace.Worktree || !definition.project_id || !items(document(runnerProject.data?.resource).repositories).length || items(document(row).worker_capabilities).some(capability => capability === "remote-workspace-clone-v1" || capability === WorkerCapability.REMOTE_WORKSPACE_CLONE_V1));
  const runner = useRunnerPreference(RunnerWorkflow.Schedule, active && !initial && projectKnown, runnerEligible, false, `${text(definition.project_id)}:${text(definition.workspace)}:${runnerProject.data?.resource?.revision ?? ""}`);
  const automaticRunner = useRef("");
  const runnerTouched = useRef(Boolean(initial));
  const [limit, setLimit] = useProductMessage("");
  const current = useQuery(ScheduleQuery.getSchedule, { id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`schedule-save:${initial?.id ?? "new"}`, ScheduleQuery.saveSchedule, (response, request) => { if (response.schedule?.kind === EntityKind.SCHEDULE && response.schedule.revision > 0n && isEntityId(response.schedule.id) && supportsResourceSchema(response.schedule)) { const submitted = object(JSON.parse(new TextDecoder().decode(request.definitionJson))); runner.remember(text(submitted.machine_id)); } saved(response.schedule); });
  const automaticRunnerBlocked = Boolean(!initial && !runnerTouched.current && automaticRunner.current && definition.machine_id === automaticRunner.current && (runner.reading || !projectKnown || !runner.suggestion || !runnerEligible(runner.suggestion)));
  const inspection = useRunnerRemediation();
  const blocked = mutation.busy || mutation.uncertain || localProof.busy || inspection?.pendingFor(text(definition.machine_id)) === true;
  useEffect(() => protectedChange(true), [protectedChange]);
  const stale = Boolean(initial && current.data?.schedule && initial.revision !== current.data.schedule.revision);
  const local = definition.workspace === Workspace.Local;
  useEffect(() => { if (active && !runnerTouched.current && !mutation.busy && !mutation.uncertain && !localProof.busy && definition.workspace !== Workspace.Local && runner.suggestion && runnerEligible(runner.suggestion) && (!definition.machine_id || definition.machine_id === automaticRunner.current)) { automaticRunner.current = runner.suggestion.id; setDefinition(value => value.machine_id === runner.suggestion!.id ? value : ({ ...value, machine_id: runner.suggestion!.id })); } }, [active, runner.suggestion, blocked, projectKnown, definition.project_id, runnerProject.data?.resource, definition.workspace, definition.machine_id]);
  useEffect(() => { if (active && !runnerTouched.current && !blocked && projectKnown && !runner.reading && !runner.lookupFailed && !runner.suggestion && automaticRunner.current && definition.machine_id === automaticRunner.current) { automaticRunner.current = ""; setDefinition(value => ({ ...value, machine_id: "" })); } }, [active, blocked, projectKnown, runner.reading, runner.lookupFailed, runner.suggestion, definition.machine_id]);
  const change = (next: Document) => {
    if (next.machine_id !== definition.machine_id || next.workspace !== definition.workspace) { runnerTouched.current = true; runner.touch(); }
    if (encode(next).byteLength > 1 << 20 || new TextEncoder().encode(text(next.prompt)).byteLength > 256 << 10) { setLimit(ownedMessage("schedules.extra.1a9ab790643b")); return false; }
    setDefinition(next); setLimit(""); return true;
  };
  const field = (key: string) => (value: unknown) => change({ ...definition, [key]: value });
  const submit = async () => {
    if (blocked || automaticRunnerBlocked || stale || (initial && current.error)) return;
    runner.touch();
    const original = object(document(initial).definition);
    const retainedLocal = local && original.workspace === Workspace.Local && original.machine_id === definition.machine_id;
    const input = { mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, definitionJson: encode(definition) };
    const proof = local && !retainedLocal ? await localProof.load(text(definition.machine_id)) : undefined;
    if (local && !retainedLocal && !proof) return;
    void mutation.send({ ...input, localWorkerToken: proof?.token });
  };
  if (!initial) {
    const props: ScheduleCreationProps = { definition, change, active, blocked, submitBlocked: automaticRunnerBlocked, cancel, submit,
      localAvailable: localProof.available,
      selectLocal: () => { void localProof.load().then((proof) => { if (proof) change({ ...definition, workspace: Workspace.Local, machine_id: proof.machineId, starting: [] }); }); },
      references: local ? null : <StartingReferences key={text(definition.project_id)} project={text(definition.project_id)} starting={items(definition.starting)} change={field("starting")} active={active} />,
      errors: <><Problem error={runnerProject.error} actions={<button type="button" disabled={!active || blocked || runnerProject.isFetching} onClick={() => void runnerProject.refetch()}>{copy("ui.retryCurrentRead")}</button>} />{runner.guidance}{limit ? <p role="alert">{limit}</p> : null}{localProof.problem ? <p role="alert">{localProof.problem}</p> : null}<Problem error={mutation.error} /></>,
      retry: mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("schedules.retryTheSameSchedule_7702b8")}</button> : null };
    return <ScheduleCreation {...props} />;
  }
  return <section><h3>{initial ? copy("schedules.editSchedule_559b37") : copy("schedules.newSchedule_3bfe90")}</h3><form onSubmit={(event) => { event.preventDefault(); void submit(); }}><fieldset disabled={blocked}>
    <TextField label={copy("schedules.scheduleName_60918e")} value={definition.name} required change={field("name")} /><label className="checkbox"><input type="checkbox" checked={definition.enabled === true} onChange={(event) => field("enabled")(event.target.checked)} />{copy("schedules.enableFutureScheduledRuns_d1eab6")}</label><ResourceChoice label={copy("schedules.project_985959")} kind={EntityKind.PROJECT} value={text(definition.project_id)} active={active} required change={(project_id) => setDefinition({ ...definition, project_id, starting: [] })} /><ResourceChoice label={copy("schedules.agentWorker_a4caa7")} kind={EntityKind.AGENT} value={text(definition.agent_id)} active={active} required change={field("agent_id")} /><ResourceChoice label={copy("schedules.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={text(definition.machine_id)} active={active} disabled={local} required change={field("machine_id")} />
    <div className="actions"><button type="button" aria-pressed={!local} onClick={() => setDefinition({ ...definition, workspace: Workspace.Worktree })}>{copy("schedules.useSeparateWorktrees_5cd0b6")}</button><button type="button" disabled={!localProof.available} aria-pressed={local} onClick={() => { void localProof.load().then((proof) => { if (proof) setDefinition({ ...definition, workspace: Workspace.Local, machine_id: proof.machineId, starting: [] }); }); }}>{copy("schedules.useThisComputerSLocalCheckouts_eadaad")}</button></div><p><LocalizedText id="schedules.workspace_4eaed8" components={{ s0: <>{local ? copy("schedules.localComputerOriginatingWorkerSelected_9357e2") : copy("schedules.worktreeSeparateDetachedCheckouts_5318f3")}</> }} /></p>{local ? <p>{copy("schedules.existingLocalSchedulesRetainTheirAuthenticated_538d29")}</p> : <StartingReferences key={text(definition.project_id)} project={text(definition.project_id)} starting={items(definition.starting)} change={field("starting")} active={active} />}
    <label>{copy("schedules.executionMode_c21e7c")}<select value={text(definition.mode)} onChange={(event) => field("mode")(event.target.value)}>{Object.values(Mode).map((mode) => <option key={mode} value={mode}>{mode}</option>)}</select></label><label>{copy("schedules.scheduledPrompt_209d2b")}<textarea required rows={6} maxLength={262144} value={text(definition.prompt)} onChange={(event) => field("prompt")(event.target.value)} /></label><TextField label={copy("schedules.cronExpression_9e6e7d")} value={definition.cron} required max={512} change={field("cron")} /><p>{copy("schedules.fiveFieldsMinuteHourDayOf_8fec47")}</p><TextField label={copy("schedules.ianaTimezone_37cf56")} value={definition.timezone} required change={field("timezone")} /><label>{copy("schedules.whenAPreviousOccurrenceIsStill_851448")}<select value={text(definition.overlap)} onChange={(event) => field("overlap")(event.target.value)}><option value={Overlap.Overlap}>{copy("schedules.overlapIndependentSessions_df3095")}</option><option value={Overlap.Skip}>{copy("schedules.skipTheNewOccurrence_e30b7e")}</option><option value={Overlap.Wait}>{copy("schedules.waitInFifoOrderForConfirmed_f1ce8e")}</option></select></label><p>{copy("schedules.theServerContinuesSchedulingWhenThe_116ec3")}</p></fieldset>
    {stale ? <p role="alert">{copy("schedules.thisScheduleChangedElsewhereYourDraft_9d939b")}</p> : null}{limit ? <p role="alert">{limit}</p> : null}{localProof.problem ? <p role="alert">{localProof.problem}</p> : null}<Problem error={current.error} actions={<button type="button" disabled={!active || blocked || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</button>} /><Problem error={mutation.error} /><div className="actions"><button className="primary" disabled={blocked || stale || Boolean(initial && current.error)}>{copy("schedules.saveSchedule_387f35")}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("schedules.retryTheSameSchedule_7702b8")}</button> : null}<button type="button" disabled={blocked} onClick={cancel}>{copy("schedules.cancelScheduleEdit_efc0c0")}</button></div></form></section>;
}

function Occurrence({ resource, open }: { resource: Resource; open: (id: string) => void }) {
  useLocale();
  const data = document(resource), selection = object(data.selection), problem = object(data.problem);
  return <article className="result"><h4>{text(data.trigger)} · {statusLabel(text(data.state))}</h4><p><LocalizedText id="schedules.dueAccepted_f5f495" components={{ s0: <><Timestamp value={text(data.due_at)} /></>, s1: <><Timestamp value={text(data.accepted_at)} /></> }} /></p>{text(data.reason) ? <p><LocalizedText id="schedules.reason_efbcae" components={{ s0: <>{text(data.reason)}</> }} /></p> : null}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}{text(data.session_id) ? <button onClick={() => open(text(data.session_id))}>{copy("schedules.openOccurrenceSession_dceba4")}</button> : <p>{copy("schedules.noSessionHasBeenCreatedFor_782f8a")}</p>}<Disclosure><DisclosureSummary>{copy("schedules.originalAcceptedSelection_4b454e")}</DisclosureSummary><p>{text(selection.name)} · {text(selection.workspace)} · {text(selection.mode)}</p><p><LocalizedText id="schedules.agentWorker_16fb23" components={{ s0: <>{text(selection.agent_id)}</>, s1: <>{text(selection.machine_id)}</> }} /></p><p><LocalizedText id="schedules.overlap_eb259a" components={{ s0: <>{text(data.overlap)}</> }} /></p><pre>{text(selection.prompt)}</pre><small>{resource.id}</small></Disclosure></article>;
}
export function ScheduleHistory({ id, active, open }: { id: string; active: boolean; open: (id: string) => void }) {
  useLocale();
  const content = useRef<HTMLElement>(null), root = useScrollRoot(content);
  const request = useCallback((token: string) => ({ scheduleId: id, pageSize: 50, pageToken: token }), [id]);
  const reader = useConnectPaginationReader(ScheduleQuery.listScheduleOccurrences, request, occurrencePage);
  const result = usePaginationChain(id, active, reader);
  usePaginationRefresh(ScheduleQuery.listScheduleOccurrences, request(""), active, result.refresh, 5000);
  return <section ref={content}><header><h3>{copy("schedules.occurrenceHistory_4a178f")}</h3><button disabled={Boolean(result.loading)} onClick={result.refreshExplicit}>{copy("schedules.refreshHistory_70f3d2")}</button></header><p>{copy("schedules.historyRemainsAvailableAfterScheduleConfiguration_44b3c0")}</p><Failure failure={result.error?.failure} /><ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={result} root={root} active={active}>{payload => payload.map(row => <Occurrence resource={row} key={row.id} open={open} />)}</ScrollPayloadWindow>{result.loaded && result.rows.length === 0 ? <p>{copy("schedules.noRetainedOccurrences_dc8c9a")}</p> : null}<ScrollContinuation query={result} root={root} active={active} label={copy("schedules.occurrenceHistory_4a178f")} /></section>;
}

export function ScheduleDetails({ initial, active, open, close, edit, protectedChange = ignoreProtectedChange }: { initial: Resource; active: boolean; open: (id: string) => void; close: () => void; edit: (row: Resource) => void; protectedChange?: (protectedState: boolean) => void }) {
  useLocale();
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
  return <section><header><h2>{scheduleName(current)}</h2><button disabled={blocked || Boolean(confirm)} onClick={close}>{copy("schedules.backToSchedules_975251")}</button></header><small>{initial.id}</small>{deleted ? <p>{copy("schedules.scheduleConfigurationDeletedRetainedOccurrencesAnd_08b290")}</p> : <><p>{definition.enabled === true ? copy("schedules.enabled_92c1cd") : copy("schedules.paused_e159b0")} · {text(definition.cron)} · {text(definition.timezone)}</p><p><LocalizedText id="schedules.nextRunUtc_eca09c" components={{ s0: <><Timestamp mode={TimestampMode.Absolute} timeZone="UTC" value={text(data.next_run_at)} fallback={copy("schedules.extra.170fa2a3d0f0")} /></> }} /></p><p><LocalizedText id="schedules.overlapPolicy_9f02ce" components={{ s0: <>{text(definition.overlap)}</> }} /></p>{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<div className="actions"><button disabled={blocked || current.schemaVersion !== 1} onClick={() => edit(current)}>{copy("schedules.editSchedule_559b37")}</button><button disabled={blocked} onClick={() => void control.send({ mutation: mutation(), action: definition.enabled === true ? ScheduleAction.PAUSE : ScheduleAction.RESUME })}>{definition.enabled === true ? copy("schedules.pauseFutureRuns_bd1b76") : copy("schedules.resumeFutureRuns_273685")}</button><button disabled={blocked} onClick={() => setConfirm("run")}>{copy("schedules.runNow_099139")}</button><button disabled={blocked} onClick={() => setConfirm("delete")}>{copy("schedules.deleteSchedule_d8d0bd")}</button></div>{confirm ? <div className="notice"><p>{confirm === "run" ? copy("schedules.acceptOneIndependentOccurrenceNowUsing_09cf1a") : copy("schedules.deleteFutureSchedulingConfigurationAlreadyAccepted_eb8d35")}</p><button disabled={blocked} onClick={() => confirm === "run" ? void run.send({ mutation: mutation() }) : void remove.send({ mutation: mutation() })}>{confirm === "run" ? copy("schedules.confirmRunNow_e9bc9d") : copy("schedules.confirmScheduleDeletion_27d4ca")}</button><button disabled={blocked} onClick={() => setConfirm(undefined)}>{copy("schedules.cancel_19766e")}</button></div> : null}<Problem error={result.error} /></>}
    {operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="schedules.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("schedules.scheduleControl_8ea17b") : index === 1 ? copy("schedules.scheduleDeletion_183079") : copy("schedules.runNow_099139")}</> }} /></button> : null}</div>)}{occurrence ? <section><h3>{copy("schedules.runNowAccepted_e83ab1")}</h3><Problem error={occurrenceResult.error} /><Occurrence resource={occurrenceResult.data?.occurrence && occurrenceResult.data.occurrence.revision >= occurrence.revision ? occurrenceResult.data.occurrence : occurrence} open={open} /></section> : null}<ScheduleHistory id={initial.id} active={active && !blocked} open={open} />
  </section>;
}

export function Schedules({ active, open, readLocalWorker }: { active: boolean; open: (id: string) => void; readLocalWorker?: ReadLocalWorkerProof }) {
  useLocale();
  const [filter, setFilter] = useState(EnabledFilter.All);
  const content = useRef<HTMLDivElement>(null), root = useScrollRoot(content);
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
  const request = useCallback((token: string) => ({ projectId, pageSize: 50, pageToken: token, ...(filter === EnabledFilter.All ? {} : { enabled: filter === EnabledFilter.Enabled }) }), [projectId, filter]);
  const reader = useConnectPaginationReader(ScheduleQuery.listSchedules, request, schedulePage);
  const result = usePaginationChain(JSON.stringify([projectId, filter]), active && !locked, reader);
  usePaginationRefresh(ScheduleQuery.listSchedules, request(""), active && !locked, result.refresh);
  const selectSchedule = (row: Resource) => { if (locked) return; setSelected(row); setHistory(""); closeDrawer(); };
  const newSchedule = () => { if (locked) return; setSelected(undefined); setHistory(""); setEditing({ key: newRequestId() }); closeDrawer(); };
  const openHistory = (event: FormEvent) => { event.preventDefault(); if (locked || !historyDraft.trim()) return; setSelected(undefined); setHistory(historyDraft.trim()); closeDrawer(); };
  return <>
    <SidebarSurface active={active} title={copy("schedules.schedules_221ff1")} className="schedules-sidebar">
      <button className="primary sidebar-action" disabled={locked} onClick={newSchedule}><SidebarIcon name="plus" />{copy("schedules.newSchedule_3bfe90")}</button>
      <div className="sidebar-filter-options" aria-label={copy("schedules.scheduleState_dd8c03")}>{([[EnabledFilter.All, copy("schedules.extra.1c1c1bfaa5af")], [EnabledFilter.Enabled, copy("schedules.extra.92c1cdfdf4cb")], [EnabledFilter.Paused, copy("schedules.extra.e159b06187d3")]] as const).map(([value, label]) => <button key={value} type="button" aria-pressed={filter === value} disabled={locked} onClick={() => { setFilter(value); }}>{label}</button>)}</div>
      <ResourceChoice label={copy("schedules.filterByProject_9e2a0a")} emptyLabel={copy("schedules.allProjects_4b8727")} kind={EntityKind.PROJECT} value={projectId} change={(id) => { if (!locked) { setProjectId(id); } }} active={active} disabled={locked} />
      <header className="sidebar-list-heading"><h3>{copy("schedules.savedSchedules_97f381")}</h3><button type="button" disabled={locked || Boolean(result.loading)} onClick={result.refreshExplicit}><SidebarIcon name="refresh" />{copy("schedules.refresh_0e9161")}</button></header>
      <Failure failure={result.error?.failure} />{!result.loaded && Boolean(result.loading) && active ? <p role="status">{copy("schedules.loadingSchedules_77307d")}</p> : null}{result.error && result.loaded ? <p className="sidebar-help">{copy("schedules.refreshFailedShowingThePreviousPage_24e457")}</p> : null}
      <div ref={content}><ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={result} root={root} active={active && !locked}>{payload => payload.map((row) => <article key={row.id} className="sidebar-schedule-row"><button type="button" disabled={locked} aria-current={selected?.id === row.id ? "true" : undefined} onClick={() => selectSchedule(row)}><span className="schedule-row-title">{scheduleName(row)}</span><span className="schedule-row-state">{object(document(row).definition).enabled === true ? copy("schedules.enabled_92c1cd") : copy("schedules.paused_e159b0")}</span><span className="schedule-row-next"><LocalizedText id="schedules.nextRunUtc_eca09c" components={{ s0: <span>{<Timestamp mode={TimestampMode.Absolute} timeZone="UTC" value={text(document(row).next_run_at)} fallback={copy("schedules.extra.170fa2a3d0f0")} />}</span> }} /></span></button></article>)}</ScrollPayloadWindow>
      {!result.error && result.loaded && result.rows.length === 0 ? <p className="sidebar-help schedules-empty"><SidebarIcon name="schedules" />{copy("schedules.noSavedSchedules_3aa66d")}</p> : null}
      <ScrollContinuation query={result} root={root} active={active && !locked} label={copy("schedules.savedSchedules_97f381")} /></div>
      <DisclosureButton density={DisclosureDensity.Compact} type="button" className="schedules-history-toggle" aria-expanded={historyExpanded} aria-controls={historyRegionId} disabled={locked} onClick={() => { if (locked) return; focusHistoryOnExpansion.current = !historyExpanded; setHistoryExpanded(!historyExpanded); }}>{copy("schedules.retainedHistory_183a8d")}</DisclosureButton>
      <DisclosureContent role="region" id={historyRegionId} aria-label={copy("schedules.retainedScheduleHistoryLookup_80858c")} hidden={!historyExpanded}>
        <form className="sidebar-form" onSubmit={openHistory}><label>{copy("schedules.retainedScheduleId_1b69be")}<input ref={historyInput} value={historyDraft} onChange={(event) => setHistoryDraft(event.target.value)} maxLength={36} required disabled={locked} /></label><button aria-label={copy("schedules.openRetainedHistory_95da82")} disabled={locked}>{copy("schedules.retainedHistory_183a8d")}</button></form>
      </DisclosureContent>
    </SidebarSurface>
    <div hidden={!active} className="page schedule-page">{editing ? <ScheduleEditor readLocalWorker={readLocalWorker} key={editing.key} initial={editing.initial} active={active} saved={(row) => { setEditing(undefined); setProtectedWorkflow(false); if (row) setSelected(row); result.refresh(); }} cancel={() => { setEditing(undefined); setProtectedWorkflow(false); }} protectedChange={setProtectedWorkflow} /> : selected ? <ScheduleDetails key={selected.id} initial={selected} active={active} open={open} close={() => { setSelected(undefined); setProtectedWorkflow(false); result.refresh(); }} edit={(initial) => { setSelected(undefined); setEditing({ initial, key: newRequestId() }); }} protectedChange={setProtectedWorkflow} /> : history ? <><button onClick={() => setHistory("")}>{copy("schedules.backToSchedules_975251")}</button><ScheduleHistory key={history} id={history} active={active} open={open} /></> : <section><h2>{copy("schedules.schedules_221ff1")}</h2><p>{copy("schedules.selectAScheduleFromTheSidebar_6800ae")}</p></section>}</div>
  </>;
}
