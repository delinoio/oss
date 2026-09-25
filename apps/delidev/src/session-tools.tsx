import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId, type Resource, type SessionChange } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

enum RecoveryAction { Prepare = "prepare", InspectWorkspace = "inspect-workspace", CleanupWorkspace = "cleanup-workspace", Execution = "execution" }
function RetainedJob({ id, title }: { id: string; title: string }) {
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id });
  return <section><h4>{title}</h4><Problem error={result.error} />{result.data?.resource ? <TrackedJob initial={result.data.resource} active /> : <p>Loading retained operation…</p>}</section>;
}
export function SessionTools({ resource, changed }: { resource: Resource; changed: (resource: Resource) => void }) {
  const data = document(resource), preparation = object(data.preparation), execution = object(data.execution);
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
  const request = (action: RecoveryAction) => { setAccepted(false); setConfirm({ action, revision: resource.revision, execution: text(execution.execution_id) }); };
  const submit = () => {
    if (!confirm || blocked || confirm.revision !== resource.revision) return;
    const mutation = { id: resource.id, expectedRevision: confirm.revision, requestId: newRequestId() };
    if (confirm.action === RecoveryAction.Prepare) void prepare.send({ mutation });
    else if (confirm.action === RecoveryAction.Execution) void recover.send({ mutation, expectedExecutionId: confirm.execution });
    else void workspace.send({ mutation, cleanup: confirm.action === RecoveryAction.CleanupWorkspace });
  };
  return <details className="session-tools"><summary>Session details and recovery</summary><p>Session {resource.id}</p><p>Workspace preparation: {text(preparation.state) || "Not prepared"} · Recovery: {text(data.recovery)} · Dispatch: {text(data.dispatch)}</p><button disabled={blocked} onClick={() => setName({ value: resourceName(resource), revision: resource.revision })}>Rename session</button>
    {name ? <form onSubmit={(event) => { event.preventDefault(); if (blocked || name.revision !== resource.revision) return; void rename.send({ mutation: { id: resource.id, expectedRevision: name.revision, requestId: newRequestId() }, name: name.value }); }}><label>Session name<input required maxLength={256} value={name.value} disabled={blocked} onChange={(event) => setName({ ...name, value: event.target.value })} /></label>{name.revision !== resource.revision ? <p role="alert">This session changed while editing. The name draft is retained; cancel and reopen before saving.</p> : null}<button disabled={blocked || name.revision !== resource.revision}>Save session name</button><button type="button" disabled={blocked} onClick={() => setName(undefined)}>Cancel rename</button></form> : null}
    <div className="actions">{data.archive === "active" && ["failed", "canceled"].includes(text(preparation.state)) && data.recovery === "none" ? <button disabled={blocked} onClick={() => request(RecoveryAction.Prepare)}>Prepare workspace again</button> : null}{data.archive !== "archived" && preparation.state === "uncertain" ? <><button disabled={blocked} onClick={() => request(RecoveryAction.InspectWorkspace)}>Inspect original workspace recovery</button><button disabled={blocked} onClick={() => request(RecoveryAction.CleanupWorkspace)}>Clean incomplete preparation</button></> : null}{data.archive !== "archived" && data.recovery === "required" && text(execution.execution_id) ? <button disabled={blocked} onClick={() => request(RecoveryAction.Execution)}>Reconcile original execution</button> : null}</div>
    {confirm ? <div className="notice"><p>{confirm.action === RecoveryAction.Prepare ? "Retry preparation using this session's original workspace selection. Normal server dispatch eligibility applies after successful preparation." : confirm.action === RecoveryAction.CleanupWorkspace ? "Allow the owning Worker to clean only incomplete managed preparation from the original operation. Ready content and original Local checkouts are preserved. Successful recovery remains paused." : confirm.action === RecoveryAction.Execution ? "Inspect the original execution's retained Worker report, native checkpoint and cleanup evidence. This does not resend input or resume execution; missing proof remains a recovery problem." : "Inspect the original preparation without permitting incomplete-workspace cleanup. Successful recovery remains paused until an explicit Resume."}</p>{confirm.revision !== resource.revision ? <p role="alert">The session changed after this action was selected. Cancel and inspect its current state first.</p> : null}<button disabled={blocked || confirm.revision !== resource.revision} onClick={submit}>Confirm selected recovery action</button><button disabled={blocked} onClick={() => setConfirm(undefined)}>Cancel recovery action</button></div> : null}
    {accepted ? <p>Operation accepted. Its Worker result and cleanup remain separate; follow the retained operation below.</p> : null}{operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}>Retry the same {index === 0 ? "rename" : index === 1 ? "preparation" : index === 2 ? "workspace recovery" : "execution recovery"}</button> : null}</div>)}
    {text(preparation.job_id) ? <RetainedJob id={text(preparation.job_id)} title="Workspace preparation operation" /> : null}{text(preparation.recovery_job_id) ? <RetainedJob id={text(preparation.recovery_job_id)} title="Workspace recovery operation" /> : null}{text(data.execution_recovery_job_id) ? <RetainedJob id={text(data.execution_recovery_job_id)} title="Execution recovery operation" /> : null}
  </details>;
}
