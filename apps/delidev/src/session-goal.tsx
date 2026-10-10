// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { EntityKind, NativeGoalAction, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text } from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import { useSessionActive, useSessionQuery as useQuery } from "./session-activity";
import { goalActionPending, goalBudget, goalObjective, goalStatuses, goalStatusValues, nativeGoalView } from "./session-goal-model";
import { Timestamp } from "./timestamp-display";
import { retainedSessionHarness } from "./session-harness";
import { Problem } from "./ui";
import "./session-goal.css";

export function SessionGoal({ session, changed, readOnly = false }: { session: Resource; changed: (value: Resource) => void; readOnly?: boolean }) {
  useLocale();
  const active = useSessionActive();
  const capabilities = useQuery(SystemQuery.getStatus, {});
  const supported = !capabilities.error && Boolean(capabilities.data?.capabilities.includes(SystemCapability.NATIVE_CODEX_GOALS_V1));
  const result = useQuery(SessionQuery.getSessionGoalState, { sessionId: session.id }, { enabled: supported, refetchInterval: 5000 });
  const joined = result.data?.session;
  const current = joined?.kind === EntityKind.SESSION && joined.id === session.id && joined.revision >= session.revision ? joined : undefined;
  const data = document(current);
  const view = nativeGoalView(data.native_goal);
  const execution = object(data.execution);
  const machineId = text(data.machine_id);
  const machine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: machineId }, { enabled: supported && Boolean(machineId), retry: false });
  const row = machine.data?.resource;
  const workerSupported = !machine.error && row?.id === machineId && row.kind === EntityKind.MACHINE && row.revision > 0n && supportsResourceSchema(row) && items(document(row).worker_capabilities).includes("native-codex-goals-v1");
  const harness = retainedSessionHarness(current);
  const sidechat = Boolean(object(data.fork).sidechat_parent_snapshot);
  const originalScope = Boolean(view && text(execution.execution_id) === view.source_execution_id && text(execution.native_thread_id) === view.source_native_thread_id && (!data.current_execution || text(object(data.current_execution).id) === view.source_execution_id));
  const liveScope = data.archive === "active" && data.recovery === "none" && data.dispatch === "claimed" && data.active_execution_id === view?.source_execution_id && execution.cleanup_verified !== true;
  const eligible = liveScope && active && supported && !readOnly && !sidechat && harness === "codex" && workerSupported && originalScope && view?.enabled === true && !result.error && !result.isFetching;
  const [draft, setDraft] = useState<{ revision: bigint; execution: string; thread: string; objective: string; changeObjective: boolean; status: string; budgetMode: string; budget: string }>();
  const mutation = useRetainedMutation(`native-goal:${session.id}`, SessionQuery.requestSessionGoalAction, (reply, request) => {
    if (reply.session) changed(reply.session);
    if (request.action === NativeGoalAction.SET) setDraft(undefined);
    void result.refetch();
  }, (reply, request) => reply.requestId === request.mutation?.requestId && reply.session?.id === request.mutation?.id && reply.session?.kind === EntityKind.SESSION && Boolean(reply.action && reply.action.kind === EntityKind.JOB && document(reply.action).type === "native-goal-action" && reply.action.sessionId === request.mutation?.id && nativeGoalView(document(reply.session).native_goal)?.action_id === reply.action.id && nativeGoalView(document(reply.session).native_goal)?.source_execution_id === request.expectedExecutionId && nativeGoalView(document(reply.session).native_goal)?.source_native_thread_id === request.expectedNativeThreadId));
  const blocked = mutation.busy || mutation.uncertain || goalActionPending(view);
  const stale = Boolean(draft && current && (draft.revision !== current.revision || draft.execution !== view?.source_execution_id || draft.thread !== view?.source_native_thread_id));
  const objectiveValid = !draft?.changeObjective || goalObjective(draft.objective);
  const budgetValid = draft?.budgetMode !== "set" || goalBudget(draft.budget) !== undefined;
  const hasChange = Boolean(draft && (draft.changeObjective || draft.status || draft.budgetMode !== "keep"));
  const observation = view?.observation;
  const send = (action: NativeGoalAction) => {
    if (!eligible || blocked || !current || !view || action === NativeGoalAction.SET && (!draft || stale || !objectiveValid || !budgetValid || !hasChange)) return;
    void mutation.send({ mutation: { id: session.id, expectedRevision: current.revision, requestId: newRequestId() }, expectedExecutionId: view.source_execution_id, expectedNativeThreadId: view.source_native_thread_id, action,
      ...(action === NativeGoalAction.SET && draft ? { set: { ...(draft.changeObjective ? { objective: draft.objective } : {}), ...(draft.status ? { status: goalStatusValues[goalStatuses.indexOf(draft.status as typeof goalStatuses[number])] } : {}), budget: draft.budgetMode === "set" ? { case: "tokenBudget" as const, value: goalBudget(draft.budget)! } : draft.budgetMode === "default" ? { case: "resetTokenBudget" as const, value: true } : { case: undefined } } } : {}) });
  };
  return <section className="session-goal" aria-label={copy("session-goal.title")}>
    <p>{copy("session-goal.explanation")}</p>
    <Problem error={capabilities.error || result.error || machine.error || mutation.error} />
    {!supported || sidechat || harness !== "codex" || !workerSupported || !originalScope || view?.enabled !== true ? <p role="status">{copy("session-goal.unavailable")}</p> : null}
    {observation === undefined ? <p>{copy("session-goal.unknown")}</p> : observation === null ? <p>{copy("session-goal.absent")}</p> : <>
      <p className="payload">{observation.objective}</p>
      <dl><dt>{copy("session-goal.status")}</dt><dd>{copy(`session-goal.status.${observation.status}`)}</dd><dt>{copy("session-goal.budget")}</dt><dd>{observation.token_budget ?? copy("session-goal.nativeDefault")}</dd><dt>{copy("session-goal.tokens")}</dt><dd>{observation.tokens_used}</dd><dt>{copy("session-goal.seconds")}</dt><dd>{observation.time_used_seconds}</dd></dl>
    </>}
    {view?.observed_at ? <p>{copy("session-goal.observed")} <Timestamp value={view.observed_at} /></p> : null}
    {result.error && view ? <p role="status">{copy("session-goal.stale")}</p> : null}
    {view?.action_state ? <p role="status">{copy(`session-goal.action.${view.action_state}`)}</p> : null}
    {supported && originalScope && !liveScope ? <p role="status">{copy("session-goal.readOnlyState")}</p> : null}
    {view?.problem_code ? <p role="alert">{copy("session-goal.recovery")}</p> : null}
    <div className="actions"><button type="button" disabled={!supported || result.isFetching} onClick={() => void result.refetch()}>{copy("session-goal.reload")}</button><button type="button" disabled={!eligible || blocked} onClick={() => send(NativeGoalAction.READ)}>{copy("session-goal.refresh")}</button><button type="button" disabled={!eligible || blocked || Boolean(draft)} onClick={() => { if (current && view) setDraft({ revision: current.revision, execution: view.source_execution_id, thread: view.source_native_thread_id, objective: observation?.objective ?? "", changeObjective: false, status: "", budgetMode: "keep", budget: observation?.token_budget ?? "" }); }}>{copy("session-goal.edit")}</button><button type="button" disabled={!eligible || blocked} onClick={() => send(NativeGoalAction.CLEAR)}>{copy("session-goal.clear")}</button></div>
    {mutation.uncertain ? <><p role="alert">{copy("session-goal.uncertain")}</p><button type="button" disabled={mutation.busy || !active || readOnly} onClick={mutation.retry}>{copy("session-goal.retry")}</button></> : null}
    {draft ? <form onSubmit={event => { event.preventDefault(); send(NativeGoalAction.SET); }}>
      <fieldset disabled={blocked}><label className="checkbox"><input type="checkbox" checked={draft.changeObjective} onChange={event => setDraft({ ...draft, changeObjective: event.target.checked })} />{copy("session-goal.changeObjective")}</label><label>{copy("session-goal.objective")}<textarea maxLength={8000} value={draft.objective} disabled={!draft.changeObjective} onChange={event => setDraft({ ...draft, objective: event.target.value })} /></label>
      <label>{copy("session-goal.status")}<select value={draft.status} onChange={event => setDraft({ ...draft, status: event.target.value })}><option value="">{copy("session-goal.keep")}</option>{goalStatuses.map(status => <option key={status} value={status}>{copy(`session-goal.status.${status}`)}</option>)}</select></label>
      <label>{copy("session-goal.budget")}<select value={draft.budgetMode} onChange={event => setDraft({ ...draft, budgetMode: event.target.value })}><option value="keep">{copy("session-goal.keep")}</option><option value="default">{copy("session-goal.nativeDefault")}</option><option value="set">{copy("session-goal.explicitBudget")}</option></select></label>{draft.budgetMode === "set" ? <label>{copy("session-goal.explicitBudget")}<input inputMode="numeric" value={draft.budget} onChange={event => setDraft({ ...draft, budget: event.target.value })} /></label> : null}</fieldset>
      {!objectiveValid ? <p role="alert">{copy("session-goal.invalidObjective")}</p> : null}{!budgetValid ? <p role="alert">{copy("session-goal.invalidBudget")}</p> : null}
      {stale ? <p role="alert">{copy("session-goal.changed")}</p> : null}
      {stale && !blocked && current && view && draft.execution === view.source_execution_id && draft.thread === view.source_native_thread_id ? <button type="button" disabled={!eligible} onClick={() => setDraft({ ...draft, revision: current.revision })}>{copy("session-goal.rebase")}</button> : null}
      <div className="actions"><button className="primary" disabled={!eligible || blocked || stale || !hasChange || !objectiveValid || !budgetValid}>{copy("session-goal.save")}</button><button type="button" disabled={mutation.busy || mutation.uncertain} onClick={() => setDraft(undefined)}>{copy("session-goal.cancel")}</button></div>
    </form> : null}
  </section>;
}
