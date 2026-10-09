import { useSessionQuery as useQuery, useSessionActive } from "./session-activity";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { createPortal } from "react-dom";
import { useLayoutEffect, useState } from "react";

import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId, type Resource, type SessionChange } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

enum RecoveryAction { Prepare = "prepare", InspectWorkspace = "inspect-workspace", CleanupWorkspace = "cleanup-workspace", Execution = "execution" }
function RetainedJob({ id, title }: { id: string; title: string }) {
  useLocale();
  const active = useSessionActive();
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id });
  return <section><h4>{title}</h4><Problem error={result.error} />{result.data?.resource ? <TrackedJob initial={result.data.resource} active={active} /> : <p>{copy("session-tools.loadingRetainedOperation_e36e04")}</p>}</section>;
}
export function SessionTools({ resource, changed, initiallyOpen = false, target, launcherTarget, openRecovery }: { resource: Resource; changed: (resource: Resource) => void; initiallyOpen?: boolean; target?: HTMLElement | null; launcherTarget?: HTMLElement | null; openRecovery?: (opener: HTMLButtonElement) => void }) {
  useLocale();
  const [host] = useState(() => globalThis.document.createElement("div"));
  useLayoutEffect(() => {
    if (!target) return;
    target.append(host);
    return () => { host.remove(); };
  }, [host, target]);
  const data = document(resource), preparation = object(data.preparation), execution = object(data.execution);
  const initial = object(data.initial_execution);
  const sidechat = Boolean(object(data.fork).sidechat_parent_snapshot);
  const executionId = text(execution.execution_id) || (data.execution == null && data.current_execution == null && data.workspace === "worktree" ? text(initial.id) : "");
  const [name, setName] = useState<{ value: string; revision: bigint }>();
  const [confirm, setConfirm] = useState<{ action: RecoveryAction; revision: bigint; execution: string }>();
  const [accepted, setAccepted] = useState(false);
  const client = useQueryClient();
  const acknowledge = (value?: SessionChange) => { if (value?.session) changed(value.session); setConfirm(undefined); setAccepted(true); };
  const rename = useRetainedMutation(`session-name:${resource.id}`, SessionQuery.renameSession, (value) => { if (value.change?.session) changed(value.change.session); setName(undefined); void client.invalidateQueries({ refetchType: "active" }); });
  const prepare = useRetainedMutation(`session-prepare:${resource.id}`, SessionQuery.prepareSessionWorkspace, (value) => acknowledge(value.change));
  const workspace = useRetainedMutation(`session-workspace-recovery:${resource.id}`, SessionQuery.recoverSessionWorkspace, (value) => acknowledge(value.change));
  const recover = useRetainedMutation(`session-execution-recovery:${resource.id}`, SessionQuery.recoverSessionExecution, (value) => acknowledge(value.change));
  const operations = [rename, prepare, workspace, recover], blocked = operations.some((operation) => operation.busy || operation.uncertain);
  const request = (action: RecoveryAction) => { setAccepted(false); setConfirm({ action, revision: resource.revision, execution: executionId }); };
  const submit = () => {
    if (!confirm || blocked || confirm.revision !== resource.revision) return;
    const mutation = { id: resource.id, expectedRevision: confirm.revision, requestId: newRequestId() };
    if (confirm.action === RecoveryAction.Prepare) void prepare.send({ mutation });
    else if (confirm.action === RecoveryAction.Execution) void recover.send({ mutation, expectedExecutionId: confirm.execution });
    else void workspace.send({ mutation, cleanup: confirm.action === RecoveryAction.CleanupWorkspace });
  };
  const recoveryActions = <div className="actions">{!sidechat && data.archive === "active" && ["failed", "canceled"].includes(text(preparation.state)) && data.recovery === "none" ? <button disabled={blocked} onClick={event => { request(RecoveryAction.Prepare); openRecovery?.(event.currentTarget); }}>{copy("session-tools.prepareWorkspaceAgain_3a819b")}</button> : null}{!sidechat && data.archive !== "archived" && preparation.state === "uncertain" ? <><button disabled={blocked} onClick={event => { request(RecoveryAction.InspectWorkspace); openRecovery?.(event.currentTarget); }}>{copy("session-tools.inspectOriginalWorkspaceRecovery_b53ede")}</button><button disabled={blocked} onClick={event => { request(RecoveryAction.CleanupWorkspace); openRecovery?.(event.currentTarget); }}>{copy("session-tools.cleanIncompletePreparation_bc39ad")}</button></> : null}{data.archive !== "archived" && data.recovery === "required" && executionId ? <button disabled={blocked} onClick={event => { request(RecoveryAction.Execution); openRecovery?.(event.currentTarget); }}>{copy("session-tools.reconcileOriginalExecution_71e689")}</button> : null}</div>;
  const body = <Disclosure className="session-tools" open={initiallyOpen}><DisclosureSummary>{initiallyOpen ? copy("session.statusAndRecovery") : copy("session-tools.sessionDetailsAndRecovery_8025d7")}</DisclosureSummary><p><LocalizedText id="session-tools.session_f705f3" components={{ s0: <>{resource.id}</> }} /></p><p><LocalizedText id="session-tools.workspacePreparationRecoveryDispatch_aeed5c" components={{ s0: <>{statusLabel(text(preparation.state)) || copy("session-tools.extra.6051f94ab1ce")}</>, s1: <>{statusLabel(text(data.recovery))}</>, s2: <>{statusLabel(text(data.dispatch))}</> }} /></p><button disabled={blocked} onClick={() => setName({ value: resourceName(resource), revision: resource.revision })}>{copy("session-tools.renameSession_2cad07")}</button>
    {name ? <form onSubmit={(event) => { event.preventDefault(); if (blocked || name.revision !== resource.revision) return; void rename.send({ mutation: { id: resource.id, expectedRevision: name.revision, requestId: newRequestId() }, name: name.value }); }}><label>{copy("session-tools.sessionName_136a71")}<input required maxLength={256} value={name.value} disabled={blocked} onChange={(event) => setName({ ...name, value: event.target.value })} /></label>{name.revision !== resource.revision ? <p role="alert">{copy("session-tools.thisSessionChangedWhileEditingThe_e72d40")}</p> : null}<button disabled={blocked || name.revision !== resource.revision}>{copy("session-tools.saveSessionName_354602")}</button><button type="button" disabled={blocked} onClick={() => setName(undefined)}>{copy("session-tools.cancelRename_3fe542")}</button></form> : null}
    {launcherTarget ? null : recoveryActions}

    {confirm ? <div className="notice"><p>{confirm.action === RecoveryAction.Prepare ? copy("session-tools.retryPreparationUsingThisSessionS_ac022c") : confirm.action === RecoveryAction.CleanupWorkspace ? copy("session-tools.allowTheOwningWorkerToClean_a165e0") : confirm.action === RecoveryAction.Execution ? copy("session-tools.inspectTheOriginalExecutionSRetained_6b6a1d") : copy("session-tools.inspectTheOriginalPreparationWithoutPermitting_f893b8")}</p>{confirm.revision !== resource.revision ? <p role="alert">{copy("session-tools.theSessionChangedAfterThisAction_98d778")}</p> : null}<button disabled={blocked || confirm.revision !== resource.revision} onClick={submit}>{copy("session-tools.confirmSelectedRecoveryAction_958097")}</button><button disabled={blocked} onClick={event => { const summary = event.currentTarget.closest("details")?.querySelector("summary"); setConfirm(undefined); summary?.focus(); }}>{copy("session-tools.cancelRecoveryAction_1e7a94")}</button></div> : null}
    {accepted ? <p>{copy("session-tools.operationAcceptedItsWorkerResultAnd_4c122d")}</p> : null}{operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="session-tools.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("session-tools.rename_9e8486") : index === 1 ? copy("session-tools.preparation_1d6f04") : index === 2 ? copy("session-tools.workspaceRecovery_6db3cd") : copy("session-tools.executionRecovery_b709f6")}</> }} /></button> : null}</div>)}
    {text(preparation.job_id) ? <RetainedJob id={text(preparation.job_id)} title={copy("session-tools.workspacePreparationOperation_7d976d")} /> : null}{text(preparation.recovery_job_id) ? <RetainedJob id={text(preparation.recovery_job_id)} title={copy("session-tools.workspaceRecoveryOperation_1c66c0")} /> : null}{text(data.execution_recovery_job_id) ? <RetainedJob id={text(data.execution_recovery_job_id)} title={copy("session-tools.executionRecoveryOperation_8fc7ad")} /> : null}
  </Disclosure>;
  // Only the presentation moves. Drafts, confirmations and original retained
  // requests stay with this one mounted controller.
  return <>{target === undefined ? body : createPortal(body, host)}{launcherTarget ? createPortal(recoveryActions, launcherTarget) : null}</>;
}
