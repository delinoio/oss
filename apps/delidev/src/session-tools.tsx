import { useSessionQuery as useQuery, useSessionActive } from "./session-activity";
import { FlatDisclosureScope } from "./disclosure";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { createPortal } from "react-dom";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";

import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId, type Resource, type SessionChange } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text, Workspace, workspaceNames } from "./documents";
import { TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

import { sessionTitlePresentation } from "./session-title";

function statusValue(value: unknown) {
  if (value == null || value === "") return copy("session-tools.notReported");
  return typeof value === "string" ? statusLabel(value) : copy("session-tools.unavailable");
}

enum RecoveryAction { Prepare = "prepare", InspectWorkspace = "inspect-workspace", CleanupWorkspace = "cleanup-workspace", Execution = "execution" }
function RetainedJob({ id, title, diagnosticsTarget, sessionId }: { id: string; title: string; sessionId: string; diagnosticsTarget?: HTMLElement | null }) {
  useLocale();
  const active = useSessionActive();
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id });
  const row = result.data?.resource, succeeded = row?.id === id && row.kind === EntityKind.JOB && row.sessionId === sessionId && row.schemaVersion === 1 && row.revision > 0n && document(row).state === "succeeded" && !text(object(document(row).problem).message) && !result.error;
  return <><section hidden={succeeded}><h4>{title}</h4><Problem error={result.error} />{row ? <TrackedJob initial={row} active={active} /> : <p>{copy("session-tools.loadingRetainedOperation_e36e04")}</p>}</section>{diagnosticsTarget ? createPortal(<section><h3>{title}</h3><p>{id} · {row?.revision.toString()} · {text(document(row).state)}</p><Problem error={result.error}/></section>, diagnosticsTarget) : null}</>;
}
export function SessionTools({ resource, changed, initiallyOpen = false, children, target, launcherTarget, openRecovery, diagnosticsTarget }: { resource: Resource; changed: (resource: Resource) => void; initiallyOpen?: boolean; children?: ReactNode; target?: HTMLElement | null; launcherTarget?: HTMLElement | null; openRecovery?: (opener: HTMLButtonElement) => void; diagnosticsTarget?: HTMLElement | null }) {
  useLocale();
  const [host] = useState(() => globalThis.document.createElement("div"));
  useLayoutEffect(() => {
    if (!target) return;
    target.append(host);
    return () => { host.remove(); };
  }, [host, target]);
  const data = document(resource), preparation = object(data.preparation), execution = object(data.execution);
  const title = sessionTitlePresentation(data);
  const initial = object(data.initial_execution);
  const sidechat = Boolean(object(data.fork).sidechat_parent_snapshot);
  const executionId = text(execution.execution_id) || (data.execution == null && data.current_execution == null && data.workspace === "worktree" ? text(initial.id) : "");
  const recoveryOpener = useRef<HTMLButtonElement>(null);
  const [confirm, setConfirm] = useState<{ action: RecoveryAction; revision: bigint; execution: string }>();
  const [accepted, setAccepted] = useState(false);
  const client = useQueryClient();
  const acknowledge = (value?: SessionChange) => { if (value?.session) changed(value.session); setConfirm(undefined); setAccepted(true); };
  const prepare = useRetainedMutation(`session-prepare:${resource.id}`, SessionQuery.prepareSessionWorkspace, (value) => acknowledge(value.change));
  const workspace = useRetainedMutation(`session-workspace-recovery:${resource.id}`, SessionQuery.recoverSessionWorkspace, (value) => acknowledge(value.change));
  const recover = useRetainedMutation(`session-execution-recovery:${resource.id}`, SessionQuery.recoverSessionExecution, (value) => acknowledge(value.change));
  const operations = [prepare, workspace, recover], blocked = operations.some((operation) => operation.busy || operation.uncertain);
  const request = (action: RecoveryAction, opener: HTMLButtonElement) => { recoveryOpener.current = opener; setAccepted(false); setConfirm({ action, revision: resource.revision, execution: executionId }); };
  const submit = () => {
    if (!confirm || blocked || confirm.revision !== resource.revision) return;
    const mutation = { id: resource.id, expectedRevision: confirm.revision, requestId: newRequestId() };
    if (confirm.action === RecoveryAction.Prepare) void prepare.send({ mutation });
    else if (confirm.action === RecoveryAction.Execution) void recover.send({ mutation, expectedExecutionId: confirm.execution });
    else void workspace.send({ mutation, cleanup: confirm.action === RecoveryAction.CleanupWorkspace });
  };
  const recoveryActions = <div className="actions">{!sidechat && data.archive === "active" && ["failed", "canceled"].includes(text(preparation.state)) && data.recovery === "none" ? <button disabled={blocked} onClick={event => { request(RecoveryAction.Prepare, event.currentTarget); openRecovery?.(event.currentTarget); }}>{copy("session-tools.prepareWorkspaceAgain_3a819b")}</button> : null}{!sidechat && data.archive !== "archived" && preparation.state === "uncertain" ? <><button disabled={blocked} onClick={event => { request(RecoveryAction.InspectWorkspace, event.currentTarget); openRecovery?.(event.currentTarget); }}>{copy("session-tools.inspectOriginalWorkspaceRecovery_b53ede")}</button><button disabled={blocked} onClick={event => { request(RecoveryAction.CleanupWorkspace, event.currentTarget); openRecovery?.(event.currentTarget); }}>{copy("session-tools.cleanIncompletePreparation_bc39ad")}</button></> : null}{data.archive !== "archived" && data.recovery === "required" && executionId ? <button disabled={blocked} onClick={event => { request(RecoveryAction.Execution, event.currentTarget); openRecovery?.(event.currentTarget); }}>{copy("session-tools.reconcileOriginalExecution_71e689")}</button> : null}</div>;
  const technical = <section className="session-information-section"><h3>{copy("session-name.sessionDetails")}</h3><dl>
      <dt>{copy("session-tools.sessionId")}</dt><dd>{resource.id} · {resource.revision.toString()}</dd>
      <dt>{copy("session-tools.dispatch")}</dt><dd>{statusValue(data.dispatch)}</dd>
      <dt>{copy("session-tools.preparation")}</dt><dd>{statusValue(preparation.state)}</dd>
      <dt>{copy("session-tools.recovery")}</dt><dd>{statusValue(data.recovery)}</dd>
      {title ? <><dt>{copy("session-tools.automaticTitle")}</dt><dd>{title.label}{title.detail ? <p>{title.detail}</p> : null}</dd></> : null}
    </dl></section>;
  const body = <FlatDisclosureScope><section className="session-tools session-information-section"><h3>{initiallyOpen ? copy("session.statusAndRecovery") : copy("session-tools.sessionDetailsAndRecovery_8025d7")}</h3><dl className="session-status-values">
      <dt>{copy("session-tools.workspace")}</dt><dd>{workspaceNames[text(data.workspace) as Workspace] || copy("session.extra.87bb59ba2f92")}</dd>
      <dt>{copy("session-tools.result")}</dt><dd>{statusValue(data.outcome)}</dd>
      <dt>{copy("session-tools.archive")}</dt><dd>{statusValue(data.archive)}</dd>
    </dl>{children}
    {launcherTarget ? null : recoveryActions}

    {confirm ? <div className="notice"><p>{confirm.action === RecoveryAction.Prepare ? copy("session-tools.retryPreparationUsingThisSessionS_ac022c") : confirm.action === RecoveryAction.CleanupWorkspace ? copy("session-tools.allowTheOwningWorkerToClean_a165e0") : confirm.action === RecoveryAction.Execution ? copy("session-tools.inspectTheOriginalExecutionSRetained_6b6a1d") : copy("session-tools.inspectTheOriginalPreparationWithoutPermitting_f893b8")}</p>{confirm.revision !== resource.revision ? <p role="alert">{copy("session-tools.theSessionChangedAfterThisAction_98d778")}</p> : null}<button disabled={blocked || confirm.revision !== resource.revision} onClick={submit}>{copy("session-tools.confirmSelectedRecoveryAction_958097")}</button><button disabled={blocked} onClick={() => { setConfirm(undefined); const opener = recoveryOpener.current; if (opener?.isConnected && !opener.disabled && !opener.closest("[hidden],[inert]")) opener.focus(); }}>{copy("session-tools.cancelRecoveryAction_1e7a94")}</button></div> : null}
    {accepted ? <p>{copy("session-tools.operationAcceptedItsWorkerResultAnd_4c122d")}</p> : null}{operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="session-tools.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("session-tools.preparation_1d6f04") : index === 1 ? copy("session-tools.workspaceRecovery_6db3cd") : copy("session-tools.executionRecovery_b709f6")}</> }} /></button> : null}</div>)}
    {text(preparation.job_id) ? <RetainedJob sessionId={resource.id} diagnosticsTarget={diagnosticsTarget} id={text(preparation.job_id)} title={copy("session-tools.workspacePreparationOperation_7d976d")} /> : null}{text(preparation.recovery_job_id) ? <RetainedJob sessionId={resource.id} diagnosticsTarget={diagnosticsTarget} id={text(preparation.recovery_job_id)} title={copy("session-tools.workspaceRecoveryOperation_1c66c0")} /> : null}{text(data.execution_recovery_job_id) ? <RetainedJob sessionId={resource.id} diagnosticsTarget={diagnosticsTarget} id={text(data.execution_recovery_job_id)} title={copy("session-tools.executionRecoveryOperation_8fc7ad")} /> : null}
  </section></FlatDisclosureScope>;
  // Only the presentation moves. Drafts, confirmations and original retained
  // requests stay with this one mounted controller.
  return <>{diagnosticsTarget ? createPortal(technical, diagnosticsTarget) : null}{target === undefined ? body : createPortal(body, host)}{launcherTarget ? createPortal(recoveryActions, launcherTarget) : null}</>;
}
