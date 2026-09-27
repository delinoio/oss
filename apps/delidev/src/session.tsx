import { SessionFiles } from "./session-files";
import { NativeBuiltin, NativeWorkspaceEvent } from "./native-builtin";
import { NativeChanges, NativeRevision } from "./native-changes";
import { NativeTodo, NativeTodoProgress } from "./native-todo";
import { NativeUsage } from "./native-usage";
import { NativeRead } from "./native-read";
import { NativeShell } from "./native-shell";
import { NativeClaudeMessage } from "./native-claude-message";
import { NativeClaudeInterruption } from "./native-claude-interruption";
import { NativeClaudeProgress } from "./native-claude-progress";
import { NativeClaudeTool } from "./native-claude-tool";
import { NativeReasoning } from "./native-reasoning";
import { SessionBudget } from "./session-budget";
import { ExecutionConfiguration } from "./execution-configuration";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  ConnectionState, EntityKind, ResourceQuery, ResourceService, SessionAction, SessionQuery,
  SyncKind, newRequestId, synchronizeResources, clientFailure, type ClientFailure, type Resource,
} from "@delinoio/delidev-api-client";
import { document as readDocument, encode, items, Mode, object, resourceName, text, Workspace, workspaceNames } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Failure, Problem } from "./ui";
import { Interaction } from "./interactions";
import { SessionTools } from "./session-tools";
import { QueuedInput } from "./queue";

function useSessionStream(id: string) {
  const transport = useTransport();
  const [resources, setResources] = useState<ReadonlyMap<string, Resource>>(new Map());
  const [removed, setRemoved] = useState<ReadonlySet<string>>(new Set());
  const [state, setState] = useState(ConnectionState.Connecting);
  const [error, setError] = useState<ClientFailure>();
  const [generation, setGeneration] = useState(0);
  const [restart, setRestart] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    const client = createClient(ResourceService, transport);
    const tombstones = new Set<string>();
    const run = async () => {
      for await (const change of synchronizeResources(client, { kind: EntityKind.SESSION, sessionId: id }, {
        signal: controller.signal, watchKinds: [EntityKind.MESSAGE, EntityKind.QUEUE, EntityKind.INTERACTION],
        maxResources: 2000, maxDocumentBytes: 8 << 20,
      })) {
        if (controller.signal.aborted) return;
        if (change.kind === SyncKind.Snapshot) {
          setResources(new Map(change.resources.map((r) => [r.id, r])));
          setRemoved(new Set());
          tombstones.clear();
          setGeneration((value) => value + 1);
        } else if (change.kind === SyncKind.Upsert) {
          setResources((current) => new Map(current).set(change.resource.id, change.resource));
        } else if (change.kind === SyncKind.Remove) {
          if (!tombstones.has(change.id) && tombstones.size >= 2000) throw new ConnectError("Reopen this session to refresh its retained history.", Code.ResourceExhausted);
          tombstones.add(change.id);
          setResources((current) => { const next = new Map(current); next.delete(change.id); return next; });
          setRemoved(new Set(tombstones));
        } else {
          setState(change.state);
          // Safe connection failures are presentation values, never raw errors.
          setError(change.failure);
        }
      }
    };
    void run().catch((reason) => { if (!controller.signal.aborted) { setError(clientFailure(reason)); setState(ConnectionState.Failed); } });
    return () => controller.abort();
  }, [id, transport, restart]);
  return { resources, removed, state, error, generation, retry: () => setRestart((value) => value + 1) };
}

function currentRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, kind: EntityKind): Resource[] {
  return base.filter((row) => !removed.has(row.id)).map((row) => {
    const update = live.get(row.id);
    return update?.kind === kind && update.revision > row.revision ? update : row;
  });
}

const TranscriptItem = memo(function TranscriptItem({ resource }: { resource: Resource }) {
  const data = readDocument(resource);
  if (data.claude_progress != null) return <NativeClaudeProgress data={data} />;
  if (data.claude_interruption != null) return <NativeClaudeInterruption data={data} />;
  if (data.claude_tool != null) {
    const valid = data.role === "tool" && data.text === "" && data.input_id == null &&
      data.phase == null && data.tool == null && data.artifact == null && data.progress == null && data.claude == null;
    return <article className="message" aria-label="Tool message">
      <header><strong>tool</strong><small>{text(data.state)}</small></header>
      <NativeClaudeTool content={valid ? data.claude_tool : undefined} state={text(data.state)} id={resource.id} native={text(data.native_id)} parent={text(data.native_parent_id)} />
    </article>;
  }
  if (data.claude != null) {
    const valid = data.role === "assistant" && data.text === "" &&
      data.native_parent_id == null && data.input_id == null &&
      data.phase == null && data.tool == null &&
      data.artifact == null && data.progress == null;
    return <article className="message" aria-label="Assistant message">
      <header><strong>assistant</strong><small>{text(data.state)}</small></header>
      <NativeClaudeMessage content={valid ? data.claude : undefined} state={text(data.state)} />
    </article>;
  }
  const tool = object(data.tool);
  const toolStarted = object(tool.started);
  const toolCompleted = object(tool.completed);
  const command = object((tool.completed ? toolCompleted : toolStarted).command);
  const artifact = object(data.artifact);
  const completed = object(artifact.completed);
  const started = object(artifact.started);
  const progress = object(data.progress);
  const plan = object(progress.plan);
  return <article className="message" aria-label={`${text(data.role) || "Agent"} message`}>
    <header><strong>{text(data.role) || "Agent"}</strong><small>{text(data.state)}</small></header>
    {text(data.text) ? <pre>{text(data.text)}</pre> : null}
    {toolStarted.kind === "opencode-builtin" ? <NativeBuiltin tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-todo" ? <NativeTodo tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-read" ? <NativeRead tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-shell" ? <NativeShell tool={tool} state={text(data.state)} /> : Object.keys(tool).length ? <details><summary>Tool · {text(toolStarted.kind) || "Native operation"} · {text(toolCompleted.status) || text(toolStarted.status)}</summary>
      {text(command.command) ? <pre>{text(command.command)}</pre> : null}
      {text(command.cwd) ? <p>Directory: {text(command.cwd)}</p> : null}
      {text(tool.output) ? <pre>{text(tool.output)}</pre> : null}
      {typeof command.aggregated_output === "string" ? <details><summary>Native aggregate output</summary><pre>{command.aggregated_output}</pre></details> : null}
      {items((tool.completed ? toolCompleted : toolStarted).changes).map((item, index) => <pre key={index}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}
      {items(tool.patches).map((patch, index) => <details key={index}><summary>Patch observation {index + 1}</summary>{items(object(patch).changes).map((item, part) => <pre key={part}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}</details>)}
      {items(tool.inputs).map((input, index) => <pre key={index}>Tool input: {text(object(object(input).input).text)}</pre>)}
    </details> : null}
    {started.kind === "opencode-revision" ? <NativeRevision artifact={artifact} state={text(data.state)} /> : started.kind === "reasoning-text" ? <NativeReasoning artifact={artifact} state={text(data.state)} /> : Object.keys(artifact).length ? <details open><summary>{text(started.kind) || "Native artifact"}</summary>
      {text(started.text) ? <pre>{text(started.text)}</pre> : null}
      {[...items(started.summary), ...items(started.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}
      {items(artifact.deltas).length ? <details><summary>Streamed observations</summary>{items(artifact.deltas).map((item, index) => { const delta = object(object(item).delta); return <pre key={index}>{text(delta.kind)}{typeof delta.index === "number" ? ` ${delta.index}` : ""}: {text(delta.text)}</pre>; })}</details> : null}
      {artifact.completed ? <section aria-label="Completed artifact"><h3>Completed artifact</h3><pre>{text(completed.text)}</pre>{[...items(completed.summary), ...items(completed.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}</section> : null}
    </details> : null}
    {progress.kind === "opencode-workspace" ? <NativeWorkspaceEvent progress={progress} state={text(data.state)} /> : progress.kind === "opencode-changes" ? <NativeChanges progress={progress} state={text(data.state)} turn={text(data.native_turn_id)} /> : progress.kind === "opencode-todo" ? <NativeTodoProgress progress={progress} state={text(data.state)} /> : Object.keys(progress).length ? <details open><summary>Progress · {text(progress.kind)}</summary><pre>{text(progress.diff) || text(plan.explanation)}</pre><ol>{items(plan.steps).map((step, index) => <li key={index}>{text(object(step).step)} · {text(object(step).status)}</li>)}</ol></details> : null}
  </article>;
});

export function SessionView({ id, draft, setDraft }: { id: string; draft: string; setDraft: (value: string) => void }) {
  const live = useSessionStream(id);
  const [filesOpen, setFilesOpen] = useState(false);
  const filesButton = useRef<HTMLButtonElement>(null);
 const [budgetBlocked,setBudgetBlocked]=useState(false);
  const [page, setPage] = useState("");
  const [queuePage, setQueuePage] = useState("");
  const [interactionPage, setInteractionPage] = useState("");
  const [previous, setPrevious] = useState<string[]>([]);
  const [mode, setMode] = useState(Mode.Execute);
  const messages = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.MESSAGE, sessionId: id, pageSize: 50, pageToken: page } }, { enabled: live.generation > 0 });
  const queue = useQuery(SessionQuery.listQueue, { sessionId: id, pageSize: 50, pageToken: queuePage }, { enabled: live.generation > 0 });
  const interactions = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.INTERACTION, sessionId: id, pageSize: 20, pageToken: interactionPage } }, { enabled: live.generation > 0 });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const observed = live.resources.get(id);
  const session = observed && acknowledged && acknowledged.revision > observed.revision ? acknowledged : observed;
  const data = readDocument(session);
  const rows = useMemo(() => {
    const result = currentRows(messages.data?.resources ?? [], live.resources, live.removed, EntityKind.MESSAGE);
    // Only the last page accepts newly appended messages. Earlier pages keep
    // their bounds and update existing message identities without rescanning.
    if (messages.data && !messages.data.nextPageToken) {
      const ids = new Set(result.map((r) => r.id));
      const after = messages.data.resources.at(-1)?.id ?? "";
      for (const row of live.resources.values()) {
        if (row.kind === EntityKind.MESSAGE && row.id > after && !ids.has(row.id) && !live.removed.has(row.id)) result.push(row);
      }
    }
    return result.sort((a, b) => a.id.localeCompare(b.id));
  }, [messages.data, live.resources, live.removed]);
  useEffect(() => { if (live.generation > 1) { void messages.refetch(); void queue.refetch(); void interactions.refetch(); } }, [live.generation]);
  const send = useRetainedMutation(`enqueue:${id}`, SessionQuery.enqueueInput, () => { setDraft(""); void queue.refetch(); });
  const control = useRetainedMutation(`control:${id}`, SessionQuery.controlSession, (value) => { if (value.change?.session) setAcknowledged(value.change.session); });
  const locked = send.busy || send.uncertain;
  const next = messages.data?.nextPageToken;
  const pending = currentRows(queue.data?.inputs ?? [], live.resources, live.removed, EntityKind.QUEUE);
  if (queue.data && !queue.data.nextPageToken) for (const row of live.resources.values()) if (row.kind === EntityKind.QUEUE && !live.removed.has(row.id) && Number(readDocument(row).sequence) > Number(readDocument(queue.data.inputs.at(-1)).sequence ?? 0) && !pending.some((r) => r.id === row.id)) pending.push(row);
  const requests = currentRows(interactions.data?.resources ?? [], live.resources, live.removed, EntityKind.INTERACTION);
  if (interactions.data && !interactions.data.nextPageToken) for (const row of live.resources.values()) if (row.kind === EntityKind.INTERACTION && row.id > (interactions.data.resources.at(-1)?.id ?? "") && !live.removed.has(row.id) && !requests.some((r) => r.id === row.id)) requests.push(row);
  const queued = pending.filter((r) => text(readDocument(r).delivery) !== "removed");
  const action = (value: SessionAction) => {
    if (!session) return;
    void control.send({ mutation: { id, expectedRevision: session.revision, requestId: newRequestId() }, action: value });
  };
  return <div className={`session-workspace${filesOpen ? " files-open" : ""}`}><section className="session" aria-label="Current session">
    <header className="session-header"><div><h2>{resourceName(session)}</h2><p>{workspaceNames[text(data.workspace) as Workspace] || "Workspace"} · {text(data.outcome)} · {text(data.dispatch)} · {text(data.archive)}</p></div>
      <div className="actions"><button ref={filesButton} aria-expanded={filesOpen} aria-controls={`files-${id}`} onClick={() => setFilesOpen(!filesOpen)}>Files</button><button disabled={!session || control.busy || control.uncertain} onClick={() => action(SessionAction.STOP)}>Stop</button>
        <button disabled={!session || control.busy || control.uncertain} onClick={() => action(text(data.archive) === "archived" ? SessionAction.RESTORE : SessionAction.ARCHIVE)}>{text(data.archive) === "archived" ? "Restore" : "Archive"}</button>
        <button disabled={!session || control.busy || control.uncertain || budgetBlocked || text(data.archive) !== "active"} onClick={() => action(SessionAction.RESUME)}>Resume</button></div></header>
    <p className="connection" role="status">{live.state === ConnectionState.Live ? "Connected" : live.state === ConnectionState.Reconnecting ? "Connection lost · Retained state shown" : live.state === ConnectionState.Failed ? "Connection requires attention" : "Connecting…"}</p>
    <Failure failure={live.error} />{live.state === ConnectionState.Failed ? <button onClick={live.retry}>Refresh connection</button> : null}
    {text(data.recovery) !== "none" && text(data.recovery) ? <p className="notice">Recovery: {text(data.recovery)}. Execution remains under server control.</p> : null}
    {object(data.problem).message ? <p className="notice">{text(object(data.problem).message)} {text(object(data.problem).guidance)}</p> : null}
    <Problem error={control.error} />{control.uncertain ? <button onClick={control.retry} disabled={control.busy}>Retry the same control request</button> : null}
    {session ? <><SessionTools resource={session} changed={setAcknowledged} /><ExecutionConfiguration resource={session} /><NativeUsage session={session} /><SessionBudget resource={session} changed={setAcknowledged} blocked={setBudgetBlocked} /></> : null}
    <details className="requests" open={requests.some((r) => readDocument(r).closure === "open")}><summary>Agent requests · {requests.length} on this page</summary><Problem error={interactions.error} />
      {requests.map((row) => <Interaction key={row.id} resource={row} refresh={() => void interactions.refetch()} />)}
      <nav aria-label="Request pages"><button disabled={!interactionPage || interactions.isFetching} onClick={() => setInteractionPage("")}>First page</button><button disabled={!interactions.data?.nextPageToken || interactions.isFetching} onClick={() => setInteractionPage(interactions.data!.nextPageToken)}>Next page</button></nav>
    </details>
    <div className="transcript" aria-label="Conversation"><Problem error={messages.error} />{messages.isPending ? <p>Loading conversation…</p> : rows.length ? rows.map((row) => <TranscriptItem key={row.id} resource={row} />) : <p className="empty">The conversation will appear here after the harness accepts input.</p>}
      <nav aria-label="Conversation pages"><button disabled={!page || messages.isFetching} onClick={() => { setPage(""); setPrevious([]); }}>First page</button><button disabled={previous.length === 0 || messages.isFetching} onClick={() => { setPage(previous.at(-1)!); setPrevious(previous.slice(0, -1)); }}>Previous</button><button disabled={!next || messages.isFetching} onClick={() => { setPrevious([...previous.slice(-99), page]); setPage(next!); }}>Next</button></nav>
    </div>
    <details className="queue"><summary>Input queue · {queued.filter((r) => text(readDocument(r).delivery) === "queued").length} waiting</summary><Problem error={queue.error} />
      {queued.map((r) => <QueuedInput key={r.id} resource={r} session={session} refresh={() => void queue.refetch()} />)}
      <nav aria-label="Queue pages"><button disabled={!queuePage || queue.isFetching} onClick={() => setQueuePage("")}>First page</button><button disabled={!queue.data?.nextPageToken || queue.isFetching} onClick={() => setQueuePage(queue.data!.nextPageToken)}>Next page</button></nav>
    </details>
    <form className="composer" onSubmit={(event) => { event.preventDefault(); void send.send({ requestId: newRequestId(), sessionId: id, documentJson: encode({ prompt: draft, mode }) }); }}>
      <label htmlFor={`prompt-${id}`}>Message</label><textarea id={`prompt-${id}`} value={draft} onChange={(event) => setDraft(event.target.value)} disabled={locked} placeholder="Send a follow-up to this session" rows={3} />
      <div className="actions"><label>Mode <select value={mode} disabled={locked} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>Execute</option><option value={Mode.Plan}>Plan</option></select></label><button className="primary" disabled={locked || !draft.trim() || text(data.archive) !== "active"}>Queue message</button></div>
      <Problem error={send.error} />{send.uncertain ? <button type="button" disabled={send.busy} onClick={send.retry}>Retry the same message</button> : null}
    </form>
  </section>{filesOpen ? <div id={`files-${id}`} className="session-app-panel"><SessionFiles key={id} sessionId={id} close={() => { setFilesOpen(false); filesButton.current?.focus(); }} /></div> : null}</div>;
}
