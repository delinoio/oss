import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { Subagents } from "./subagents";
import { NativeGrokTool } from "./native-grok-interactions";
import { NativeGrokText, NativeGrokUser } from "./native-grok";
import { SessionBrowser } from "./session-browser";
import { SessionFiles } from "./session-files";
import { RequestDiagnostics } from "./request-diagnostics";
import { SessionDiff } from "./session-diff";
import { NativeBuiltin, NativeWorkspaceEvent } from "./native-builtin";
import { NativeChanges, NativeRevision } from "./native-changes";
import { NativeTodo, NativeTodoProgress } from "./native-todo";
import { NativeUsage } from "./native-usage";
import { NativeRead } from "./native-read";
import { NativeShell } from "./native-shell";
import { NativeClaudeMessage } from "./native-claude-message";
import { NativeClaudeInterruption } from "./native-claude-interruption";
import { NativeContextCompaction } from "./native-context-compaction";
import { NativeClaudeProgress } from "./native-claude-progress";
import { NativeClaudeTool } from "./native-claude-tool";
import { NativeReasoning } from "./native-reasoning";
import { SessionContext } from "./session-context";
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
import { ServiceProblem, Failure, Problem  } from "./ui";
import { Interaction } from "./interactions";
import { SessionTerminals } from "./session-terminals";
import { SessionForkAction } from "./session-fork";
import { SidechatFindings } from "./sidechat";
import { SessionTools } from "./session-tools";
import { SessionStorageAction } from "./session-storage";
import { SessionPullRequests } from "./session-pull-requests";
import { QueuedInput } from "./queue";
import { StartupRejection } from "./startup-rejection";
import { sessionTitlePresentation } from "./session-title";

function useSessionStream(id: string) {
  const transport = useTransport();
  const [resources, setResources] = useState<ReadonlyMap<string, Resource>>(new Map());
  const [removed, setRemoved] = useState<ReadonlySet<string>>(new Set());
  const [newMessageIds, setNewMessageIds] = useState<readonly string[]>([]);
  const [newQueueIds, setNewQueueIds] = useState<readonly string[]>([]);
  const [newInteractionIds, setNewInteractionIds] = useState<readonly string[]>([]);
  const [state, setState] = useState(ConnectionState.Connecting);
  const [error, setError] = useState<ClientFailure>();
  const [generation, setGeneration] = useState(0);
  const [restart, setRestart] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    const client = createClient(ResourceService, transport);
    const tombstones = new Set<string>();
    const known = new Set<string>();
    const run = async () => {
      for await (const change of synchronizeResources(client, { kind: EntityKind.SESSION, sessionId: id }, {
        signal: controller.signal, watchKinds: [EntityKind.MESSAGE, EntityKind.QUEUE, EntityKind.INTERACTION],
        maxResources: 2000, maxDocumentBytes: 8 << 20,
      })) {
        if (controller.signal.aborted) return;
        if (change.kind === SyncKind.Snapshot) {
          known.clear();
          for (const row of change.resources) known.add(row.id);
          setResources(new Map(change.resources.map((r) => [r.id, r])));
          setRemoved(new Set());
          setNewMessageIds((current) => current.filter((id) => known.has(id)));
          setNewQueueIds((current) => current.filter((id) => known.has(id)));
          setNewInteractionIds((current) => current.filter((id) => known.has(id)));
          tombstones.clear();
          setGeneration((value) => value + 1);
        } else if (change.kind === SyncKind.Upsert) {
          if (!known.has(change.resource.id)) {
            if (change.resource.kind === EntityKind.MESSAGE) setNewMessageIds((current) => current.length >= 2000 ? current : [...current, change.resource.id]);
            if (change.resource.kind === EntityKind.QUEUE) setNewQueueIds((current) => current.length >= 2000 ? current : [...current, change.resource.id]);
            if (change.resource.kind === EntityKind.INTERACTION) setNewInteractionIds((current) => current.length >= 2000 ? current : [...current, change.resource.id]);
          }
          known.add(change.resource.id);
          setResources((current) => new Map(current).set(change.resource.id, change.resource));
        } else if (change.kind === SyncKind.Remove) {
          known.delete(change.id);
          setNewMessageIds((current) => current.filter((id) => id !== change.id));
          setNewQueueIds((current) => current.filter((id) => id !== change.id));
          setNewInteractionIds((current) => current.filter((id) => id !== change.id));
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
  return { resources, removed, newMessageIds, newQueueIds, newInteractionIds, state, error, generation, retry: () => setRestart((value) => value + 1) };
}

function currentRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, kind: EntityKind): Resource[] {
  return base.filter((row) => !removed.has(row.id)).map((row) => {
    const update = live.get(row.id);
    return update?.kind === kind && update.revision > row.revision ? update : row;
  });
}

function appendedRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean, kind: EntityKind): Resource[] {
  const result = currentRows(base, live, removed, kind);
  if (!lastPage) return result;
  const ids = new Set(result.map((row) => row.id));
  for (const id of arrivals) {
    const row = live.get(id);
    if (row?.kind === kind && row.sessionId === sessionId && !ids.has(id) && !removed.has(id)) {
      result.push(row);
      ids.add(id);
    }
  }
  return result;
}

export function messageRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean): Resource[] {
  return appendedRows(base, live, removed, arrivals, sessionId, lastPage, EntityKind.MESSAGE);
}

export function queueRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean): Resource[] {
  return appendedRows(base, live, removed, arrivals, sessionId, lastPage, EntityKind.QUEUE);
}

export function interactionRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean): Resource[] {
  return appendedRows(base, live, removed, arrivals, sessionId, lastPage, EntityKind.INTERACTION);
}

const TranscriptItem = memo(function TranscriptItem({ resource }: { resource: Resource }) {
  useLocale();
  const data = readDocument(resource);
  if (Object.hasOwn(data,"grok_tool")) return <NativeGrokTool data={data}/>;
 if (Object.hasOwn(data, "grok_user")) return <NativeGrokUser data={data} />;
  if (data.grok_text != null) return <NativeGrokText data={data} />;
  if (data.claude_progress != null) return <NativeClaudeProgress data={data} />;
  if (data.claude_interruption != null) return <NativeClaudeInterruption data={data} />;
  if (data.claude_tool != null) {
    const valid = data.role === "tool" && data.text === "" && data.input_id == null &&
      data.phase == null && data.tool == null && data.artifact == null && data.progress == null && data.claude == null;
    return <article className="message" aria-label={copy("session.toolMessage_adbec1")}>
      <header><strong>{copy("session.tool_7c9bbe")}</strong><small>{statusLabel(text(data.state))}</small></header>
      <NativeClaudeTool content={valid ? data.claude_tool : undefined} state={text(data.state)} id={resource.id} native={text(data.native_id)} parent={text(data.native_parent_id)} />
    </article>;
  }
  if (data.claude != null) {
    const valid = data.role === "assistant" && data.text === "" &&
      data.native_parent_id == null && data.input_id == null &&
      data.phase == null && data.tool == null &&
      data.artifact == null && data.progress == null;
    return <article className="message" aria-label={copy("session.assistantMessage_8352f5")}>
      <header><strong>{copy("session.assistant_a39a7f")}</strong><small>{statusLabel(text(data.state))}</small></header>
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
  return <article className="message" aria-label={copy("session.message_e9ca2b", { v0: text(data.role) || "Agent" })}>
    <header><strong>{text(data.role) || copy("session.extra.11b39c93777e")}</strong><small>{statusLabel(text(data.state))}</small></header>
    {text(data.text) ? <pre>{text(data.text)}</pre> : null}
    {toolStarted.kind === "opencode-builtin" ? <NativeBuiltin tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-todo" ? <NativeTodo tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-read" ? <NativeRead tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-shell" ? <NativeShell tool={tool} state={text(data.state)} /> : Object.keys(tool).length ? <details><summary><LocalizedText id="session.tool_844a02" components={{ s0: <>{text(toolStarted.kind) || copy("session.extra.fa176576233d")}</>, s1: <>{text(toolCompleted.status) || text(toolStarted.status)}</> }} /></summary>
      {text(command.command) ? <pre>{text(command.command)}</pre> : null}
      {text(command.cwd) ? <p><LocalizedText id="session.directory_369f13" components={{ s0: <>{text(command.cwd)}</> }} /></p> : null}
      {text(tool.output) ? <pre>{text(tool.output)}</pre> : null}
      {typeof command.aggregated_output === "string" ? <details><summary>{copy("session.nativeAggregateOutput_2d06e4")}</summary><pre>{command.aggregated_output}</pre></details> : null}
      {items((tool.completed ? toolCompleted : toolStarted).changes).map((item, index) => <pre key={index}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}
      {items(tool.patches).map((patch, index) => <details key={index}><summary><LocalizedText id="session.patchObservation_8b886e" components={{ s0: <>{index + 1}</> }} /></summary>{items(object(patch).changes).map((item, part) => <pre key={part}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}</details>)}
      {items(tool.inputs).map((input, index) => <pre key={index}>Tool input: {text(object(object(input).input).text)}</pre>)}
    </details> : null}
    {started.kind === "opencode-revision" ? <NativeRevision artifact={artifact} state={text(data.state)} /> : started.kind === "reasoning-text" ? <NativeReasoning artifact={artifact} state={text(data.state)} /> : Object.keys(artifact).length ? <details open><summary>{text(started.kind) || copy("session.extra.a3e40dda2eb1")}</summary>
      {text(started.text) ? <pre>{text(started.text)}</pre> : null}
      {[...items(started.summary), ...items(started.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}
      {items(artifact.deltas).length ? <details><summary>{copy("session.streamedObservations_589dae")}</summary>{items(artifact.deltas).map((item, index) => { const delta = object(object(item).delta); return <pre key={index}>{text(delta.kind)}{typeof delta.index === "number" ? ` ${delta.index}` : ""}: {text(delta.text)}</pre>; })}</details> : null}
      {artifact.completed ? <section aria-label={copy("session.completedArtifact_de26fd")}><h3>{copy("session.completedArtifact_de26fd")}</h3><pre>{text(completed.text)}</pre>{[...items(completed.summary), ...items(completed.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}</section> : null}
    </details> : null}
    {progress.kind === "native-compaction" ? <NativeContextCompaction progress={progress} state={text(data.state)} /> : progress.kind === "opencode-workspace" ? <NativeWorkspaceEvent progress={progress} state={text(data.state)} /> : progress.kind === "opencode-changes" ? <NativeChanges progress={progress} state={text(data.state)} turn={text(data.native_turn_id)} /> : progress.kind === "opencode-todo" ? <NativeTodoProgress progress={progress} state={text(data.state)} /> : Object.keys(progress).length ? <details open><summary><LocalizedText id="session.progress_a4d877" components={{ s0: <>{text(progress.kind)}</> }} /></summary><pre>{text(progress.diff) || text(plan.explanation)}</pre><ol>{items(plan.steps).map((step, index) => <li key={index}>{text(object(step).step)} · {text(object(step).status)}</li>)}</ol></details> : null}
  </article>;
});

enum SessionPanel { Closed = "closed", Files = "files", Diff = "diff", Terminals = "terminals", Browser = "browser", Diagnostics = "diagnostics" }

export function SessionView({ id, draft, setDraft }: { id: string; draft: string; setDraft: (value: string) => void }) {
  useLocale();
  const live = useSessionStream(id);
  const [panel, setPanel] = useState(SessionPanel.Closed);
  const terminalsButton = useRef<HTMLButtonElement>(null);
  const filesButton = useRef<HTMLButtonElement>(null);
  const diffButton = useRef<HTMLButtonElement>(null);
  const diagnosticsButton = useRef<HTMLButtonElement>(null);
  const browserButton = useRef<HTMLButtonElement>(null);
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
  // A paused account switch selects the next account without rewriting the
  // preceding execution. Match the server's continuation selection immediately.
  const accountChanges = items(data.account_changes);
  const browserAccountId = accountChanges.length
    ? text(object(accountChanges.at(-1)).account_id)
    : text(object(data.current_execution).account_id) || text(object(data.initial_execution).initial_account_id);
  const titlePresentation = sessionTitlePresentation(data);
  const rows = useMemo(() => {
    // The stream records creation order. UUIDs from different Workers are not
    // an append sequence, even when each Worker generates UUID-v7 values.
    return messageRows(messages.data?.resources ?? [], live.resources, live.removed, live.newMessageIds, id, !!messages.data && !messages.data.nextPageToken);
  }, [messages.data, live.resources, live.removed, live.newMessageIds, id]);
  useEffect(() => { if (live.generation > 1) { void messages.refetch(); void queue.refetch(); void interactions.refetch(); } }, [live.generation]);
  const send = useRetainedMutation(`enqueue:${id}`, SessionQuery.enqueueInput, () => { setDraft(""); void queue.refetch(); });
  const control = useRetainedMutation(`control:${id}`, SessionQuery.controlSession, (value) => { if (value.change?.session) setAcknowledged(value.change.session); });
  const locked = send.busy || send.uncertain;
  const next = messages.data?.nextPageToken;
  // Stream arrivals have exact identities even when their JSON sequence exceeds
  // JavaScript's safe-integer range. Append only arrivals on the final page.
  const pending = queueRows(queue.data?.inputs ?? [], live.resources, live.removed, live.newQueueIds, id, !!queue.data && !queue.data.nextPageToken);
  const requests = interactionRows(interactions.data?.resources ?? [], live.resources, live.removed, live.newInteractionIds, id, !!interactions.data && !interactions.data.nextPageToken);
  const queued = pending.filter((r) => text(readDocument(r).delivery) !== "removed");
  const action = (value: SessionAction) => {
    if (!session) return;
    void control.send({ mutation: { id, expectedRevision: session.revision, requestId: newRequestId() }, action: value });
  };
  return <div className={`session-workspace${panel !== SessionPanel.Closed ? " files-open" : ""}`}><section className="session" aria-label={copy("session.currentSession_a32789")}>
    <header className="session-header"><div><h2>{resourceName(session)}</h2><p>{workspaceNames[text(data.workspace) as Workspace] || copy("session.extra.87bb59ba2f92")} · {statusLabel(text(data.outcome))} · {statusLabel(text(data.dispatch))} · {statusLabel(text(data.archive))}</p>{titlePresentation ? <p className="session-title-status" role="status">{titlePresentation.label}{titlePresentation.detail ? copy("session.message_2fa20b", { v0: titlePresentation.detail }) : ""}</p> : null}</div>
      <div className="actions">{session ? <SessionForkAction source={session} /> : null}<button disabled={Boolean(object(data.fork).sidechat_parent_snapshot)} ref={terminalsButton} aria-expanded={panel === SessionPanel.Terminals} aria-controls={`terminals-${id}`} onClick={() => setPanel(panel === SessionPanel.Terminals ? SessionPanel.Closed : SessionPanel.Terminals)}>{copy("session.terminals_7482c4")}</button><button ref={filesButton} aria-expanded={panel === SessionPanel.Files} aria-controls={`files-${id}`} onClick={() => setPanel(panel === SessionPanel.Files ? SessionPanel.Closed : SessionPanel.Files)}>{copy("session.files_abc7e9")}</button><button ref={diffButton} aria-expanded={panel === SessionPanel.Diff} aria-controls={`diff-${id}`} onClick={() => setPanel(panel === SessionPanel.Diff ? SessionPanel.Closed : SessionPanel.Diff)}>{copy("session.diff_7ecf46")}</button><button ref={diagnosticsButton} aria-expanded={panel === SessionPanel.Diagnostics} aria-controls={`diagnostics-${id}`} onClick={() => setPanel(panel === SessionPanel.Diagnostics ? SessionPanel.Closed : SessionPanel.Diagnostics)}>{copy("session.diagnostics_268f14")}</button><button ref={browserButton} aria-expanded={panel === SessionPanel.Browser} aria-controls={`browser-${id}`} onClick={() => setPanel(panel === SessionPanel.Browser ? SessionPanel.Closed : SessionPanel.Browser)}>{copy("session.browser_d31de1")}</button><button disabled={!session || control.busy || control.uncertain} onClick={() => action(SessionAction.STOP)}>{copy("session.stop_cae7d5")}</button>
        <button disabled={!session || control.busy || control.uncertain} onClick={() => action(text(data.archive) === "archived" ? SessionAction.RESTORE : SessionAction.ARCHIVE)}>{text(data.archive) === "archived" ? copy("session.restore_a76e13") : copy("session.archive_66f480")}</button>
        <button disabled={!session || control.busy || control.uncertain || budgetBlocked || Object.hasOwn(data, "startup_rejection") || text(data.archive) !== "active"} onClick={() => action(SessionAction.RESUME)}>{copy("session.resume_d640c7")}</button></div></header>
    <p className="connection" role="status">{live.state === ConnectionState.Live ? copy("session.connected_229655") : live.state === ConnectionState.Reconnecting ? copy("session.connectionLostRetainedStateShown_8cc737") : live.state === ConnectionState.Failed ? copy("session.connectionRequiresAttention_160d4a") : copy("session.connecting_72021e")}</p>
    <Failure failure={live.error} />{live.state === ConnectionState.Failed ? <button onClick={live.retry}>{copy("session.refreshConnection_73791f")}</button> : null}
    {text(data.recovery) !== "none" && text(data.recovery) ? <p className="notice"><LocalizedText id="session.recoveryExecutionRemainsUnderServerControl_d80aa1" components={{ s0: <>{statusLabel(text(data.recovery))}</> }} /></p> : null}
    {object(data.problem).message ? <ServiceProblem code={text(object(data.problem).code) || text(object(data.problem).problem_code)}><p className="notice">{text(object(data.problem).message)} {text(object(data.problem).guidance)}</p></ServiceProblem> : null}
    {session ? <StartupRejection session={session} /> : null}
    <Problem error={control.error} />{control.uncertain ? <button onClick={control.retry} disabled={control.busy}>{copy("session.retryTheSameControlRequest_609aff")}</button> : null}
    {session ? <><SessionTools resource={session} changed={setAcknowledged} /><SessionStorageAction source={session} /><SessionPullRequests key={id} session={session} /><ExecutionConfiguration resource={session} /><NativeUsage session={session} /><SessionContext key={id} session={session} /><Subagents key={id} sessionId={id} revision={session.revision.toString()} /><SessionBudget resource={session} changed={setAcknowledged} blocked={setBudgetBlocked} /></> : null}
    <details className="requests" open={requests.some((r) => readDocument(r).closure === "open")}><summary><LocalizedText id="session.agentRequestsOnThisPage_5e8644" components={{ s0: <>{requests.length}</> }} /></summary><Problem error={interactions.error} />
      {requests.map((row) => <Interaction key={row.id} resource={row} refresh={() => void interactions.refetch()} />)}
      <nav aria-label={copy("session.requestPages_d06a30")}><button disabled={!interactionPage || interactions.isFetching} onClick={() => setInteractionPage("")}>{copy("session.firstPage_0bdbb7")}</button><button disabled={!interactions.data?.nextPageToken || interactions.isFetching} onClick={() => setInteractionPage(interactions.data!.nextPageToken)}>{copy("session.nextPage_c08ac7")}</button></nav>
    </details>
    {session ? <SidechatFindings key={id} session={session} messages={rows} /> : null}
    <div className="transcript" aria-label={copy("session.conversation_ccca18")}><Problem error={messages.error} />{messages.isPending ? <p>{copy("session.loadingConversation_5eb1e4")}</p> : rows.length ? rows.map((row) => <TranscriptItem key={row.id} resource={row} />) : <p className="empty">{copy("session.theConversationWillAppearHereAfter_24857a")}</p>}
      <nav aria-label={copy("session.conversationPages_72b1b9")}><button disabled={!page || messages.isFetching} onClick={() => { setPage(""); setPrevious([]); }}>{copy("session.firstPage_0bdbb7")}</button><button disabled={previous.length === 0 || messages.isFetching} onClick={() => { setPage(previous.at(-1)!); setPrevious(previous.slice(0, -1)); }}>{copy("session.previous_a57b08")}</button><button disabled={!next || messages.isFetching} onClick={() => { setPrevious([...previous.slice(-99), page]); setPage(next!); }}>{copy("session.next_1ff57a")}</button></nav>
    </div>
    <details className="queue"><summary><LocalizedText id="session.inputQueueWaiting_5228da" components={{ s0: <>{queued.filter((r) => text(readDocument(r).delivery) === "queued").length}</> }} /></summary><Problem error={queue.error} />
      {queued.map((r) => <QueuedInput key={r.id} resource={r} session={session} refresh={() => void queue.refetch()} />)}
      <nav aria-label={copy("session.queuePages_1acdd8")}><button disabled={!queuePage || queue.isFetching} onClick={() => setQueuePage("")}>{copy("session.firstPage_0bdbb7")}</button><button disabled={!queue.data?.nextPageToken || queue.isFetching} onClick={() => setQueuePage(queue.data!.nextPageToken)}>{copy("session.nextPage_c08ac7")}</button></nav>
    </details>
    <form className="composer" onSubmit={(event) => { event.preventDefault(); void send.send({ requestId: newRequestId(), sessionId: id, documentJson: encode({ prompt: draft, mode }) }); }}>
      <label htmlFor={`prompt-${id}`}>{copy("session.message_2f7766")}</label><textarea id={`prompt-${id}`} value={draft} onChange={(event) => setDraft(event.target.value)} disabled={locked} placeholder={copy("session.sendAFollowUpToThis_c9d723")} rows={3} />
      <div className="actions"><label>{copy("session.mode_cd20bc")}<select value={mode} disabled={locked} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>{copy("session.execute_e3a67d")}</option><option value={Mode.Plan}>{copy("session.plan_fa8ed0")}</option></select></label><button className="primary" disabled={locked || !draft.trim() || text(data.archive) !== "active"}>{copy("session.queueMessage_891d4e")}</button></div>
      <Problem error={send.error} />{send.uncertain ? <button type="button" disabled={send.busy} onClick={send.retry}>{copy("session.retryTheSameMessage_5656d9")}</button> : null}
    </form>
  </section>{panel === SessionPanel.Terminals && session ? <div id={`terminals-${id}`} className="session-app-panel"><SessionTerminals key={id} session={session} close={() => { setPanel(SessionPanel.Closed); terminalsButton.current?.focus(); }} /></div> : panel === SessionPanel.Files ? <div id={`files-${id}`} className="session-app-panel"><SessionFiles key={id} sessionId={id} close={() => { setPanel(SessionPanel.Closed); filesButton.current?.focus(); }} /></div> : panel === SessionPanel.Diff ? <div id={`diff-${id}`} className="session-app-panel"><SessionDiff key={id} sessionId={id} worktree={data.workspace === Workspace.Worktree} close={() => { setPanel(SessionPanel.Closed); diffButton.current?.focus(); }} /></div> : panel === SessionPanel.Diagnostics ? <div id={`diagnostics-${id}`} className="session-app-panel"><RequestDiagnostics key={id} sessionId={id} close={() => { setPanel(SessionPanel.Closed); diagnosticsButton.current?.focus(); }} /></div> : panel === SessionPanel.Browser && session ? <div id={`browser-${id}`} className="session-app-panel"><SessionBrowser key={`${id}:${browserAccountId}`} session={session} accountId={browserAccountId} close={() => { setPanel(SessionPanel.Closed); browserButton.current?.focus(); }} /></div> : null}</div>;
}
