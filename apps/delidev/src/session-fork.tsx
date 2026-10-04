// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ForkWorkspace, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { JobState } from "./jobs";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import { useOpenCodeForkProfile } from "./opencode-fork-profile";

const Context = createContext<((source: Resource) => void) | undefined>(undefined);
const sourceHarness = (resource: Resource) => text(object(object(document(resource).initial_execution).configuration).harness);
const settled = (resource: Resource) => {
  const data = document(resource), execution = object(data.execution), initial = object(data.initial_execution);
  const harness = text(object(initial.configuration).harness);
  const profile = harness === "codex" || harness === "opencode" && data.workspace === "general-chat" && !Object.keys(object(execution.subagents)).length && !Object.keys(object(execution.native_compactions)).length && !execution.latest_workspace_event_id && !execution.latest_todo_id && !execution.latest_plan_id && object(execution.observed).opencode_agent === "build";
  return resource.schemaVersion === 1 && data.fork === undefined && data.archive === "active" && data.recovery === "none" && data.outcome === "succeeded" && !data.active_execution_id && !data.pending_steer_id && execution.cleanup_verified === true && !execution.unconfirmed_responses && !object(execution.waiting).user_input && !object(execution.waiting).approval && text(execution.native_turn_id) && profile;
};

export function SessionForkAction({ source }: { source: Resource }) {
  const show = useContext(Context);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: Boolean(show) });
  const openCode = sourceHarness(source) === "opencode";
  const machine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: text(document(source).machine_id) }, { enabled: Boolean(show) && openCode });
  const runner = document(machine.data?.resource);
  const supported = openCode ? status.data?.capabilities.includes(SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1) && ["darwin", "linux"].includes(text(runner.os)) && Array.isArray(runner.worker_capabilities) && runner.worker_capabilities.includes("opencode-general-chat-fork-v1") : status.data?.capabilities.includes(SystemCapability.CODEX_SESSION_FORK_V1);
  const profile = useOpenCodeForkProfile(source, Boolean(show) && openCode && Boolean(settled(source)) && Boolean(supported));
  if (!show || !settled(source) || !supported || openCode && (profile.data !== true || profile.isError)) return null;
  return <button type="button" onClick={() => show(source)}>Fork session</button>;
}

// This controller stays mounted for the connection, so navigation cannot lose
// an accepted job or prevent a late acknowledgment from being retained.
export function SessionForkProvider({ children, openSession, readLocalWorker }: { children: ReactNode; openSession: (id: string) => void; readLocalWorker?: ReadLocalWorkerProof }) {
  const [source, setSource] = useState<Resource>();
  const [visible, setVisible] = useState(false);
  const [name, setName] = useState("");
  const [workspace, setWorkspace] = useState(ForkWorkspace.UNSPECIFIED);
  const [job, setJob] = useState<Resource>();
  const [invalid, setInvalid] = useState(false);
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
  const show = (resource: Resource) => {
    if (!source || (!blocked && !job && source.id !== resource.id)) { setSource(resource); setName(`${resourceName(resource)} fork`.slice(0, 256)); setWorkspace(ForkWorkspace.UNSPECIFIED); }
    setVisible(true);
  };
  const submit = async () => {
    if (!source || blocked || job || stale || !name.trim() || !settled(source)) return;
    if (sourceHarness(source) === "opencode") {
      const fresh = await profile.refetch();
      if (fresh.isError || fresh.data !== true) return;
    }
    const request = { mutation: { id: source.id, expectedRevision: source.revision, requestId: newRequestId() }, expectedTurnId: text(object(document(source).execution).native_turn_id), name, workspace };
    const proof = workspace === ForkWorkspace.LOCAL ? await local.load(text(document(source).machine_id)) : undefined;
    if (workspace === ForkWorkspace.LOCAL && !proof) return;
    void mutation.send({ ...request, localWorkerToken: proof?.token });
  };
  const resultJob = fork.data?.job ?? job;
  const state = text(document(resultJob).state);
  const problem = object(document(resultJob).problem);
  const child = state === JobState.Succeeded && fork.data?.session?.kind === EntityKind.SESSION && text(object(document(fork.data.session).fork).source_session_id) === source?.id ? fork.data.session : undefined;
  const reset = () => { setSource(undefined); setJob(undefined); setVisible(false); setInvalid(false); void client.invalidateQueries({ refetchType: "active" }); };
  return <Context.Provider value={show}>{children}{source && visible ? <Modal title="Fork session" close={() => setVisible(false)}>
    <p>Create an independent conversation from {resourceName(source)} at its completed turn, using the same computer, account and settings. {sourceHarness(source) === "opencode" ? "The child keeps a separate copy of this conversation and its General Chat files." : "Source messages stay in their original session; the new native conversation retains that context."}</p>
    <p>Source: {source.id} · Turn: {text(object(document(source).execution).native_turn_id)}</p>
    {!job ? <form onSubmit={(event) => { event.preventDefault(); void submit(); }}><label>Fork name<input autoFocus required maxLength={256} value={name} disabled={blocked} onChange={(event) => setName(event.target.value)} /></label><label>Workspace<select value={workspace} disabled={blocked} onChange={(event) => setWorkspace(Number(event.target.value) as ForkWorkspace)}><option value={ForkWorkspace.UNSPECIFIED}>Independent workspace</option>{document(source).workspace === "local" ? <option value={ForkWorkspace.LOCAL} disabled={!local.available}>Share this computer's Local checkouts</option> : null}</select></label><p>{sourceHarness(source) === "opencode" ? "OpenCode forks support completed plain-text General Chat on macOS and Linux. The child starts paused; add a new message and Resume when ready." : "Independent project workspaces copy every repository's current commit and files. Local sharing is available for user-owned Local checkouts and requires proof from this computer's Worker."}</p>{stale ? <p role="alert">The source changed. Close and discard this draft, then inspect the current turn before another fork.</p> : null}<Problem error={current.error} />{local.problem ? <p role="alert">{local.problem}</p> : null}<button disabled={blocked || stale || !name.trim() || sourceHarness(source) === "opencode" && (profile.isFetching || profile.isError || profile.data !== true)}>Create fork</button>{!blocked ? <button type="button" onClick={reset}>Discard fork draft</button> : null}</form> : <section aria-label="Fork operation"><p role="status">Fork operation: {state || "Accepted"}</p><small>{job.id}</small>{!child ? <p>The child appears after native history, every workspace and process cleanup are verified.</p> : <button onClick={() => { reset(); openSession(child.id); }}>Open forked session</button>}{text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}<Problem error={fork.error} /><button disabled={fork.isFetching} onClick={() => void fork.refetch()}>Refresh fork operation</button>{[JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button onClick={reset}>Finish fork operation</button> : null}</section>}
    <Problem error={mutation.error} />{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry the same fork request</button> : null}{invalid ? <p role="alert">The fork acknowledgment is incomplete. Retain the original request and inspect its operation before another fork.</p> : null}
  </Modal> : null}{source && !visible ? <button className="notice" onClick={() => setVisible(true)}>Return to retained fork operation</button> : null}</Context.Provider>;
}
