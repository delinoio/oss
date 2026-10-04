// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError } from "@connectrpc/connect";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionDeletionState, SessionQuery, SystemCapability, SystemQuery, WorkspaceStorageAction, WorkspaceStorageQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { JobState } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";

const Context = createContext<((source: Resource) => void) | undefined>(undefined);
const terminal = (state: string) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState);
const labels: Partial<Record<WorkspaceStorageAction, string>> = { [WorkspaceStorageAction.PREVIEW]: "Preview workspace usage", [WorkspaceStorageAction.CREATE]: "Create workspace snapshot", [WorkspaceStorageAction.CLEANUP]: "Store and clean workspace", [WorkspaceStorageAction.INSPECT]: "Inspect snapshot", [WorkspaceStorageAction.RESTORE]: "Restore workspace", [WorkspaceStorageAction.DELETE]: "Permanently delete snapshot", [WorkspaceStorageAction.RECOVER]: "Reconcile original storage operation" };
function bytes(value: unknown): string {
  if (typeof value !== "string" || !/^(0|[1-9][0-9]*)$/.test(value)) return "Unknown";
  return `${BigInt(value).toLocaleString()} bytes`;
}
export function SessionStorageAction({ source }: { source: Resource }) {
  const open = useContext(Context);
  return open ? <button type="button" onClick={() => open(source)}>Workspace storage and permanent deletion</button> : null;
}
interface Confirmation { action: WorkspaceStorageAction; revision: bigint; snapshotId?: string; previewJobId?: string; recoveryJobId?: string }

// The connection owns this controller, including operations for sessions whose
// resources disappear during deletion. Modal close and navigation retain work.
export function SessionStorageProvider({ children }: { children: ReactNode }) {
  const [source, setSource] = useState<Resource>();
  const [visible, setVisible] = useState(false);
  const [jobId, setJobId] = useState<string>();
  const [confirm, setConfirm] = useState<Confirmation>();
  const [deletionRevision, setDeletionRevision] = useState<bigint>();
  const [deletionAccepted, setDeletionAccepted] = useState(false);
  const [page, setPage] = useState("");
  const [previous, setPrevious] = useState<string[]>([]);
  const client = useQueryClient();
  const refresh = () => void client.invalidateQueries({ refetchType: "active" });
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: Boolean(source) && visible });
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.SESSION, id: source?.id ?? "" }, { enabled: Boolean(source) && visible && !deletionAccepted, refetchInterval: source && (jobId || text(object(document(source).storage).job_id)) ? 2000 : false });
  const sourceMissing = current.isError && !current.isFetching && ConnectError.from(current.error).code === Code.NotFound;
  const fresh = current.data?.resource;
  const session = fresh && source && fresh.id === source.id && fresh.kind === EntityKind.SESSION && fresh.revision >= source.revision ? fresh : source;
  const data = document(session), storage = object(data.storage);
  const operationId = jobId ?? text(storage.job_id);
  const operation = useQuery(WorkspaceStorageQuery.getWorkspaceStorageOperation, { id: operationId }, { enabled: Boolean(source && operationId) && !deletionAccepted && !sourceMissing, refetchInterval: (query) => source && operationId && !terminal(text(document(query.state.data?.job).state)) ? 2000 : false });
  const job = operation.data?.job?.id === operationId && operation.data.job.kind === EntityKind.JOB && operation.data.job.sessionId === source?.id ? operation.data.job : undefined;
  const state = text(document(job).state), output = object(document(job).output), input = object(document(job).input);
  // Failed/canceled recovery restores the predecessor on the server. Keep the
  // failed attempt visible, but use that refreshed anchor for the next request.
  const recoveryJobId = input.action === "recover" && [JobState.Failed, JobState.Canceled].includes(state as JobState) && storage.state === "uncertain" ? text(storage.job_id) : operationId;
  const snapshots = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.SNAPSHOT, sessionId: source?.id ?? "", pageSize: 50, pageToken: page } }, { enabled: Boolean(source) && visible && !deletionAccepted && !sourceMissing });
  const deletion = useQuery(SessionQuery.getSessionDeletion, { sessionId: source?.id ?? "" }, { enabled: Boolean(source) && (deletionAccepted || sourceMissing), refetchInterval: (query) => query.state.data?.job?.state === SessionDeletionState.SUCCEEDED ? false : 2000 });
  const deleted = Boolean(source && deletion.data?.job && deletion.data.job.sessionId === source.id && deletion.data.job.state === SessionDeletionState.SUCCEEDED);
  const request = useRetainedMutation(`workspace-storage:${source?.id ?? ""}`, WorkspaceStorageQuery.requestWorkspaceStorage, (response) => { setJobId(response.job?.id); setConfirm(undefined); refresh(); }, (response, retained) => response.job?.kind === EntityKind.JOB && response.job.sessionId === retained.mutation?.id);
  const cancel = useRetainedMutation(`workspace-storage-cancel:${source?.id ?? ""}`, WorkspaceStorageQuery.cancelWorkspaceStorageOperation, () => refresh(), (response, retained) => Boolean(response.job && retained.mutation && response.job.id === retained.mutation.id && response.job.revision >= retained.mutation.expectedRevision));
  const remove = useRetainedMutation(`session-delete:${source?.id ?? ""}`, SessionQuery.deleteSession, () => { setDeletionAccepted(true); setDeletionRevision(undefined); refresh(); }, (response, retained) => Boolean(response.job && retained.mutation && response.job.sessionId === retained.mutation.id && response.job.id));
  const mutations = [request, cancel, remove];
  const blocked = mutations.some((mutation) => mutation.busy || mutation.uncertain);
  const operationPending = Boolean(!sourceMissing && operationId && (!job || !terminal(state)));
  const reset = () => { setSource(undefined); setJobId(undefined); setConfirm(undefined); setDeletionRevision(undefined); setDeletionAccepted(false); setPage(""); setPrevious([]); setVisible(false); refresh(); };
  const show = (row: Resource) => {
    if (!source) setSource(row);
    else if (source.id !== row.id && !blocked && !operationPending && (!deletionAccepted || deleted)) { setSource(row); setJobId(undefined); setConfirm(undefined); setDeletionRevision(undefined); setDeletionAccepted(false); setPage(""); setPrevious([]); }
    setVisible(true);
  };
  useEffect(() => { if (job?.revision) void client.invalidateQueries({ refetchType: "active", predicate: (query) => !query.queryKey.includes(WorkspaceStorageQuery.getWorkspaceStorageOperation.name) }); }, [client, job?.id, job?.revision]);
  useEffect(() => { if (sourceMissing) { setConfirm(undefined); setDeletionRevision(undefined); } }, [sourceMissing]);
  const select = (action: WorkspaceStorageAction, snapshotId?: string) => {
    if (!session || blocked || operationPending || deletionAccepted || current.isFetching || current.isError) return;
    setDeletionRevision(undefined);
    setConfirm({ action, revision: session.revision, snapshotId, ...(action === WorkspaceStorageAction.CLEANUP ? { previewJobId: job?.id } : {}), ...(action === WorkspaceStorageAction.RECOVER ? { recoveryJobId } : {}) });
  };
  const submit = () => {
    if (!session || !confirm || blocked || session.revision !== confirm.revision || current.isFetching || current.isError) return;
    void request.send({ mutation: { id: session.id, expectedRevision: confirm.revision, requestId: newRequestId() }, action: confirm.action, snapshotId: confirm.snapshotId, previewJobId: confirm.previewJobId, recoveryJobId: confirm.recoveryJobId });
  };
  const sidechat = Boolean(object(data.fork).sidechat_parent_snapshot);
  const storageSupported = status.data?.capabilities.includes(SystemCapability.WORKSPACE_STORAGE_V1);
  const deleteSupported = status.data?.capabilities.includes(SystemCapability.PERMANENT_SESSION_DELETION_V1);
  const previewReady = Boolean(job && state === JobState.Succeeded && output.action === "preview" && output.cleanup_verified === true && text(output.preview_digest));
  const actionsBlocked = blocked || current.isFetching || current.isError || operationPending || deletionAccepted;
  return <Context.Provider value={show}>{children}{source && visible ? <Modal title="Workspace storage and permanent deletion" close={() => setVisible(false)}>
    <p>{resourceName(source)} · Session {source.id}</p>
    {deletionAccepted || sourceMissing ? <section aria-label="Permanent deletion operation"><p role="status">{deleted ? "Permanent deletion completed." : sourceMissing && !deletion.data?.job ? "This session is no longer available. Its deletion cleanup status is unavailable." : "Permanent deletion is pending original Worker, dependent Sidechat, database and managed backup cleanup."}</p>{deletion.data?.job ? <><p>Workers pending: {deletion.data.job.workersPending} · Database removed: {String(deletion.data.job.databaseRemoved)} · Backups removed: {String(deletion.data.job.backupsRemoved)}</p><small>{deletion.data.job.id}</small></> : null}<Problem error={deletion.error}/><button disabled={deletion.isFetching} onClick={() => void deletion.refetch()}>Refresh permanent deletion</button>{deleted ? <button onClick={reset}>Finish deletion operation</button> : sourceMissing && !blocked ? <button onClick={reset}>Finish storage view</button> : null}</section> : <>
      <Problem error={current.error}/>
      {sidechat ? <p>Sidechat references the parent's current workspace. Delete this Sidechat to remove its managed conversation and runtime while preserving parent files.</p> : data.workspace === "local" ? <p>Original Local checkouts use your own backup workflow. DeliDev does not snapshot or clean them.</p> : storageSupported ? <section aria-label="Workspace storage"><p>Workspace: {text(storage.state) || "present"}. Stop work and wait for terminal, forwarding and native cleanup before storage operations. Archive preserves files.</p><div className="actions"><button disabled={actionsBlocked || storage.state === "stored"} onClick={() => select(WorkspaceStorageAction.PREVIEW)}>Preview workspace usage</button><button disabled={actionsBlocked || storage.state === "stored"} onClick={() => select(WorkspaceStorageAction.CREATE)}>Create workspace snapshot</button>{previewReady ? <button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.CLEANUP)}>Store and clean workspace</button> : null}{state === JobState.Uncertain || storage.state === "uncertain" ? <button disabled={blocked || current.isFetching || !recoveryJobId} onClick={() => { if(session) setConfirm({action:WorkspaceStorageAction.RECOVER,revision:session.revision,recoveryJobId}); }}>Reconcile original storage operation</button> : null}</div></section> : <p>Update the connected server to manage workspace storage.</p>}
      {!sidechat && data.workspace !== "local" && storageSupported ? <section aria-label="Workspace snapshots"><h3>Workspace snapshots</h3><Problem error={snapshots.error}/>{snapshots.data?.resources.filter((row) => row.kind === EntityKind.SNAPSHOT && row.sessionId === source.id && !document(row).deleted).map((row) => <article key={row.id}><p>{row.id} · {bytes(document(row).size_bytes)} · {text(document(row).created_at)}</p><div className="actions"><button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.INSPECT,row.id)}>Inspect snapshot</button>{storage.state === "stored" && storage.snapshot_id === row.id ? <button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.RESTORE,row.id)}>Restore workspace</button> : <button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.DELETE,row.id)}>Permanently delete snapshot</button>}</div></article>)}<button disabled={snapshots.isFetching || previous.length===0} onClick={() => {setPage(previous.at(-1) ?? "");setPrevious(previous.slice(0,-1));}}>Previous snapshots</button><button disabled={snapshots.isFetching || !snapshots.data?.nextPageToken} onClick={() => {setPrevious([...previous,page]);setPage(snapshots.data?.nextPageToken ?? "");}}>Next snapshots</button></section> : null}
      {deleteSupported ? <button disabled={blocked || current.isFetching || !session} onClick={() => {setConfirm(undefined);setDeletionRevision(session?.revision);}}>Permanently delete session</button> : <p>Update the connected server to permanently delete managed sessions.</p>}
      {deletionRevision !== undefined ? <section aria-label="Confirm permanent session deletion"><p>Permanently delete this session's managed conversation, native runtimes, workspace snapshots, managed backups. Running work will be stopped. Original Local checkouts and independent Forks remain. This cannot be undone or canceled.</p>{session?.revision !== deletionRevision ? <p role="alert">The session changed. Cancel and inspect its current state.</p> : null}<button disabled={blocked || current.isFetching || session?.revision !== deletionRevision} onClick={() => void remove.send({mutation:{id:source.id,expectedRevision:deletionRevision,requestId:newRequestId()}})}>Confirm permanent session deletion</button><button disabled={blocked} onClick={() => setDeletionRevision(undefined)}>Cancel deletion confirmation</button></section> : null}
    </>}
    {!deletionAccepted && job ? <section aria-label="Workspace storage operation"><p role="status">Storage operation: {state}. Acceptance does not establish native cleanup.</p><small>{job.id}</small><Problem error={operation.error}/>{text(object(document(job).problem).message) ? <p role="alert">{text(object(document(job).problem).message)} {text(object(document(job).problem).guidance)}</p> : null}{state===JobState.Succeeded ? <><p>Source: {bytes(output.source_bytes)} · Retained snapshots: {bytes(output.retained_snapshot_bytes)} · Removed source: {bytes(output.removed_source_bytes)}</p><p>Filesystem free before: {bytes(output.free_bytes_before)} · After: {bytes(output.free_bytes_after)}. Logical bytes do not prove reclaimed physical space.</p><p>Native cleanup verified: {String(output.cleanup_verified===true)} · Workspace: {text(output.workspace_state)}</p></> : null}<button disabled={operation.isFetching} onClick={() => void operation.refetch()}>Refresh storage operation</button>{[JobState.Queued,JobState.Claimed].includes(state as JobState) && !deletionAccepted ? <><p>Cancel keeps the original operation. Cancellation cannot undo an already committed native effect.</p><button disabled={blocked} onClick={() => void cancel.send({mutation:{id:job.id,expectedRevision:job.revision,requestId:newRequestId()}})}>Cancel original storage operation</button></> : null}</section> : !deletionAccepted && operationId ? <><p>Loading original storage operation…</p><Problem error={operation.error}/></> : null}
    {confirm ? <section aria-label="Confirm workspace storage"><p>{labels[confirm.action]} using session revision {confirm.revision.toString()}.</p>{confirm.action===WorkspaceStorageAction.CLEANUP ? <p>Create and verify a snapshot of every managed repository, then remove the source workspace only if it still matches preview {confirm.previewJobId}. Original independent cleanup must complete first.</p> : confirm.action===WorkspaceStorageAction.DELETE ? <p>Permanently remove the selected snapshot. This cannot be undone.</p> : confirm.action===WorkspaceStorageAction.RESTORE ? <p>Restore the exact cleanup snapshot. The session stays paused until explicit Resume.</p> : confirm.action===WorkspaceStorageAction.RECOVER ? <p>Inspect and settle the original uncertain operation without replaying its native side effect. Missing ownership stays uncertain.</p> : <p>Use the original owning Runner Device. Snapshot creation preserves source files and does not retire Sidechats.</p>}{session?.revision!==confirm.revision ? <p role="alert">The session changed. Cancel and inspect its current state.</p> : null}<button disabled={blocked || current.isFetching || session?.revision!==confirm.revision} onClick={submit}>Confirm selected storage action</button><button disabled={blocked} onClick={() => setConfirm(undefined)}>Cancel storage confirmation</button></section> : null}
    {mutations.map((mutation,index)=><div key={index}><Problem error={mutation.error}/>{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry the same {index===0 ? "storage" : index===1 ? "storage cancellation" : "permanent deletion"} request</button> : null}</div>)}
    {!blocked && !operationPending && !deletionAccepted && !sourceMissing ? <button onClick={reset}>Finish storage view</button> : null}
    {text(input.action) ? <small>Original action: {text(input.action)}</small> : null}
  </Modal> : null}{source && !visible ? <button className="notice" onClick={() => setVisible(true)}>Return to retained storage or deletion operation</button> : null}</Context.Provider>;
}
