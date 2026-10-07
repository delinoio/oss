import { LocalizedText, copy, useLocale } from "./localization";
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ForkPurpose, ForkWorkspace, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { JobState, OperationStatus } from "./jobs";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, Modal, Problem  } from "./ui";
import { useOpenCodeForkProfile } from "./opencode-fork-profile";

const Context = createContext<((source: Resource, purpose?: ForkPurpose) => void) | undefined>(undefined);
const sourceHarness = (resource: Resource) => text(object(object(document(resource).initial_execution).configuration).harness);
const settled = (resource: Resource) => {
  const data = document(resource), execution = object(data.execution), initial = object(data.initial_execution);
  const harness = text(object(initial.configuration).harness);
  const profile = harness === "codex" || harness === "opencode" && data.workspace === "general-chat" && !Object.keys(object(execution.subagents)).length && !Object.keys(object(execution.native_compactions)).length && !execution.latest_workspace_event_id && !execution.latest_todo_id && !execution.latest_plan_id && object(execution.observed).opencode_agent === "build";
  return resource.schemaVersion === 1 && (data.storage === undefined || object(data.storage).state === "present") && data.fork === undefined && data.archive === "active" && data.recovery === "none" && data.outcome === "succeeded" && !data.active_execution_id && !data.pending_steer_id && execution.cleanup_verified === true && !execution.unconfirmed_responses && !object(execution.waiting).user_input && !object(execution.waiting).approval && text(execution.native_turn_id) && profile;
};

export function SessionForkAction({ source }: { source: Resource }) {
  useLocale();
  const show = useContext(Context);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: Boolean(show) });
  const openCode = sourceHarness(source) === "opencode";
  const machine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: text(document(source).machine_id) }, { enabled: Boolean(show) && (openCode || sourceHarness(source) === "codex") });
  const runner = document(machine.data?.resource);
  const supported = openCode ? status.data?.capabilities.includes(SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1) && ["darwin", "linux"].includes(text(runner.os)) && Array.isArray(runner.worker_capabilities) && runner.worker_capabilities.includes("opencode-general-chat-fork-v1") : status.data?.capabilities.includes(SystemCapability.CODEX_SESSION_FORK_V1);
  const profile = useOpenCodeForkProfile(source, Boolean(show) && openCode && Boolean(settled(source)) && Boolean(supported));
  const sidechatSupported = sourceHarness(source) === "codex" && object(object(document(source).initial_execution).configuration).subscription !== true && !Object.keys(object(object(document(source).execution).subagents)).length && status.data?.capabilities.includes(SystemCapability.NATIVE_SIDECHAT_V1) && Array.isArray(runner.worker_capabilities) && runner.worker_capabilities.includes("codex-read-only-sidechat-v1");
  if (!show || !settled(source)) return null;
  return <>{supported && (!openCode || profile.data === true && !profile.isError) ? <button type="button" onClick={() => show(source)}>{copy("session-fork.forkSession_51bc41")}</button> : null}{sidechatSupported ? <button type="button" onClick={() => show(source, ForkPurpose.SIDECHAT)}>{copy("session-fork.openSidechat_20501a")}</button> : null}</>;
}

// This controller stays mounted for the connection, so navigation cannot lose
// an accepted job or prevent a late acknowledgment from being retained.
export function SessionForkProvider({ children, openSession, readLocalWorker }: { children: ReactNode; openSession: (id: string) => void; readLocalWorker?: ReadLocalWorkerProof }) {
  useLocale();
  const [source, setSource] = useState<Resource>();
  const [visible, setVisible] = useState(false);
  const [name, setName] = useState("");
  const [purpose, setPurpose] = useState(ForkPurpose.UNSPECIFIED);
  const sidechat = purpose === ForkPurpose.SIDECHAT;
  const [workspace, setWorkspace] = useState(ForkWorkspace.UNSPECIFIED);
  const [job, setJob] = useState<Resource>();
  const [invalid, setInvalid] = useState(false);
  const generation = useRef(0);
  const preflight = useRef<number | undefined>(undefined);
  const [checking, setChecking] = useState(false);
  useEffect(() => () => { generation.current += 1; preflight.current = undefined; }, []);
  const local = useLocalWorkerProof(readLocalWorker);
  const client = useQueryClient();
  const mutation = useRetainedMutation("session-fork", SessionQuery.forkSession, (response, request) => {
    if (!response.job || response.job.kind !== EntityKind.JOB || text(object(document(response.job).input).source_session_id) !== request.mutation?.id) { setInvalid(true); return; }
    setJob(response.job);
  });
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.SESSION, id: source?.id ?? "" }, { enabled: visible && Boolean(source) && !job });
  const fork = useQuery(SessionQuery.getSessionFork, { jobId: job?.id ?? "" }, { enabled: visible && Boolean(job), refetchInterval: (query) => {
    const state = text(document(query.state.data?.job).state);
    return visible && job && ![JobState.Succeeded, JobState.Failed, JobState.Canceled, JobState.Uncertain].includes(state as JobState) ? 2000 : false;
  } });
  const profile = useOpenCodeForkProfile(source, visible && Boolean(source) && sourceHarness(source!) === "opencode" && !job);
  const stale = Boolean(source && current.data?.resource && current.data.resource.revision !== source.revision);
  const blocked = mutation.busy || mutation.uncertain || local.busy || invalid;
  const invalidatePreflight = () => { generation.current += 1; preflight.current = undefined; setChecking(false); };
  const show = (resource: Resource, requestedPurpose = ForkPurpose.UNSPECIFIED) => {
    if (!source || (!blocked && !job && (source.id !== resource.id || purpose !== requestedPurpose))) { invalidatePreflight(); setSource(resource); setPurpose(requestedPurpose); setName(`${resourceName(resource)} ${requestedPurpose === ForkPurpose.SIDECHAT ? "Sidechat" : "fork"}`.slice(0, 256)); setWorkspace(ForkWorkspace.UNSPECIFIED); }
    setVisible(true);
  };
  const submit = async () => {
    if (!source || preflight.current !== undefined || blocked || job || stale || !name.trim() || !settled(source) || current.data?.resource && !settled(current.data.resource)) return;
    const originalGeneration = generation.current;
    preflight.current = originalGeneration;
    setChecking(true);
    // Bind all asynchronous checks to this draft and immutable source boundary.
    // Discard/replacement invalidates preflight; hiding retains the operation.
    const active = () => generation.current === originalGeneration && preflight.current === originalGeneration;
    const request = { mutation: { id: source.id, expectedRevision: source.revision, requestId: newRequestId() }, expectedTurnId: text(object(document(source).execution).native_turn_id), name, workspace, ...(sidechat ? { purpose: ForkPurpose.SIDECHAT } : {}) };
    try {
      if (sourceHarness(source) === "opencode") {
        const fresh = await profile.refetch();
        if (!active() || fresh.isError || fresh.data !== true) return;
      }
      const proof = workspace === ForkWorkspace.LOCAL ? await local.load(text(document(source).machine_id)) : undefined;
      if (!active() || workspace === ForkWorkspace.LOCAL && !proof) return;
      void mutation.send({ ...request, localWorkerToken: proof?.token });
    } finally {
      if (active()) { preflight.current = undefined; setChecking(false); }
    }
  };
  const resultJob = fork.data?.job ?? job;
  const state = text(document(resultJob).state);
  const problem = object(document(resultJob).problem);
  const child = state === JobState.Succeeded && fork.data?.session?.kind === EntityKind.SESSION && text(object(document(fork.data.session).fork).source_session_id) === source?.id ? fork.data.session : undefined;
  const reset = () => { invalidatePreflight(); setSource(undefined); setJob(undefined); setVisible(false); setInvalid(false); void client.invalidateQueries({ refetchType: "active" }); };
  return <Context.Provider value={show}>{children}{source && visible ? <Modal title={sidechat ? copy("session-fork.openSidechat_20501a") : copy("session-fork.forkSession_51bc41")} close={() => setVisible(false)}>
    {sidechat ? <p>{copy("session-fork.discussTheCompletedTurnWithThe_ebd3a0")}</p> : <p><LocalizedText id="session-fork.createAnIndependentConversationFromAt_76ffeb" components={{ s0: <>{resourceName(source)}</>, s1: <>{sourceHarness(source) === "opencode" ? copy("session-fork.theChildKeepsASeparateCopy_a3e97a") : copy("session-fork.sourceMessagesStayInTheirOriginal_f0fe74")}</> }} /></p>}
    <p><LocalizedText id="session-fork.sourceTurn_0471be" components={{ s0: <>{source.id}</>, s1: <>{text(object(document(source).execution).native_turn_id)}</> }} /></p>
    {!job ? <form onSubmit={(event) => { event.preventDefault(); void submit(); }}><label>{copy("session-fork.forkName_d9ad67")}<input autoFocus required maxLength={256} value={name} disabled={blocked} onChange={(event) => setName(event.target.value)} /></label>{!sidechat ? <label>{copy("session-fork.workspace_87bb59")}<select value={workspace} disabled={blocked} onChange={(event) => setWorkspace(Number(event.target.value) as ForkWorkspace)}><option value={ForkWorkspace.UNSPECIFIED}>{copy("session-fork.independentWorkspace_c606c9")}</option>{document(source).workspace === "local" ? <option value={ForkWorkspace.LOCAL} disabled={!local.available}>{copy("session-fork.shareThisComputerSLocalCheckouts_3fca80")}</option> : null}</select></label> : null}{sidechat ? <p>{copy("session-fork.sidechatStartsPausedWithNativeRead_cafd5f")}</p> : <p>{sourceHarness(source) === "opencode" ? copy("session-fork.opencodeForksSupportCompletedPlainText_35db18") : copy("session-fork.independentProjectWorkspacesCopyEveryRepository_17283a")}</p>}{stale ? <p role="alert">{copy("session-fork.theSourceChangedCloseAndDiscard_45f91e")}</p> : null}<Problem error={current.error} />{local.problem ? <p role="alert">{local.problem}</p> : null}<button disabled={blocked || checking || stale || !name.trim() || !settled(current.data?.resource ?? source) || sourceHarness(source) === "opencode" && (profile.isFetching || profile.isError || profile.data !== true)}>{sidechat ? copy("session-fork.createSidechat_664521") : copy("session-fork.createFork_d217b7")}</button>{!blocked ? <button type="button" onClick={reset}>{copy("session-fork.discardForkDraft_8920d0")}</button> : null}</form> : <div><OperationStatus state={state} />{!child ? <p>{copy("session-fork.theChildAppearsAfterNativeHistory_0f8ca6")}</p> : <button onClick={() => { reset(); openSession(child.id); }}>{copy("session-fork.openForkedSession_0a254e")}</button>}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<Problem error={fork.error} />{fork.error || state === JobState.Uncertain ? <button disabled={fork.isFetching} onClick={() => void fork.refetch()}>{copy("jobs.retryStatusRead")}</button> : null}{[JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button onClick={reset}>{copy("session-fork.finishForkOperation_c7f817")}</button> : null}</div>}
    <Problem error={mutation.error} />{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("session-fork.retryTheSameForkRequest_782e42")}</button> : null}{invalid ? <p role="alert">{copy("session-fork.theForkAcknowledgmentIsIncompleteRetain_a0f92f")}</p> : null}
  </Modal> : null}{source && !visible ? <button className="notice" onClick={() => setVisible(true)}>{copy("session-fork.returnToRetainedForkOperation_1beee5")}</button> : null}</Context.Provider>;
}
