// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ForkPurpose, ForkWorkspace, ResourceQuery, SessionQuery, SystemQuery, clientFailure, newRequestId, type SystemCapability, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { settled, sidechatSupported } from "./sidechat-eligibility";
import { useRetainedMutation } from "./mutation";
import { JobState } from "./jobs";
import { copy, useLocale } from "./localization";
import { Problem, ServiceProblem } from "./ui";
import { SessionTabKind, useSessionTabsStore } from "./session-tabs";

export interface SidechatDraft { text: string; start: number; end: number; direction: "forward" | "backward" | "none"; focus?: number; opener?: HTMLElement; keepCaret?: boolean }
interface Operation { source: Resource; requestId: string; draft: SidechatDraft; focus: number; admitted: boolean; rejected?: boolean; invalid?: boolean; job?: Resource }
interface Ready { sourceKey: string; parent: string; child: string; name: string; draft: SidechatDraft }
const sourceKey = (source: Resource) => `${source.id}:${source.revision}:${text(object(document(source).execution).native_turn_id)}`;
export function sidechatAdmission(source: Resource, fresh: Resource | undefined, machine: Resource | undefined, capabilities: readonly SystemCapability[] | undefined) {
  const machineId = text(document(source).machine_id);
  return Boolean(fresh && fresh.id === source.id && fresh.kind === EntityKind.SESSION && sourceKey(fresh) === sourceKey(source) && text(document(fresh).machine_id) === machineId && settled(fresh) && machine && machine.id === machineId && machine.kind === EntityKind.MACHINE && machine.schemaVersion === 1 && machine.revision > 0n && sidechatSupported(fresh, capabilities, document(machine)));
}
export function verifiedSidechatJob(source: Pick<Resource, "id">, job: Resource | undefined, id?: string, minimumRevision = 1n) {
  return Boolean(job && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(job.id) && (!id || job.id === id) && job.kind === EntityKind.JOB && job.schemaVersion === 1 && job.revision >= minimumRevision && text(object(document(job).input).source_session_id) === source.id && Object.values(JobState).includes(text(document(job).state) as JobState));
}
const emptyDraft = (): SidechatDraft => ({ text: "", start: 0, end: 0, direction: "none" });
export function verifiedSidechat(source: Resource, child: Resource | undefined): child is Resource {
  const fork = object(document(child).fork);
  return Boolean(child && child.kind === EntityKind.SESSION && child.schemaVersion === 1 && child.revision > 0n && child.id !== source.id && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(child.id) && fork.source_session_id === source.id && Object.keys(object(fork.sidechat_parent_snapshot)).length);
}
type Controller = { open: (source: Resource) => void; outstanding: boolean; hasOutstanding: () => boolean; operation?: Operation; ready: ReadonlyMap<string, Ready>; childDraft: (id: string, value: string) => void; draft: (draft: SidechatDraft) => void; pendingComposer: RefObject<HTMLTextAreaElement | null>; retryRequest: () => void; retryStatus: () => void; retryAdmission: () => void; discard: () => void; canDiscard: boolean; requestError?: unknown; readError?: unknown; status: string; busy: boolean; uncertain: boolean; admissionError?: unknown; unverified: boolean; problem: ReturnType<typeof object> };
const Context = createContext<Controller | undefined>(undefined);
export const useDirectSidechat = () => useContext(Context);

/** Connection-owned original operation; pending tab identities never enter RPCs. */
export function DirectSidechatProvider({ children, activateParent }: { children: ReactNode; activateParent: (id: string) => void }) {
  const tabs = useSessionTabsStore();
  const [operation, setOperation] = useState<Operation>();
  const owner = useRef(operation); owner.current = operation;
  const [ready, setReady] = useState<ReadonlyMap<string, Ready>>(new Map());
  const pendingComposer = useRef<HTMLTextAreaElement>(null);
  const focus = useRef(0);
  const replace = (next?: Operation) => { owner.current = next; setOperation(next); };
  const mutation = useRetainedMutation("session-sidechat-direct", SessionQuery.forkSession, (result, request) => {
    const original = owner.current, job = result.job;
    if (!original || request.mutation?.requestId !== original.requestId) return;
    if (!verifiedSidechatJob(original.source, job)) { replace({ ...original, invalid: true }); return; }
    replace({ ...original, job });
  }, (result, request) => verifiedSidechatJob({ id: request.mutation?.id ?? "" }, result.job));
  const [admissionVersion, setAdmissionVersion] = useState(0);
  const attempt = useRef("");
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.SESSION, id: operation?.source.id ?? "" }, { enabled: false, retry: false });
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: false, retry: false });
  const machineId = text(document(operation?.source).machine_id);
  const runner = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: machineId }, { enabled: false, retry: false });
  const jobRead = useQuery(SessionQuery.getSessionFork, { jobId: operation?.job?.id ?? "" }, { enabled: Boolean(operation?.job) && text(document(operation?.job).state) !== JobState.Uncertain, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, refetchOnMount: false, refetchInterval: query => {
    if (query.state.data && operation?.job && (!verifiedSidechatJob(operation.source, query.state.data.job, operation.job.id, operation.job.revision) || query.state.data.job?.revision === operation.job.revision && text(document(query.state.data.job).state) !== text(document(operation.job).state))) return false;
    const state = text(document(query.state.data?.job).state);
    return operation?.job && !query.state.error && ![JobState.Succeeded, JobState.Failed, JobState.Canceled, JobState.Uncertain].includes(state as JobState) ? 2000 : false;
  } });
  useEffect(() => {
    const original = owner.current;
    const identity = `${original?.requestId}:${admissionVersion}`;
    if (!original || original.admitted || attempt.current === identity) return;
    attempt.current = identity;
    void (async () => {
      const [sourceRead, statusRead, runnerRead] = await Promise.all([current.refetch(), status.refetch(), runner.refetch()]);
      if (!alive.current || owner.current?.requestId !== original.requestId || owner.current.admitted || sourceRead.isError || statusRead.isError || runnerRead.isError) return;
      const fresh = sourceRead.data?.resource, machine = runnerRead.data?.resource;
      if (!sidechatAdmission(original.source, fresh, machine, statusRead.data?.capabilities)) {
        replace({ ...owner.current, admitted: true, rejected: true }); return;
      }
      // Freeze admission before dispatch: rerenders and repeated opening cannot send again.
      replace({ ...owner.current, admitted: true });
      void mutation.send({ mutation: { id: original.source.id, expectedRevision: original.source.revision, requestId: original.requestId }, expectedTurnId: text(object(document(original.source).execution).native_turn_id), name: `${resourceName(original.source)} Sidechat`.slice(0, 256), workspace: ForkWorkspace.UNSPECIFIED, purpose: ForkPurpose.SIDECHAT });
    })();
  }, [operation?.requestId, admissionVersion]);
  const retainedJob = jobRead.data?.job ?? operation?.job;
  const jobState = text(document(retainedJob).state);
  const originalJob = Boolean(operation?.job && verifiedSidechatJob(operation.source, retainedJob, operation.job.id, operation.job.revision) && (retainedJob?.revision !== operation.job.revision || text(document(retainedJob).state) === text(document(operation.job).state)));
  const unverified = Boolean(jobRead.data && (!originalJob || jobState === JobState.Succeeded && operation && !verifiedSidechat(operation.source, jobRead.data.session)));
  useEffect(() => {
    const original = owner.current, observed = jobRead.data?.job;
    if (original?.job && originalJob && observed && observed.revision > original.job.revision) replace({ ...original, job: observed });
  }, [jobRead.data, originalJob]);
  useEffect(() => { if (operation?.job) console.info("delidev.sidechat.status", { requestId: operation.requestId, jobId: operation.job.id, phase: unverified ? "unverified" : Object.values(JobState).includes(jobState as JobState) ? jobState : "preparing", ...(jobRead.error ? { errorCode: clientFailure(jobRead.error).code } : {}) }); }, [operation?.job?.id, jobState, unverified, jobRead.error]);
  useEffect(() => {
    const original = owner.current, child = jobRead.data?.session;
    if (!original || !original.job || !originalJob || jobRead.error || jobState !== JobState.Succeeded || !verifiedSidechat(original.source, child)) return;
    const field = pendingComposer.current;
    const ownsFocus = field && documentIsActive(field);
    const draft = { ...original.draft, ...(ownsFocus ? { start: field.selectionStart, end: field.selectionEnd, direction: field.selectionDirection, focus: ++focus.current } : { focus: undefined }) };
    const value: Ready = { sourceKey: sourceKey(original.source), parent: original.source.id, child: child.id, name: resourceName(child), draft };
    setReady(previous => new Map(previous).set(child.id, value));
    // Registration and descriptor replacement are atomic, with no navigation.
    // Closed or background tabs retain the child/draft without being reopened.
    tabs.publishSidechat(original.source.id, original.requestId, { kind: SessionTabKind.Sidechat, id: child.id, name: value.name });
    replace(); mutation.clearRejected();
    console.info("delidev.sidechat.published", { requestId: original.requestId, jobId: original.job.id, phase: "ready" });
  }, [jobRead.data, jobRead.error, originalJob, jobState, tabs]);
  const open = (source: Resource) => {
    const original = owner.current;
    if (original) { replace({ ...original, focus: ++focus.current }); tabs.open(original.source.id, { kind: SessionTabKind.PendingSidechat, requestId: original.requestId }); activateParent(original.source.id); return; }
    const existing = [...ready.values()].find(value => value.sourceKey === sourceKey(source));
    if (existing) { setReady(previous => new Map(previous).set(existing.child, { ...existing, draft: { ...existing.draft, focus: ++focus.current, opener: globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : undefined, keepCaret: true } })); tabs.open(existing.parent, { kind: SessionTabKind.Sidechat, id: existing.child, name: existing.name }); activateParent(existing.parent); return; }
    if (!settled(source)) return;
    mutation.clearRejected();
    const requestId = newRequestId();
    replace({ source: { ...source, documentJson: source.documentJson.slice() }, requestId, draft: emptyDraft(), focus: ++focus.current, admitted: false });
    tabs.open(source.id, { kind: SessionTabKind.PendingSidechat, requestId }); activateParent(source.id);
    console.info("delidev.sidechat.preparing", { requestId, phase: "preparing" });
  };
  const failedTerminal = originalJob && [JobState.Failed, JobState.Canceled].includes(jobState as JobState);
  const canDiscard = Boolean(operation && !mutation.busy && !mutation.uncertain && !operation.invalid && (failedTerminal || !operation.job && (operation.rejected || operation.admitted && mutation.error)));
  const controller: Controller = { open, outstanding: Boolean(operation), hasOutstanding: () => Boolean(owner.current), unverified, operation, ready, pendingComposer, childDraft: (id, value) => { setReady(previous => { const child = previous.get(id); if (!child || child.draft.text === value) return previous; return new Map(previous).set(id, { ...child, draft: { ...child.draft, text: value, focus: undefined, opener: undefined } }); }); }, draft: draft => { const original = owner.current; if (original) replace({ ...original, draft }); }, retryRequest: mutation.retry, retryStatus: () => { void jobRead.refetch(); }, retryAdmission: () => { setAdmissionVersion(value => value + 1); }, discard: () => { if (!canDiscard) return; if (operation) tabs.close(operation.source.id, JSON.stringify([SessionTabKind.PendingSidechat, operation.requestId])); replace(); mutation.clearRejected(); }, canDiscard, requestError: mutation.error, readError: jobRead.error, status: jobState, busy: mutation.busy || jobRead.isFetching || current.isFetching || status.isFetching || runner.isFetching, uncertain: mutation.uncertain, admissionError: current.error ?? status.error ?? runner.error, problem: originalJob ? object(document(retainedJob).problem) : {} };
  return <Context.Provider value={controller}>{children}</Context.Provider>;
}
function documentIsActive(field: HTMLTextAreaElement) { return globalThis.document.activeElement === field && field.isConnected && !field.closest("[hidden],[inert]"); }

export function PendingSidechatPane({ parent, active }: { parent: string; active: boolean }) {
  useLocale();
  const direct = useDirectSidechat(), focused = useRef(0);
  const operation = direct?.operation;
  useLayoutEffect(() => { if (!active || !direct || !operation || operation.focus === focused.current) return; const field = direct.pendingComposer.current; if (!field) return; field.focus({ preventScroll: true }); field.setSelectionRange(operation.draft.start, operation.draft.end, operation.draft.direction); focused.current = operation.focus; }, [active, operation?.focus]);
  if (!direct || !operation || operation.source.id !== parent) return null;
  const terminal = [JobState.Failed, JobState.Canceled, JobState.Uncertain, JobState.Succeeded].includes(direct.status as JobState);
  const problem = direct.problem;
  return <section className="session-sidechat-pending" hidden={!active} inert={!active} aria-label={copy("sidechat.direct.title")}>
    <div className="session-body"><p role="status" aria-live="polite">{copy("sidechat.direct.readOnly")}</p><p role={terminal || operation.rejected || operation.invalid || direct.unverified || Boolean(direct.requestError || direct.readError || direct.admissionError) ? "alert" : "status"}>{copy(terminal || operation.rejected || operation.invalid || direct.unverified || Boolean(direct.requestError || direct.readError || direct.admissionError) ? "sidechat.direct.unavailable" : "sidechat.direct.preparing")}</p>
      <Problem error={direct.admissionError} actions={<button type="button" disabled={direct.busy} onClick={direct.retryAdmission}>{copy("sidechat.direct.retryAdmission")}</button>}/>
      <Problem error={direct.requestError}/><Problem error={direct.readError}/>
      {text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}
      {direct.uncertain ? <button type="button" disabled={direct.busy} onClick={direct.retryRequest}>{copy("sidechat.direct.retryRequest")}</button> : null}
      {operation.job && (direct.readError || direct.unverified || terminal && ![JobState.Failed, JobState.Canceled].includes(direct.status as JobState)) ? <button type="button" disabled={direct.busy} onClick={direct.retryStatus}>{copy("sidechat.direct.retryStatus")}</button> : null}
      {direct.canDiscard ? <button type="button" onClick={direct.discard}>{copy("sidechat.direct.discard")}</button> : null}
    </div>
    <form className="composer" onSubmit={event => event.preventDefault()}><div className="composer-toolbar"><button type="button" disabled aria-label={copy("sidechat.direct.attach")}>+</button><label className="plan-mode"><input type="checkbox" disabled/>{copy("session.planMode")}</label><button type="submit" className="primary composer-submit" disabled aria-label={copy("sidechat.direct.send")}>↑</button></div><label htmlFor={`sidechat-draft-${operation.requestId}`}>{copy("sidechat.direct.message")}</label><textarea ref={direct.pendingComposer} id={`sidechat-draft-${operation.requestId}`} rows={1} value={operation.draft.text} placeholder={copy("sidechat.direct.placeholder")} onChange={event => { if (new TextEncoder().encode(event.target.value).byteLength <= (256 << 10)) direct.draft({ text: event.target.value, start: event.target.selectionStart, end: event.target.selectionEnd, direction: event.target.selectionDirection }); }} onSelect={event => { const field = event.currentTarget; if (new TextEncoder().encode(field.value).byteLength > (256 << 10)) return; direct.draft({ text: field.value, start: field.selectionStart, end: field.selectionEnd, direction: field.selectionDirection }); }}/></form>
  </section>;
}
