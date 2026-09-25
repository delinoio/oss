import { useEffect, useMemo, useState } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  ConnectionState, EntityKind, ResourceQuery, ResourceService, SessionAction, SessionQuery,
  SyncKind, newRequestId, synchronizeResources, clientFailure, type ClientFailure, type Resource,
} from "@delinoio/delidev-api-client";
import { document as readDocument, encode, items, Mode, object, resourceName, text, Workspace, workspaceNames } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Failure, Problem } from "./ui";

function useSessionStream(id: string) {
  const transport = useTransport();
  const [resources, setResources] = useState<ReadonlyMap<string, Resource>>(new Map());
  const [removed, setRemoved] = useState<ReadonlySet<string>>(new Set());
  const [state, setState] = useState(ConnectionState.Connecting);
  const [error, setError] = useState<ClientFailure>();
  const [generation, setGeneration] = useState(0);
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
  }, [id, transport]);
  return { resources, removed, state, error, generation };
}

function currentRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, kind: EntityKind): Resource[] {
  return base.filter((row) => !removed.has(row.id)).map((row) => {
    const update = live.get(row.id);
    return update?.kind === kind && update.revision > row.revision ? update : row;
  });
}

function TranscriptItem({ resource }: { resource: Resource }) {
  const data = readDocument(resource);
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
    {Object.keys(tool).length ? <details><summary>Tool · {text(toolStarted.kind) || "Native operation"} · {text(toolCompleted.status) || text(toolStarted.status)}</summary>
      {text(command.command) ? <pre>{text(command.command)}</pre> : null}
      {text(command.cwd) ? <p>Directory: {text(command.cwd)}</p> : null}
      {text(tool.output) ? <pre>{text(tool.output)}</pre> : null}
      {typeof command.aggregated_output === "string" ? <details><summary>Native aggregate output</summary><pre>{command.aggregated_output}</pre></details> : null}
      {items((tool.completed ? toolCompleted : toolStarted).changes).map((item, index) => <pre key={index}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}
      {items(tool.patches).map((patch, index) => <details key={index}><summary>Patch observation {index + 1}</summary>{items(object(patch).changes).map((item, part) => <pre key={part}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}</details>)}
      {items(tool.inputs).map((input, index) => <pre key={index}>Tool input: {text(object(object(input).input).text)}</pre>)}
    </details> : null}
    {Object.keys(artifact).length ? <details open><summary>{text(started.kind) || "Native artifact"}</summary>
      {text(started.text) ? <pre>{text(started.text)}</pre> : null}
      {[...items(started.summary), ...items(started.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}
      {items(artifact.deltas).length ? <details><summary>Streamed observations</summary>{items(artifact.deltas).map((item, index) => { const delta = object(object(item).delta); return <pre key={index}>{text(delta.kind)}{typeof delta.index === "number" ? ` ${delta.index}` : ""}: {text(delta.text)}</pre>; })}</details> : null}
      {artifact.completed ? <section aria-label="Completed artifact"><h3>Completed artifact</h3><pre>{text(completed.text)}</pre>{[...items(completed.summary), ...items(completed.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}</section> : null}
    </details> : null}
    {Object.keys(progress).length ? <details open><summary>Progress · {text(progress.kind)}</summary><pre>{text(progress.diff) || text(plan.explanation)}</pre><ol>{items(plan.steps).map((step, index) => <li key={index}>{text(object(step).step)} · {text(object(step).status)}</li>)}</ol></details> : null}
  </article>;
}

export function SessionView({ id, draft, setDraft }: { id: string; draft: string; setDraft: (value: string) => void }) {
  const live = useSessionStream(id);
  const [page, setPage] = useState("");
  const [previous, setPrevious] = useState<string[]>([]);
  const [mode, setMode] = useState(Mode.Execute);
  const messages = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.MESSAGE, sessionId: id, pageSize: 50, pageToken: page } }, { enabled: live.generation > 0 });
  const queue = useQuery(SessionQuery.listQueue, { sessionId: id, pageSize: 100 });
  const session = live.resources.get(id);
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
  useEffect(() => { if (live.generation > 1) { void messages.refetch(); void queue.refetch(); } }, [live.generation]);
  const send = useRetainedMutation(`enqueue:${id}`, SessionQuery.enqueueInput, () => { setDraft(""); void queue.refetch(); });
  const control = useRetainedMutation(`control:${id}`, SessionQuery.controlSession);
  const locked = send.busy || send.uncertain;
  const next = messages.data?.nextPageToken;
  const pending = currentRows(queue.data?.inputs ?? [], live.resources, live.removed, EntityKind.QUEUE);
  for (const row of live.resources.values()) if (row.kind === EntityKind.QUEUE && !pending.some((r) => r.id === row.id)) pending.push(row);
  const queued = pending.filter((r) => text(readDocument(r).delivery) !== "removed");
  const action = (value: SessionAction) => {
    if (!session) return;
    void control.send({ mutation: { id, expectedRevision: session.revision, requestId: newRequestId() }, action: value });
  };
  return <section className="session" aria-label="Current session">
    <header className="session-header"><div><h2>{resourceName(session)}</h2><p>{workspaceNames[text(data.workspace) as Workspace] || "Workspace"} · {text(data.outcome)} · {text(data.dispatch)} · {text(data.archive)}</p></div>
      <div className="actions"><button disabled={!session || control.busy || control.uncertain} onClick={() => action(SessionAction.STOP)}>Stop</button>
        <button disabled={!session || control.busy || control.uncertain} onClick={() => action(text(data.archive) === "archived" ? SessionAction.RESTORE : SessionAction.ARCHIVE)}>{text(data.archive) === "archived" ? "Restore" : "Archive"}</button>
        <button disabled={!session || control.busy || control.uncertain || text(data.archive) !== "active"} onClick={() => action(SessionAction.RESUME)}>Resume</button></div></header>
    <p className="connection" role="status">{live.state === ConnectionState.Live ? "Connected" : live.state === ConnectionState.Reconnecting ? "Connection lost · Retained state shown" : live.state === ConnectionState.Failed ? "Connection requires attention" : "Connecting…"}</p>
    <Failure failure={live.error} />
    {text(data.recovery) !== "none" && text(data.recovery) ? <p className="notice">Recovery: {text(data.recovery)}. Execution remains under server control.</p> : null}
    {object(data.problem).message ? <p className="notice">{text(object(data.problem).message)} {text(object(data.problem).guidance)}</p> : null}
    <Problem error={control.error} />{control.uncertain ? <button onClick={control.retry} disabled={control.busy}>Retry the same control request</button> : null}
    <div className="transcript" aria-label="Conversation"><Problem error={messages.error} />{messages.isPending ? <p>Loading conversation…</p> : rows.length ? rows.map((row) => <TranscriptItem key={row.id} resource={row} />) : <p className="empty">The conversation will appear here after the harness accepts input.</p>}
      <nav aria-label="Conversation pages"><button disabled={previous.length === 0 || messages.isFetching} onClick={() => { setPage(previous.at(-1)!); setPrevious(previous.slice(0, -1)); }}>Previous</button><button disabled={!next || messages.isFetching} onClick={() => { setPrevious([...previous, page]); setPage(next!); }}>Next</button></nav>
    </div>
    <details className="queue"><summary>Input queue · {queued.filter((r) => text(readDocument(r).delivery) === "queued").length} waiting</summary><Problem error={queue.error} />
      {queued.map((r) => { const value = readDocument(r); return <article key={r.id}><strong>{text(value.mode)} · {text(value.delivery)}</strong><p>{text(value.prompt)}</p></article>; })}
      {queue.data?.nextPageToken ? <p>More retained input exists. Open queue history through the CLI.</p> : null}
    </details>
    <form className="composer" onSubmit={(event) => { event.preventDefault(); void send.send({ requestId: newRequestId(), sessionId: id, documentJson: encode({ prompt: draft, mode }) }); }}>
      <label htmlFor={`prompt-${id}`}>Message</label><textarea id={`prompt-${id}`} value={draft} onChange={(event) => setDraft(event.target.value)} disabled={locked} placeholder="Send a follow-up to this session" rows={3} />
      <div className="actions"><label>Mode <select value={mode} disabled={locked} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>Execute</option><option value={Mode.Plan}>Plan</option></select></label><button className="primary" disabled={locked || !draft.trim() || text(data.archive) !== "active"}>Queue message</button></div>
      <Problem error={send.error} />{send.uncertain ? <button type="button" disabled={send.busy} onClick={send.retry}>Retry the same message</button> : null}
    </form>
  </section>;
}
