import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { Code, ConnectError } from "@connectrpc/connect";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionDeletionState, SessionQuery, SystemCapability, SystemQuery, WorkspaceStorageAction, WorkspaceStorageQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { JobState } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, Modal, Problem  } from "./ui";

const Context = createContext<((source: Resource) => void) | undefined>(undefined);
const terminal = (state: string) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState);
const labels: Partial<Record<WorkspaceStorageAction, string>> = { get [WorkspaceStorageAction.PREVIEW]() { return copy("session-storage.previewWorkspaceUsage_3fc644"); }, get [WorkspaceStorageAction.CREATE]() { return copy("session-storage.createWorkspaceSnapshot_682586"); }, get [WorkspaceStorageAction.CLEANUP]() { return copy("session-storage.storeAndCleanWorkspace_20141a"); }, get [WorkspaceStorageAction.INSPECT]() { return copy("session-storage.inspectSnapshot_10027a"); }, get [WorkspaceStorageAction.RESTORE]() { return copy("session-storage.restoreWorkspace_ae0701"); }, get [WorkspaceStorageAction.DELETE]() { return copy("session-storage.permanentlyDeleteSnapshot_9e6a7c"); }, get [WorkspaceStorageAction.RECOVER]() { return copy("session-storage.reconcileOriginalStorageOperation_a6ba97"); } };
function bytes(value: unknown): string {
  if (typeof value !== "string" || !/^(0|[1-9][0-9]*)$/.test(value)) return copy("session-storage.extra.b764cdc0eab7");
  return copy("session-storage.sentence.4c59147c97a5", { v0: BigInt(value).toLocaleString(displayLocale()) });
}
export function SessionStorageAction({ source }: { source: Resource }) {
  useLocale();
  const open = useContext(Context);
  return open ? <button type="button" onClick={() => open(source)}>{copy("session-storage.workspaceStorageAndPermanentDeletion_d87c74")}</button> : null;
}
interface Confirmation { action: WorkspaceStorageAction; revision: bigint; snapshotId?: string; previewJobId?: string; recoveryJobId?: string }

// The connection owns this controller, including operations for sessions whose
// resources disappear during deletion. Modal close and navigation retain work.
export function SessionStorageProvider({ children }: { children: ReactNode }) {
  useLocale();
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
  return <Context.Provider value={show}>{children}{source && visible ? <Modal title={copy("session-storage.workspaceStorageAndPermanentDeletion_d87c74")} close={() => setVisible(false)}>
    <p><LocalizedText id="session-storage.session_37c76b" components={{ s0: <>{resourceName(source)}</>, s1: <>{source.id}</> }} /></p>
    {deletionAccepted || sourceMissing ? <section aria-label={copy("session-storage.permanentDeletionOperation_555bb6")}><p role="status">{deleted ? copy("session-storage.permanentDeletionCompleted_aa8dac") : sourceMissing && !deletion.data?.job ? copy("session-storage.thisSessionIsNoLongerAvailable_53b083") : copy("session-storage.permanentDeletionIsPendingOriginalWorker_b121dd")}</p>{deletion.data?.job ? <><p><LocalizedText id="session-storage.workersPendingDatabaseRemovedBackupsRemoved_a7bdf9" components={{ s0: <>{deletion.data.job.workersPending}</>, s1: <>{String(deletion.data.job.databaseRemoved)}</>, s2: <>{String(deletion.data.job.backupsRemoved)}</> }} /></p><small>{deletion.data.job.id}</small></> : null}<Problem error={deletion.error}/><button disabled={deletion.isFetching} onClick={() => void deletion.refetch()}>{copy("session-storage.refreshPermanentDeletion_ea2afc")}</button>{deleted ? <button onClick={reset}>{copy("session-storage.finishDeletionOperation_2d315d")}</button> : sourceMissing && !blocked ? <button onClick={reset}>{copy("session-storage.finishStorageView_4b5b52")}</button> : null}</section> : <>
      <Problem error={current.error}/>
      {sidechat ? <p>{copy("session-storage.sidechatReferencesTheParentSCurrent_00ff51")}</p> : data.workspace === "local" ? <p>{copy("session-storage.originalLocalCheckoutsUseYourOwn_cf2eb0")}</p> : storageSupported ? <section aria-label={copy("session-storage.workspaceStorage_3107f9")}><p><LocalizedText id="session-storage.workspaceStopWorkAndWaitFor_674acb" components={{ s0: <>{text(storage.state) || "present"}</> }} /></p><div className="actions"><button disabled={actionsBlocked || storage.state === "stored"} onClick={() => select(WorkspaceStorageAction.PREVIEW)}>{copy("session-storage.previewWorkspaceUsage_3fc644")}</button><button disabled={actionsBlocked || storage.state === "stored"} onClick={() => select(WorkspaceStorageAction.CREATE)}>{copy("session-storage.createWorkspaceSnapshot_682586")}</button>{previewReady ? <button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.CLEANUP)}>{copy("session-storage.storeAndCleanWorkspace_20141a")}</button> : null}{state === JobState.Uncertain || storage.state === "uncertain" ? <button disabled={blocked || current.isFetching || !recoveryJobId} onClick={() => { if(session) setConfirm({action:WorkspaceStorageAction.RECOVER,revision:session.revision,recoveryJobId}); }}>{copy("session-storage.reconcileOriginalStorageOperation_a6ba97")}</button> : null}</div></section> : <p>{copy("session-storage.updateTheConnectedServerToManage_37cc25")}</p>}
      {!sidechat && data.workspace !== "local" && storageSupported ? <section aria-label={copy("session-storage.workspaceSnapshots_626fda")}><h3>{copy("session-storage.workspaceSnapshots_626fda")}</h3><Problem error={snapshots.error}/>{snapshots.data?.resources.filter((row) => row.kind === EntityKind.SNAPSHOT && row.sessionId === source.id && !document(row).deleted).map((row) => <article key={row.id}><p>{row.id} · {bytes(document(row).size_bytes)} · {text(document(row).created_at)}</p><div className="actions"><button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.INSPECT,row.id)}>{copy("session-storage.inspectSnapshot_10027a")}</button>{storage.state === "stored" && storage.snapshot_id === row.id ? <button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.RESTORE,row.id)}>{copy("session-storage.restoreWorkspace_ae0701")}</button> : <button disabled={actionsBlocked} onClick={() => select(WorkspaceStorageAction.DELETE,row.id)}>{copy("session-storage.permanentlyDeleteSnapshot_9e6a7c")}</button>}</div></article>)}<button disabled={snapshots.isFetching || previous.length===0} onClick={() => {setPage(previous.at(-1) ?? "");setPrevious(previous.slice(0,-1));}}>{copy("session-storage.previousSnapshots_1ead42")}</button><button disabled={snapshots.isFetching || !snapshots.data?.nextPageToken} onClick={() => {setPrevious([...previous,page]);setPage(snapshots.data?.nextPageToken ?? "");}}>{copy("session-storage.nextSnapshots_28f079")}</button></section> : null}
      {deleteSupported ? <button disabled={blocked || current.isFetching || !session} onClick={() => {setConfirm(undefined);setDeletionRevision(session?.revision);}}>{copy("session-storage.permanentlyDeleteSession_c390e7")}</button> : <p>{copy("session-storage.updateTheConnectedServerToPermanently_ca2cef")}</p>}
      {deletionRevision !== undefined ? <section aria-label={copy("session-storage.confirmPermanentSessionDeletion_86b2f8")}><p>{copy("session-storage.permanentlyDeleteThisSessionSManaged_7619eb")}</p>{session?.revision !== deletionRevision ? <p role="alert">{copy("session-storage.theSessionChangedCancelAndInspect_6b4daf")}</p> : null}<button disabled={blocked || current.isFetching || session?.revision !== deletionRevision} onClick={() => void remove.send({mutation:{id:source.id,expectedRevision:deletionRevision,requestId:newRequestId()}})}>{copy("session-storage.confirmPermanentSessionDeletion_86b2f8")}</button><button disabled={blocked} onClick={() => setDeletionRevision(undefined)}>{copy("session-storage.cancelDeletionConfirmation_f5eef6")}</button></section> : null}
    </>}
    {!deletionAccepted && job ? <section aria-label={copy("session-storage.workspaceStorageOperation_1114b9")}><p role="status"><LocalizedText id="session-storage.storageOperationAcceptanceDoesNotEstablish_249e0b" components={{ s0: <>{state}</> }} /></p><small>{job.id}</small><Problem error={operation.error}/>{text(object(document(job).problem).message) ? <ServiceProblem code={text(object(document(job).problem).code) || text(object(document(job).problem).problem_code)}><p role="alert">{text(object(document(job).problem).message)} {text(object(document(job).problem).guidance)}</p></ServiceProblem> : null}{state===JobState.Succeeded ? <><p><LocalizedText id="session-storage.sourceRetainedSnapshotsRemovedSource_3728d6" components={{ s0: <>{bytes(output.source_bytes)}</>, s1: <>{bytes(output.retained_snapshot_bytes)}</>, s2: <>{bytes(output.removed_source_bytes)}</> }} /></p><p><LocalizedText id="session-storage.filesystemFreeBeforeAfterLogicalBytes_ba72f3" components={{ s0: <>{bytes(output.free_bytes_before)}</>, s1: <>{bytes(output.free_bytes_after)}</> }} /></p><p><LocalizedText id="session-storage.nativeCleanupVerifiedWorkspace_372ea5" components={{ s0: <>{String(output.cleanup_verified===true)}</>, s1: <>{text(output.workspace_state)}</> }} /></p></> : null}<button disabled={operation.isFetching} onClick={() => void operation.refetch()}>{copy("session-storage.refreshStorageOperation_81ef5d")}</button>{[JobState.Queued,JobState.Claimed].includes(state as JobState) && !deletionAccepted ? <><p>{copy("session-storage.cancelKeepsTheOriginalOperationCancellation_29934d")}</p><button disabled={blocked} onClick={() => void cancel.send({mutation:{id:job.id,expectedRevision:job.revision,requestId:newRequestId()}})}>{copy("session-storage.cancelOriginalStorageOperation_908522")}</button></> : null}</section> : !deletionAccepted && operationId ? <><p>{copy("session-storage.loadingOriginalStorageOperation_7b5e3f")}</p><Problem error={operation.error}/></> : null}
    {confirm ? <section aria-label={copy("session-storage.confirmWorkspaceStorage_0b1544")}><p><LocalizedText id="session-storage.usingSessionRevision_f098b3" components={{ s0: <>{labels[confirm.action]}</>, s1: <>{confirm.revision.toString()}</> }} /></p>{confirm.action===WorkspaceStorageAction.CLEANUP ? <p><LocalizedText id="session-storage.createAndVerifyASnapshotOf_2e5244" components={{ s0: <>{confirm.previewJobId}</> }} /></p> : confirm.action===WorkspaceStorageAction.DELETE ? <p>{copy("session-storage.permanentlyRemoveTheSelectedSnapshotThis_14ff51")}</p> : confirm.action===WorkspaceStorageAction.RESTORE ? <p>{copy("session-storage.restoreTheExactCleanupSnapshotThe_132eb3")}</p> : confirm.action===WorkspaceStorageAction.RECOVER ? <p>{copy("session-storage.inspectAndSettleTheOriginalUncertain_7e42c6")}</p> : <p>{copy("session-storage.useTheOriginalOwningRunnerDevice_9892d8")}</p>}{session?.revision!==confirm.revision ? <p role="alert">{copy("session-storage.theSessionChangedCancelAndInspect_6b4daf")}</p> : null}<button disabled={blocked || current.isFetching || session?.revision!==confirm.revision} onClick={submit}>{copy("session-storage.confirmSelectedStorageAction_46af18")}</button><button disabled={blocked} onClick={() => setConfirm(undefined)}>{copy("session-storage.cancelStorageConfirmation_2f009f")}</button></section> : null}
    {mutations.map((mutation,index)=><div key={index}><Problem error={mutation.error}/>{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}><LocalizedText id="session-storage.retryTheSameRequest_723e0c" components={{ s0: <>{index===0 ? copy("session-storage.storage_49a25f") : index===1 ? copy("session-storage.storageCancellation_4fbc29") : copy("session-storage.permanentDeletion_082b6b")}</> }} /></button> : null}</div>)}
    {!blocked && !operationPending && !deletionAccepted && !sourceMissing ? <button onClick={reset}>{copy("session-storage.finishStorageView_4b5b52")}</button> : null}
    {text(input.action) ? <small><LocalizedText id="session-storage.originalAction_319e9b" components={{ s0: <>{text(input.action)}</> }} /></small> : null}
  </Modal> : null}{source && !visible ? <button className="notice" onClick={() => setVisible(true)}>{copy("session-storage.returnToRetainedStorageOrDeletion_a1fed8")}</button> : null}</Context.Provider>;
}
