import { NativeAutoReview } from "./native-auto-review";
import { SessionActivityProvider } from "./session-activity";
import { SessionTabBar } from "./session-tab-bar";
import { useSessionTabs, SessionTabKind, sessionTabKey } from "./session-tabs";
import { sessionProgress, progressMessages, progressResponseOwner, responseSuppressesProgress } from "./session-progress";
import { SessionProgressStatus } from "./session-progress-status";
import { ToolTurnTranscript } from "./tool-turn-transcript";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { NativeImageView } from "./native-image-view";
import { SessionHarness } from "./session-harness";
import { useSkillCompletion, type SkillTokenBinding } from "./skill-completion";
import { acknowledgeSessionSubmission, nativeSubmissionInput, submissionQueueReadable, SubmissionPhase, useSessionSubmissions } from "./session-submissions";
import { imageMime } from "./image-input";
import { ImageAttachmentInput, RetainedImages, imageEntryHandlers } from "./image-attachments";
import { useImageDraft, useImageRoute } from "./image-drafts";
import { RunnerTaskRemediation } from "./session-runner-remediation";
import { sessionControlEligibility, useSessionControl } from "./session-control";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useConversationDrafts } from "./conversation-drafts";
import { initialInteractionDraft, interactionRequestIdentity, type InboxInteractionDraft } from "./inbox-drafts";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { executionStartupFailure, canRetryExecutionStartup, startupCorrection, ExecutionStartupDetails } from "./execution-startup";

import { useShortcuts } from "./shortcut-provider";
import { ShortcutExecution, ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { Subagents } from "./subagents";
import { NativeGrokTool } from "./native-grok-interactions";
import { NativeGrokText, NativeGrokUser } from "./native-grok";
import { SessionBrowser } from "./session-browser";
import { SessionFiles, SessionFilePreview } from "./session-files";
import { RequestDiagnostics } from "./request-diagnostics";
import { SessionDiff } from "./session-diff";
import { NativeBuiltin, NativeWorkspaceEvent } from "./native-builtin";
import { NativeChanges, NativeRevision } from "./native-changes";
import { NativeTodo, NativeTodoProgress } from "./native-todo";
import { NativeUsage } from "./native-usage";
import { NativeRead } from "./native-read";
import { NativeShell } from "./native-shell";
import { NativeClaudeMessage, validNativeClaudeMessage } from "./native-claude-message";
import { NativeClaudeInterruption } from "./native-claude-interruption";
import { NativeContextCompaction } from "./native-context-compaction";
import { NativeClaudeProgress } from "./native-claude-progress";
import { NativeClaudeTool } from "./native-claude-tool";
import { NativeReasoning } from "./native-reasoning";
import { SessionContext } from "./session-context";
import { SessionBudget } from "./session-budget";
import { ExecutionConfiguration } from "./execution-configuration";
import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  ConnectionState, EntityKind, ResourceQuery, ResourceService, SessionAction, SessionQuery,
  SyncKind, newRequestId, synchronizeResources, clientFailure, supportsResourceSchema, type ClientFailure, type Resource,
} from "@delinoio/delidev-api-client";
import { document as readDocument, encode, items, Mode, object, resourceName, text, Workspace } from "./documents";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, Failure, Problem, failureSummary } from "./ui";
import { SessionActions, SessionIcon, SessionIconKind, SessionNotice } from "./session-presentation";
import "./session.css";
import { Interaction } from "./interactions";
import { SessionTerminals } from "./session-terminals";
import { SessionForkAction } from "./session-fork";
import { SidechatFindings } from "./sidechat";
import { useSidechatQuestionRetry, SidechatRetryAction, sidechatAnswerFilter } from "./sidechat-retry";
import { SessionTools } from "./session-tools";
import { SessionStorageAction } from "./session-storage";
import { SessionPullRequests } from "./session-pull-requests";
import { PendingQueueInputs, QueuedInput, type QueuedInputDraft } from "./queue";
import { StartupRejection } from "./startup-rejection";

function useSessionStream(id: string, active: boolean) {
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
    if (!active) return;
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
  }, [id, transport, restart, active]);
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

/** Queue history remains retained; only authoritative waiting inputs are presented. */
export function isQueuedInput(row: Resource): boolean {
  return text(readDocument(row).delivery) === "queued";
}

export function queueRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean): Resource[] {
  return appendedRows(base, live, removed, arrivals, sessionId, lastPage, EntityKind.QUEUE);
}

export function interactionRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean): Resource[] {
  return appendedRows(base, live, removed, arrivals, sessionId, lastPage, EntityKind.INTERACTION);
}

// Completion suppression belongs only to recognized conversation presentation.
function ConversationStatus({ state }: { state: string }) {
  return !state || state === "complete" || state === "completed" ? null : <header><small>{statusLabel(state)}</small></header>;
}

export const TranscriptItem = memo(function TranscriptItem({ resource, active = true, actions }: { resource: Resource; active?: boolean; actions?: ReactNode }) {
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
      data.artifact == null && data.progress == null && validNativeClaudeMessage(data.claude, text(data.state));
    return <article className={valid ? "message message-claude-assistant" : "message"} aria-label={copy("session.assistantMessage_8352f5")}>
      {valid ? <ConversationStatus state={text(data.state)} /> : <header><strong>{copy("session.assistant_a39a7f")}</strong><small>{statusLabel(text(data.state))}</small></header>}
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
  const textRole = data.tool == null && data.artifact == null && data.progress == null &&
    ["grok_text", "claude", "claude_tool", "claude_progress", "claude_interruption"].every(key => !Object.hasOwn(data, key));
  const roleClass = textRole && data.role === "user" ? " message-user" : textRole && data.role === "assistant" ? " message-assistant" : "";
  return <article className={`message${roleClass}`} aria-label={roleClass ? copy(data.role === "user" ? "session.userMessage" : "session.assistantMessage_8352f5") : copy("session.message_e9ca2b", { v0: text(data.role) || "Agent" })}>
    {roleClass ? <ConversationStatus state={text(data.state)} /> : <header><strong>{text(data.role) || copy("session.extra.11b39c93777e")}</strong><small>{statusLabel(text(data.state))}</small></header>}
    {text(data.text) ? <pre>{text(data.text)}</pre> : null}
    {data.role==="user"?actions:null}
    <RetainedImages value={data.attachments} sessionId={resource.sessionId} active={active} />
    {toolStarted.kind === "image-view" ? <NativeImageView tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-builtin" ? <NativeBuiltin tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-todo" ? <NativeTodo tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-read" ? <NativeRead tool={tool} state={text(data.state)} /> : toolStarted.kind === "opencode-shell" ? <NativeShell tool={tool} state={text(data.state)} /> : Object.keys(tool).length ? <Disclosure><DisclosureSummary><LocalizedText id="session.tool_844a02" components={{ s0: <>{text(toolStarted.kind) || copy("session.extra.fa176576233d")}</>, s1: <>{text(toolCompleted.status) || text(toolStarted.status)}</> }} /></DisclosureSummary>
      {text(command.command) ? <pre>{text(command.command)}</pre> : null}
      {text(command.cwd) ? <p><LocalizedText id="session.directory_369f13" components={{ s0: <>{text(command.cwd)}</> }} /></p> : null}
      {text(tool.output) ? <pre>{text(tool.output)}</pre> : null}
      {typeof command.aggregated_output === "string" ? <Disclosure><DisclosureSummary>{copy("session.nativeAggregateOutput_2d06e4")}</DisclosureSummary><pre>{command.aggregated_output}</pre></Disclosure> : null}
      {items((tool.completed ? toolCompleted : toolStarted).changes).map((item, index) => <pre key={index}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}
      {items(tool.patches).map((patch, index) => <Disclosure key={index}><DisclosureSummary><LocalizedText id="session.patchObservation_8b886e" components={{ s0: <>{index + 1}</> }} /></DisclosureSummary>{items(object(patch).changes).map((item, part) => <pre key={part}>{text(object(item).path)}{"\n"}{text(object(item).diff)}</pre>)}</Disclosure>)}
      {items(tool.inputs).map((input, index) => <pre key={index}>Tool input: {text(object(object(input).input).text)}</pre>)}
    </Disclosure> : null}
    {started.kind === "opencode-revision" ? <NativeRevision artifact={artifact} state={text(data.state)} /> : started.kind === "reasoning-text" ? <NativeReasoning artifact={artifact} state={text(data.state)} /> : Object.keys(artifact).length ? <Disclosure open><DisclosureSummary>{text(started.kind) || copy("session.extra.a3e40dda2eb1")}</DisclosureSummary>
      {text(started.text) ? <pre>{text(started.text)}</pre> : null}
      {[...items(started.summary), ...items(started.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}
      {items(artifact.deltas).length ? <Disclosure><DisclosureSummary>{copy("session.streamedObservations_589dae")}</DisclosureSummary>{items(artifact.deltas).map((item, index) => { const delta = object(object(item).delta); return <pre key={index}>{text(delta.kind)}{typeof delta.index === "number" ? ` ${delta.index}` : ""}: {text(delta.text)}</pre>; })}</Disclosure> : null}
      {artifact.completed ? <section aria-label={copy("session.completedArtifact_de26fd")}><h3>{copy("session.completedArtifact_de26fd")}</h3><pre>{text(completed.text)}</pre>{[...items(completed.summary), ...items(completed.content)].map((part, index) => <pre key={index}>{text(part)}</pre>)}</section> : null}
    </Disclosure> : null}
    {progress.kind === "codex-auto-review" ? <NativeAutoReview progress={progress} state={text(data.state)} /> : progress.kind === "native-compaction" ? <NativeContextCompaction progress={progress} state={text(data.state)} /> : progress.kind === "opencode-workspace" ? <NativeWorkspaceEvent progress={progress} state={text(data.state)} /> : progress.kind === "opencode-changes" ? <NativeChanges progress={progress} state={text(data.state)} turn={text(data.native_turn_id)} /> : progress.kind === "opencode-todo" ? <NativeTodoProgress progress={progress} state={text(data.state)} /> : Object.keys(progress).length ? <Disclosure open><DisclosureSummary><LocalizedText id="session.progress_a4d877" components={{ s0: <>{text(progress.kind)}</> }} /></DisclosureSummary><pre>{text(progress.diff) || text(plan.explanation)}</pre><ol>{items(plan.steps).map((step, index) => <li key={index}>{text(object(step).step)} · {text(object(step).status)}</li>)}</ol></Disclosure> : null}
  </article>;
});

const submissionLabels = {
  [SubmissionPhase.Preparing]: "session.submissionPreparing", [SubmissionPhase.Sending]: "session.submissionSending",
  [SubmissionPhase.Queued]: "session.submissionQueued", [SubmissionPhase.Claimed]: "session.submissionClaimed",
  [SubmissionPhase.Accepted]: "session.submissionAccepted", [SubmissionPhase.Uncertain]: "session.submissionUncertain",
  [SubmissionPhase.Rejected]: "session.submissionRejected", [SubmissionPhase.Removed]: "session.submissionRemoved",
} as const;

const tabShortcutIds = [ShortcutId.SessionTab1, ShortcutId.SessionTab2, ShortcutId.SessionTab3,
  ShortcutId.SessionTab4, ShortcutId.SessionTab5, ShortcutId.SessionTab6,
  ShortcutId.SessionTab7, ShortcutId.SessionTab8, ShortcutId.SessionTab9] as const;
enum SessionPanel { Closed = "closed", Files = "files", Diff = "diff", Terminals = "terminals", Browser = "browser", Diagnostics = "diagnostics" }
enum InfoTarget { Status = "status", Recovery = "recovery", Budget = "budget" }

export function SubmissionStatus({ phase }: { phase: SubmissionPhase }) {
  useLocale();
  return <header><small role="status">{copy(submissionLabels[phase])}</small></header>;
}

export function SessionView({ id, draft, setDraft, initialSkills, changeSkills, active = true, embedded = false }: { id: string; draft: string; setDraft: (value: string, bindings?: SkillTokenBinding[]) => boolean | void; initialSkills?: SkillTokenBinding[]; changeSkills?: (bindings: SkillTokenBinding[]) => void; active?: boolean; embedded?: boolean; openRunnerSettings?: () => void }) {
  useLocale();
  const tabs = useSessionTabs(id);
  const conversationActive = active && (embedded || tabs.tab.kind === SessionTabKind.Conversation);
  // Session metadata owns account and native presentation authority even when
  // a resource pane hides the conversation. Only transcript reads pause there.
  const live = useSessionStream(id, active && tabs.tab.kind !== SessionTabKind.Sidechat);
  const submissions = useSessionSubmissions();
  const [submissionError, setSubmissionError] = useState<unknown>();
  const [revealSubmission, setRevealSubmission] = useState<string>();
  const submissionTransport = useTransport();
  useEffect(() => {
    if (!conversationActive) return;
    const controller = new AbortController();
    const client = createClient(ResourceService, submissionTransport);
    const originals = submissions.store.snapshot().filter(row => row.sessionId === id && row.queueId && !row.native);
    // ListQueue excludes removed tombstones. Reinspect only retained original
    // IDs, serially, when this view or its authenticated transport is replaced.
    void (async () => {
      for (const original of originals) {
        if (controller.signal.aborted || !submissions.store.alive) return;
        try {
          const result = await client.getResource({ kind: EntityKind.QUEUE, id: original.queueId! }, { signal: controller.signal });
          if (controller.signal.aborted || !submissions.store.alive) return;
          const row = result.resource;
          if (!row || row.id !== original.queueId || row.sessionId !== id || row.kind !== EntityKind.QUEUE || row.revision < 1n || !supportsResourceSchema(row) || !submissionQueueReadable(row)) throw new ConnectError("The original input status could not be verified.", Code.DataLoss);
          submissions.store.observe(id, [row], true);
        } catch (error) {
          if (controller.signal.aborted || !submissions.store.alive) return;
          submissions.store.observationFailed(original.queueId!);
          console.warn("delidev.session_input.observation_failed", { classification: clientFailure(error).code });
        }
      }
    })();
    return () => controller.abort();
  }, [id, conversationActive, submissionTransport, submissions.store]);
  const panel = tabs.tab.kind === SessionTabKind.Conversation ? SessionPanel.Closed : tabs.tab.kind === SessionTabKind.Files ? SessionPanel.Files : tabs.tab.kind === SessionTabKind.Diff ? SessionPanel.Diff : tabs.tab.kind === SessionTabKind.Diagnostics ? SessionPanel.Diagnostics : tabs.tab.kind === SessionTabKind.Page || tabs.tab.kind === SessionTabKind.Browser ? SessionPanel.Browser : SessionPanel.Closed;
  const conversationRegion = useRef<HTMLDivElement>(null);
  const upperContent = useRef<HTMLDivElement>(null);
  const [filesOpened,setFilesOpened]=useState(false);
  const [terminalOpened, setTerminalOpened] = useState(false);
  const [browserOpened,setBrowserOpened]=useState(false);
  const [recoveryLauncherTarget, setRecoveryLauncherTarget] = useState<HTMLDivElement | null>(null);
  const [infoToolsTarget, setInfoToolsTarget] = useState<HTMLDivElement | null>(null);
  const terminalsButton = useRef<HTMLButtonElement>(null);
  const filesButton = useRef<HTMLButtonElement>(null);
  const diffButton = useRef<HTMLButtonElement>(null);
  const diagnosticsButton = useRef<HTMLButtonElement>(null);
  const browserButton = useRef<HTMLButtonElement>(null);
  const infoHeading = useRef<HTMLHeadingElement>(null);
  const infoEvidence = useRef<HTMLDivElement>(null);
  const budgetDetails = useRef<HTMLDetailsElement>(null);
  const information = useRef<HTMLElement>(null);
  const [prOpen, setPROpen] = useState(true), [subagentsOpen, setSubagentsOpen] = useState(true);
  const [infoReveal, setInfoReveal] = useState<{ target: InfoTarget }>();
  useLayoutEffect(() => {
    if (!infoReveal) return;
    const target = infoReveal.target === InfoTarget.Budget ? budgetDetails.current : infoReveal.target === InfoTarget.Recovery ? infoToolsTarget : infoEvidence.current;
    if (target instanceof HTMLDetailsElement) target.open = true;
    else {
      target?.querySelectorAll("details").forEach(details => { details.open = true; });
      const recovery = information.current?.querySelector<HTMLDetailsElement>(".session-tools");
      if (recovery) recovery.open = true;
    }
    const focusTarget = infoReveal.target === InfoTarget.Recovery ? target?.querySelector<HTMLButtonElement>(".notice button") : target;
    focusTarget?.focus({ preventScroll: true });
    focusTarget?.scrollIntoView?.({ block: "nearest" });
  }, [infoReveal]);
 const [budgetBlocked,setBudgetBlocked]=useState(false);
 const [runnerRemediationPending, setRunnerRemediationPending] = useState(false);
  const queueDrafts = useConversationDrafts<QueuedInputDraft>();
  const requestDrafts = useConversationDrafts<InboxInteractionDraft>();
  const interactionRow = (row: Resource) => {
    const draft = requestDrafts.values.get(row.id) ?? initialInteractionDraft(row);
    return <Interaction key={row.id} resource={row} refresh={interactions.refresh} draft={draft} saveDraft={editable => { if (draft) requestDrafts.save(row.id, { ...draft, editable }); }} clearDraft={() => requestDrafts.save(row.id)} submissionAllowed={!interactions.error && (!draft || draft.requestIdentity === interactionRequestIdentity(row))} />;
  };
  const transcriptRoot = useRef<HTMLDivElement>(null), requestsRoot = useRef<HTMLDivElement>(null), queueRoot = useRef<HTMLDivElement>(null);
  const [requestsOpen, setRequestsOpen] = useState(false), [queueOpen, setQueueOpen] = useState(false);
  const [mode, setMode] = useState(Mode.Execute);
  const messages = useConversationPages(EntityKind.MESSAGE, id, conversationActive && live.generation > 0);
  const queue = useConversationPages(EntityKind.QUEUE, id, conversationActive && live.generation > 0);
  const interactions = useConversationPages(EntityKind.INTERACTION, id, conversationActive && live.generation > 0, 20);
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const observed = live.resources.get(id);
  const original = observed && acknowledged && acknowledged.revision > observed.revision ? acknowledged : observed;
  const { resource: session, control, action } = useSessionControl(id, original, active && tabs.tab.kind!==SessionTabKind.Sidechat);
  const retryQuestion=useSidechatQuestionRetry(session,conversationActive);
  const historyHeights=useRef(new Map<string,number>());
  const historyRoot=useRef<HTMLDivElement>(null);
  const data = readDocument(session);
  const startupFailure = executionStartupFailure(data);
  const startupRetry = canRetryExecutionStartup(data);
  const inlineRecovery = Boolean(startupFailure || Object.hasOwn(data, "startup_rejection") || data.recovery === "required" || ["failed", "canceled", "uncertain"].includes(text(object(data.preparation).state)));
  // A paused account switch selects the next account without rewriting the
  // preceding execution. Match the server's continuation selection immediately.
  const accountChanges = items(data.account_changes);
  const browserAccountId = accountChanges.length
    ? text(object(accountChanges.at(-1)).account_id)
    : text(object(data.current_execution).account_id) || text(object(data.initial_execution).initial_account_id);
  const rows = useMemo(() => {
    // The stream records creation order. UUIDs from different Workers are not
    // an append sequence, even when each Worker generates UUID-v7 values.
    return messageRows(messages.data?.resources ?? [], live.resources, live.removed, live.newMessageIds, id, !!messages.data && !messages.data.nextPageToken);
  }, [messages.data, live.resources, live.removed, live.newMessageIds, id]);
  useEffect(() => { if (conversationActive && live.generation > 1) { void messages.refresh(); void queue.refresh(); void interactions.refresh(); } }, [live.generation, conversationActive]);
  const images = useImageDraft(`session:${id}`);
  const [imageTextLimit, setImageTextLimit] = useState(false);
  const imageRoute = useImageRoute(text(data.machine_id), text(data.agent_id), conversationActive, images.images.length > 0, object(data.fork).sidechat_parent_snapshot ? "sidechat" : text(object(object(data.initial_execution).configuration).harness) || text(object(object(object(data.fork).snapshot).configuration).harness));
  const send = useRetainedMutation(`enqueue:${id}`, SessionQuery.enqueueInput, (_result, request) => { images.controller.accepted(request.requestId, request.attachments.map(image => image.id)); setDraft(""); skills.clearAccepted(); void queue.refresh(); }, acknowledgeSessionSubmission);
  useEffect(() => { if (send.error && !send.uncertain && !send.busy) images.controller.operationId = undefined; }, [send.error, send.uncertain, send.busy, images.controller]);
  const locked = send.busy || send.uncertain || images.busy || submissions.store.preparing(id);
  const composer = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    const input = composer.current;
    if (!input) return;
    input.style.height = "auto";
    const maximum = Number.parseFloat(getComputedStyle(input).maxHeight);
    input.style.height = `${Math.min(input.scrollHeight, Number.isFinite(maximum) ? maximum : 180)}px`;
  }, [draft, session?.id]);
  useLayoutEffect(() => {
    const input = composer.current;
    if (!input) return;
    let width = input.clientWidth;
    const workspace = input.closest<HTMLElement>(".session-workspace");
    // Conversation containers can be shorter than the retained workspace after
    // Info reflows. Pin this cap to its original owner, not the nearest cqh scope.
    const cap = () => {
      if (workspace && workspace.clientHeight > 0) workspace.style.setProperty("--session-composer-cap", `${workspace.clientHeight / 2}px`);
    };
    const fit = () => {
      cap();
      input.style.height = "auto";
      const maximum = Number.parseFloat(getComputedStyle(input).maxHeight);
      input.style.height = `${Math.min(input.scrollHeight, Number.isFinite(maximum) ? maximum : 180)}px`;
    };
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(() => { cap(); if (width !== input.clientWidth) { width = input.clientWidth; fit(); } });
    observer?.observe(input);
    if (workspace) observer?.observe(workspace);
    cap();
    window.addEventListener("resize", fit);
    return () => { observer?.disconnect(); workspace?.style.removeProperty("--session-composer-cap"); window.removeEventListener("resize", fit); };
  }, [session?.id]);
  const skills = useSkillCompletion({ value: draft, change: (value, bindings) => { if (new TextEncoder().encode(value).byteLength > (256 << 10)) { setImageTextLimit(true); return false; } setImageTextLimit(false); return setDraft(value, bindings); }, textarea: composer, machineId: text(data.machine_id), agentId: text(data.agent_id), sessionId: id, initialBindings: initialSkills, bindingsChanged: changeSkills, retainTransportContext: Boolean(changeSkills), active: conversationActive, disabled: locked });
  const canSend = !locked && !skills.blocked && new TextEncoder().encode(draft).byteLength <= (256 << 10) && Boolean(draft.trim() || images.images.length) && (!images.images.length || imageRoute.ready) && text(data.archive) === "active";
  const enqueue = async () => {
    if (!canSend) return;
    const requestId = images.images.length ? images.controller.operationId ?? newRequestId() : newRequestId();
    const original = encode({ prompt: draft, mode });
    const selections = skills.selections.map(selection => ({ ...selection }));
    const attachmentCount = images.images.length, machine = imageRoute.machine;
    try {
      if (!submissions.store.freeze(requestId, id, draft, mode, attachmentCount)) return;
    } catch (error) { setSubmissionError(error); return; }
    setSubmissionError(undefined); setRevealSubmission(requestId);
    let attachments;
    try { attachments = attachmentCount && machine ? await images.controller.prepare(machine, requestId) : []; }
    catch { submissions.store.preparationFailed(requestId); return; }
    if (!submissions.store.alive) return;
    submissions.store.prepared(requestId, attachments);
    void send.send({ requestId, sessionId: id, documentJson: original, skills: selections.length ? { selections } : undefined, attachments });
  };
  const shortcuts = useShortcuts([
    ...(!embedded ? Array.from({length:9},(_,index)=>({id: tabShortcutIds[index]!,scope:Surface.Sessions,label:`shortcuts.tab${index+1}` as import("./localization").MessageKey,bindings:[{key:String(index+1),primary:true}],input:ShortcutInput.Allow,terminal:true,active,enabled:index<tabs.tabs.length,run:()=>{document.getElementById(`session-tab-${id}-${index}`)?.focus({preventScroll:true});tabs.store.position(id,index+1);}})) : []),
    { id: ShortcutId.SessionFocus, scope: Surface.Sessions, label: "shortcuts.focusMessage", bindings: [{ key: "i", primary: true }], input: ShortcutInput.Allow, active: conversationActive, enabled: !locked, unavailableReason: "shortcuts.pending", run: () => composer.current?.focus() },
    { id: ShortcutId.SessionSend, scope: Surface.Sessions, label: "shortcuts.queueMessage", bindings: [{ key: "Enter", primary: true }], target: composer, input: ShortcutInput.Target, active: conversationActive, enabled: canSend, unavailableReason: locked ? "shortcuts.pending" : text(data.archive) !== "active" ? "shortcuts.activeSessionRequired" : "shortcuts.messageRequired", run: () => composer.current?.form?.requestSubmit() },
    { id: ShortcutId.SessionNewline, scope: Surface.Sessions, label: "shortcuts.newline", bindings: [{ key: "Enter" }], target: composer, input: ShortcutInput.Target, execution: ShortcutExecution.Native, active: conversationActive, enabled: !locked, unavailableReason: "shortcuts.pending" },
  ]);
  // Stream arrivals have exact identities even when their JSON sequence exceeds
  // JavaScript's safe-integer range. Append only arrivals on the final page.
  const pending = queueRows(queue.data?.inputs ?? [], live.resources, live.removed, live.newQueueIds, id, !!queue.data && !queue.data.nextPageToken);
  useEffect(() => {
    submissions.store.observe(id, [...(messages.data?.resources ?? []), ...pending, ...live.resources.values()]);
  }, [id, messages.data, queue.data, live.resources, submissions.store, submissions.rows]);
  useLayoutEffect(() => {
    if (!revealSubmission || !active) return;
    const target = transcriptRoot.current?.querySelector<HTMLElement>(`[data-submission="${revealSubmission}"]`);
    target?.scrollIntoView?.({ block: "nearest" });
    setRevealSubmission(undefined);
  }, [revealSubmission, active]);
  const requests = interactionRows(interactions.data?.resources ?? [], live.resources, live.removed, live.newInteractionIds, id, !!interactions.data && !interactions.data.nextPageToken);
  const queued = pending.filter(isQueuedInput);
  const presentedQueueIds = new Set(queue.payloadPages.flatMap(page => queueRows(page.payload, live.resources, live.removed, [], id, false).filter(isQueuedInput).map(row => row.id)).concat(!queue.nextPageToken ? queued.filter(row => !queue.rows.some(known => known.id === row.id)).map(row => row.id) : []));
  const panelButtons = {
    [SessionPanel.Files]: filesButton, [SessionPanel.Diff]: diffButton,
    [SessionPanel.Terminals]: terminalsButton, [SessionPanel.Browser]: browserButton,
    [SessionPanel.Diagnostics]: diagnosticsButton,
  };
  const closeTab = (key:string) => {
    const index=tabs.tabs.findIndex(tab=>sessionTabKey(tab)===key);
    if(index<=0)return;
    const target=tabs.selected===key?index-1:tabs.tabs.findIndex(tab=>sessionTabKey(tab)===tabs.selected);
    document.getElementById(`session-tab-${id}-${target}`)?.focus({preventScroll:true});
    tabs.store.close(id,key);
  };
  const closePanel = () => closeTab(tabs.selected);
  const closeTerminal = closePanel;
  const togglePanel = (next: Exclude<SessionPanel, SessionPanel.Closed>) => {
    if(next===SessionPanel.Files)setFilesOpened(true);
    if(next === SessionPanel.Terminals) setTerminalOpened(true);
    if(next === SessionPanel.Browser) setBrowserOpened(true);
    const kinds={ [SessionPanel.Files]:SessionTabKind.Files,[SessionPanel.Diff]:SessionTabKind.Diff,[SessionPanel.Terminals]:SessionTabKind.Terminals,[SessionPanel.Browser]:SessionTabKind.Browser,[SessionPanel.Diagnostics]:SessionTabKind.Diagnostics } as const;
    tabs.store.open(id, {kind:kinds[next]});
  };
  // Revealing Info preserves the selected resource and its original controller.
  const showInfo = (_opener: HTMLButtonElement, target = InfoTarget.Status) => {
    setInfoReveal({ target });
  };
  const problem = object(data.problem);
  const recovering = text(data.recovery) !== "none" && Boolean(text(data.recovery));
  const connectionLabel = live.state === ConnectionState.Live ? copy("session.connected_229655")
    : live.state === ConnectionState.Reconnecting ? copy("session.connectionLostRetainedStateShown_8cc737")
    : live.state === ConnectionState.Failed ? copy("session.connectionRequiresAttention_160d4a") : copy("session.connecting_72021e");
  const projectedSubmissions = submissions.rows.filter(row => row.sessionId === id && !row.native && !rows.some(message => {
    return Boolean(row.queueId) && row.queueId === nativeSubmissionInput(message);
  }));
  const tools = [
    { panel: SessionPanel.Diff, icon: SessionIconKind.Diff, label: copy("session.diff_7ecf46") },
    { panel: SessionPanel.Files, icon: SessionIconKind.Files, label: copy("session.files_abc7e9") },
    { panel: SessionPanel.Terminals, icon: SessionIconKind.Terminals, label: copy("session.terminals_7482c4") },
    { panel: SessionPanel.Browser, icon: SessionIconKind.Browser, label: copy("session.browser_d31de1") },
    { panel: SessionPanel.Diagnostics, icon: SessionIconKind.Diagnostics, label: copy("session.diagnostics_268f14") },
  ] as const;
  const progressRevision = useRef({ id, revision: 0n });
  const progressResponse = useRef<{ owner?: string; seen: boolean }>({ seen: false });
  const progressRows = progressMessages(messages.rows, live.resources, live.removed, id);
  const progressOwner = progressResponseOwner(session, id);
  useLayoutEffect(() => {
    if (!progressOwner || session && progressRevision.current.id === id && session.revision < progressRevision.current.revision) return;
    if (progressResponse.current.owner !== progressOwner.key) progressResponse.current = { owner: progressOwner.key, seen: false };
    if (responseSuppressesProgress(progressRows, progressOwner)) progressResponse.current.seen = true;
  });
  useLayoutEffect(() => {
    if (progressRevision.current.id !== id) progressRevision.current = { id, revision: 0n };
    if (session?.id === id && session.revision > progressRevision.current.revision) progressRevision.current.revision = session.revision;
  }, [id, session]);
  const progress = sessionProgress({ session, sessionId: id,
    current: (progressRevision.current.id !== id || Boolean(session && session.revision >= progressRevision.current.revision)) && conversationActive && live.state === ConnectionState.Live && !live.error && !queue.error && !interactions.error && !messages.error && !messages.isPending,
    complete: Boolean(messages.data) && !messages.nextPageToken,
    blocked: Boolean(progressOwner && progressResponse.current.owner === progressOwner.key && progressResponse.current.seen) || budgetBlocked || runnerRemediationPending || control.busy || control.uncertain || Boolean(control.error) || requests.some(row => readDocument(row).closure === "open") || pending.some(row => readDocument(row).delivery === "uncertain") || projectedSubmissions.some(row => row.observationUnavailable || row.phase === SubmissionPhase.Uncertain),
    messages: progressRows,
    queueCurrent: Boolean(queue.data) && !queue.error && !queue.isPending && !queue.nextPageToken,
    queue: pending,
  });
  return <SessionActivityProvider active={active && tabs.tab.kind!==SessionTabKind.Sidechat}><section className="session-workspace session-tabbed" aria-label={copy("session.currentSession_a32789")} onKeyDown={event => {
    if (event.key === "Escape" && event.target instanceof Node && upperContent.current?.contains(event.target) && !(event.target instanceof Element && event.target.closest("[data-shortcuts=passthrough]")) && tabs.tab.kind !== SessionTabKind.Conversation && !(event.target instanceof Element && event.target.closest("dialog[open]"))) {
      event.stopPropagation(); if (event.target instanceof Element && event.target.closest(".terminal-dock")) closeTerminal(); else if (panel !== SessionPanel.Closed) closePanel(); else closeTerminal();
    }
  }}>
    <header className="session-header">
      <div className="session-heading">
        <SessionHarness resource={session}><div className="session-heading-line"><h2>{resourceName(session)}</h2><p className={`connection${live.state === ConnectionState.Live ? " is-live" : ""}`} role="status">{connectionLabel}</p></div></SessionHarness>
      </div>
      <div className="session-controls" hidden={!embedded && tabs.tab.kind===SessionTabKind.Sidechat} inert={!embedded && tabs.tab.kind===SessionTabKind.Sidechat}>
        <button type="button" disabled={!session || control.busy || control.uncertain} onClick={() => action(SessionAction.STOP)}>{copy("session.stop_cae7d5")}</button>
        <button type="button" disabled={!session || control.busy || control.uncertain || runnerRemediationPending || !sessionControlEligibility(session, budgetBlocked).resume} onClick={() => action(SessionAction.RESUME)}>{startupRetry ? copy("session.startupRetry") : copy("session.resume_d640c7")}</button>
        <SessionActions>
          {session ? <SessionForkAction source={session} disabled={control.busy || control.uncertain} /> : null}
          <button type="button" disabled={!session || control.busy || control.uncertain} onClick={() => action(text(data.archive) === "archived" ? SessionAction.RESTORE : SessionAction.ARCHIVE)}>{text(data.archive) === "archived" ? copy("session.restore_a76e13") : copy("session.archive_66f480")}</button>
        </SessionActions>
      </div>
    </header>
    {!embedded ? <><div className="session-toolbar"><div className="session-toolbar-actions" role="group" aria-label={copy("session.workspaceTools")}>{tools.map(tool => <button key={tool.panel} type="button" ref={panelButtons[tool.panel]} disabled={tool.panel===SessionPanel.Terminals&&Boolean(object(data.fork).sidechat_parent_snapshot)} onClick={()=>togglePanel(tool.panel)}><SessionIcon kind={tool.icon}/>{tool.label}</button>)}</div></div><SessionTabBar id={id} tabs={tabs.tabs} selected={tabs.selected} select={key=>tabs.store.select(id,key)} close={closeTab}/></> : null}
    <div className="session-content">
    <div id={`session-pane-${id}`} role={embedded ? undefined : "tabpanel"} aria-labelledby={embedded ? undefined : `session-tab-${id}-${tabs.tabs.findIndex(tab=>sessionTabKey(tab)===tabs.selected)}`} ref={upperContent} className="session-upper-content">
    <div ref={conversationRegion} className="session-conversation-region">
    <SessionActivityProvider active={conversationActive}><div className="session-body" hidden={!conversationActive} inert={!conversationActive}>
      <div className="session-notices">
        {live.error || live.state === ConnectionState.Failed ? <SessionNotice details={opener => showInfo(opener)}>{live.error ? failureSummary(live.error.code) : connectionLabel}</SessionNotice> : null}
        {startupFailure ? <SessionNotice><strong>{copy("session.startupFailed")}</strong><span>{startupFailure.state === 2 ? startupCorrection(startupFailure) : copy("session.startupRecover")}</span><ExecutionStartupDetails key={text(startupFailure.correlation_id)} failure={startupFailure} />{startupFailure.state === 2 ? <p>{copy("session.startupManualSteps")}</p> : null}</SessionNotice> : Object.keys(object(object(data.startup).failure)).length ? <p role="alert">{copy("session.startupInvalidEvidence")}</p> : null}
        {text(problem.message) && !startupFailure ? <SessionNotice details={opener => showInfo(opener)}><strong>{text(data.dispatch) === "blocked" ? copy("session.executionBlocked") : copy("session.attentionRequired")}</strong><span>{failureSummary(text(problem.code) || text(problem.problem_code))}</span></SessionNotice> : null}
        {recovering ? <SessionNotice details={opener => showInfo(opener)}><LocalizedText id="session.recoveryExecutionRemainsUnderServerControl_d80aa1" components={{ s0: <>{statusLabel(text(data.recovery))}</> }} /></SessionNotice> : null}
        {session && Object.hasOwn(data, "startup_rejection") ? <StartupRejection session={session} /> : null}
        {budgetBlocked ? <SessionNotice details={opener => showInfo(opener, InfoTarget.Budget)}>{copy("session-budget.budgetThresholdReachedNewTurnsAnd_6236ce")}</SessionNotice> : null}
        {control.error ? <SessionNotice details={opener => showInfo(opener)}>{failureSummary(clientFailure(control.error).code)}</SessionNotice> : null}
        {control.uncertain ? <SessionNotice details={opener => showInfo(opener)}><span>{copy("session.startupControlUncertain")}</span><button type="button" onClick={control.retry} disabled={control.busy}>{copy("session.retryTheSameControlRequest_609aff")}</button></SessionNotice> : null}
        {submissionError ? <SessionNotice>{failureSummary(clientFailure(submissionError).code)}</SessionNotice> : null}
        {send.error ? <SessionNotice details={opener => showInfo(opener)}>{failureSummary(clientFailure(send.error).code)}</SessionNotice> : null}
      <RunnerTaskRemediation active={conversationActive} machineId={text(data.machine_id)} disabled={control.busy || control.uncertain} visible={Boolean(startupFailure)} onPending={setRunnerRemediationPending} />
        <div ref={setRecoveryLauncherTarget} hidden={!inlineRecovery} />
      </div>
      {session ? <SessionActivityProvider active={active && tabs.tab.kind!==SessionTabKind.Sidechat}><SessionTools resource={session} changed={setAcknowledged} initiallyOpen target={infoToolsTarget} launcherTarget={inlineRecovery && conversationActive ? recoveryLauncherTarget : undefined} openRecovery={opener => showInfo(opener, InfoTarget.Recovery)}><div className="session-information-evidence" ref={infoEvidence} tabIndex={-1}>
          <Failure failure={live.error} />{live.state === ConnectionState.Failed ? <button onClick={live.retry}>{copy("session.refreshConnection_73791f")}</button> : null}
          {recovering ? <p className="notice"><LocalizedText id="session.recoveryExecutionRemainsUnderServerControl_d80aa1" components={{ s0: <>{statusLabel(text(data.recovery))}</> }} /></p> : null}
          {text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p>{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}
          <Problem error={control.error} />
          <Problem error={send.error} />
        </div></SessionTools></SessionActivityProvider> : null}
      <div ref={transcriptRoot} className="transcript" aria-label={copy("session.conversation_ccca18")}>
        <Failure failure={messages.error?.failure} />
        {messages.error && messages.data ? <p className="notice">{copy("session.retainedConversation")}</p> : null}
        {messages.isPending ? <p role="status">{copy("session.loadingConversation_5eb1e4")}</p> : rows.length || messages.rows.length ? <ToolTurnTranscript key={`tools:${id}`} sessionId={id} active={conversationActive} query={messages} live={live.resources} removed={live.removed} arrivals={live.newMessageIds} root={transcriptRoot} include={sidechatAnswerFilter(session)} render={row => <TranscriptItem key={row.id} resource={row} active={conversationActive} actions={<SidechatRetryAction controller={retryQuestion} inputId={text(readDocument(row).input_id)}/>}/>} /> : messages.error ? <p>{copy("session.conversationUnavailable")}</p> : progress ? null : projectedSubmissions.length ? null : <div className="session-empty"><SessionIcon kind={SessionIconKind.Conversation} /><h3>{copy("session.emptyConversation")}</h3><p>{copy("session.theConversationWillAppearHereAfter_24857a")}</p></div>}
        {progress ? <SessionProgressStatus phase={progress} compact={Boolean(rows.length || messages.rows.length || projectedSubmissions.length)} /> : null}
        {items(data.sidechat_retries).length ? <Disclosure className="sidechat-answer-history"><DisclosureSummary>{copy("sidechat.retry.history")}</DisclosureSummary><p>{copy("sidechat.retry.retainedHistory")}</p><div className="sidechat-history-content" ref={historyRoot}><ToolTurnTranscript sessionId={id} active={conversationActive} query={{...messages,pages:messages.pages.map(page=>({...page,height:historyHeights.current.get(page.token)})),measure:(token,height)=>{historyHeights.current.set(token,height);}}} live={live.resources} removed={live.removed} arrivals={live.newMessageIds} root={historyRoot} include={sidechatAnswerFilter(session,true)} render={row=><TranscriptItem resource={row} active={conversationActive}/>} /></div></Disclosure> : null}
        <ScrollContinuation query={messages} root={transcriptRoot} active={conversationActive && live.generation > 0} label={copy("session.conversationPages_72b1b9")} />
        {retryQuestion.sidechat && retryQuestion.unsupported ? <p role="status">{copy("sidechat.retry.unsupported")}</p> : null}
        {session ? <SidechatFindings key={id} session={session} messages={rows} /> : null}
        {projectedSubmissions.map(row => <article key={row.requestId} data-submission={row.requestId} className="message message-user" aria-label={copy("session.submittedUserMessage")}>
          <SubmissionStatus phase={row.observationUnavailable ? SubmissionPhase.Uncertain : row.phase} />
          {row.prompt ? <pre>{row.prompt}</pre> : null}
          {row.attachments.length ? <RetainedImages value={row.attachments.map(image => ({ id: image.id, machine_id: image.machineId, media_type: imageMime[image.mediaType], byte_length: Number(image.byteLength), sha256: image.sha256 }))} sessionId={id} active={conversationActive} /> : row.attachmentCount ? <p>{copy("session.submittedImages", { count: row.attachmentCount })}</p> : null}
        </article>)}
      </div>
      <div className="session-input-tray">
        <Disclosure className="requests" open={requests.some(r => readDocument(r).closure === "open") || requestsOpen} onToggle={event => setRequestsOpen(event.currentTarget.open)}><DisclosureSummary>{interactions.isPending ? copy("session.loadingRequests") : <LocalizedText id="session.agentRequestsOnThisPage_5e8644" components={{ s0: <>{requests.length}</> }} />}</DisclosureSummary>
          <div ref={requestsRoot} className="session-tray-content"><Failure failure={interactions.error?.failure} />
            <ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={interactions} root={requestsRoot} active={conversationActive && requestsOpen}>{payload => interactionRows(payload, live.resources, live.removed, [], id, false).map(interactionRow)}</ScrollPayloadWindow>{!interactions.nextPageToken ? requests.filter(row => !interactions.rows.some(known => known.id === row.id)).map(interactionRow) : null}
            <ScrollContinuation query={interactions} root={requestsRoot} active={conversationActive && requestsOpen} label={copy("session.requestPages_d06a30")} />
          </div>
        </Disclosure>
        <PendingQueueInputs sessionId={id} presentInputIds={presentedQueueIds} refresh={queue.refresh} />
        <Disclosure className="queue" onToggle={event => setQueueOpen(event.currentTarget.open)}><DisclosureSummary>{queue.isPending ? copy("session.loadingQueue") : <LocalizedText id="session.inputQueueWaiting_5228da" components={{ s0: <>{queued.length}</> }} />}</DisclosureSummary>
          <div ref={queueRoot} className="session-tray-content"><Failure failure={queue.error?.failure} />
            <ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={queue} root={queueRoot} active={conversationActive && queueOpen}>{payload => queueRows(payload, live.resources, live.removed, [], id, false).filter(isQueuedInput).map(row => <QueuedInput active={conversationActive && queueOpen} key={row.id} resource={row} session={session} refresh={queue.refresh} draft={queueDrafts.values.get(row.id)} changeDraft={value => queueDrafts.save(row.id, value)} readOnly={Boolean(queue.error)} />)}</ScrollPayloadWindow>{!queue.nextPageToken ? queued.filter(row => !queue.rows.some(known => known.id === row.id)).map(row => <QueuedInput active={conversationActive && queueOpen} key={row.id} resource={row} session={session} refresh={queue.refresh} draft={queueDrafts.values.get(row.id)} changeDraft={value => queueDrafts.save(row.id, value)} readOnly={Boolean(queue.error)} />) : null}
            <ScrollContinuation query={queue} root={queueRoot} active={conversationActive && queueOpen} label={copy("session.queuePages_1acdd8")} />
          </div>
        </Disclosure>
      </div>
      <form className="composer" {...imageEntryHandlers(images, locked || !imageRoute.systemSupported)} onSubmit={event => { event.preventDefault(); enqueue(); }}>
        <label className="sidebar-sr-only" htmlFor={`prompt-${id}`}>{copy("session.message_2f7766")}</label>
        <ImageAttachmentInput compact active={conversationActive} draft={images} disabled={locked} available={imageRoute.systemSupported} routeReady={imageRoute.ready} routeLoading={imageRoute.loading} machineId={text(data.machine_id)} controls={<>
          <label className="plan-mode"><input type="checkbox" checked={mode === Mode.Plan} disabled={locked} onChange={event => setMode(event.target.checked ? Mode.Plan : Mode.Execute)} />{copy("session.planMode")}</label>
          <button className="primary composer-submit" aria-label={copy("session.queueMessage_891d4e")} title={copy("session.queueMessage_891d4e")} aria-keyshortcuts={shortcuts.aria(ShortcutId.SessionSend)} disabled={!canSend}><svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M10 16V4m-5 5 5-5 5 5" /></svg></button>
        </>}>
        <textarea ref={composer} onKeyDown={event => { if (!skills.onKeyDown(event) && !event.nativeEvent.isComposing) shortcuts.onKeyDown(event); }} onSelect={skills.onSelect} onCompositionStart={skills.onCompositionStart} onCompositionEnd={skills.onCompositionEnd} {...skills.attributes} aria-keyshortcuts={shortcuts.aria(ShortcutId.SessionFocus, ShortcutId.SessionSend, ShortcutId.SessionNewline)} id={`prompt-${id}`} value={draft} onChange={event => skills.onChange(event.target.value,event.target.selectionStart)} disabled={locked} placeholder={copy("session.sendAFollowUpToThis_c9d723")} rows={1} />
        {skills.list}{skills.warning}
        {imageTextLimit ? <p role="alert">{copy("image-input.textLimit")}</p> : null}
        </ImageAttachmentInput>
        {send.uncertain ? <button className="composer-original-retry" type="button" disabled={send.busy} onClick={send.retry}>{copy("session.retryTheSameMessage_5656d9")}</button> : null}
      </form>
    </div></SessionActivityProvider>
    {active && tabs.tab.kind===SessionTabKind.File ? <div className="session-app-panel"><SessionFilePreview sessionId={id} repository={tabs.tab.repository} path={tabs.tab.path} close={closePanel}/></div> : null}
    {active && (tabs.tab.kind===SessionTabKind.Diff || tabs.tab.kind===SessionTabKind.Comparison) ? <div className="session-app-panel"><SessionDiff sessionId={id} worktree={data.workspace===Workspace.Worktree} close={closePanel} selected={tabs.tab.kind===SessionTabKind.Comparison?tabs.tab:undefined} openComparison={value=>tabs.store.open(id,{kind:SessionTabKind.Comparison,...value})}/></div>:null}
    {filesOpened ? <div hidden={!active||panel!==SessionPanel.Files} inert={!active||panel!==SessionPanel.Files} className="session-app-panel"><SessionFiles active={active&&panel===SessionPanel.Files} sessionId={id} close={closePanel} openFile={(repository,path)=>tabs.store.open(id,{kind:SessionTabKind.File,repository,path})}/></div>:null}
    {active && panel===SessionPanel.Diagnostics ? <div className="session-app-panel"><RequestDiagnostics sessionId={id} close={closePanel}/></div>:null}
    {session && browserOpened ? <div hidden={!active||panel!==SessionPanel.Browser} inert={!active||panel!==SessionPanel.Browser} className="session-app-panel"><SessionBrowser key={`${id}:${browserAccountId}`} session={session} accountId={browserAccountId} close={closePanel} active={active&&panel===SessionPanel.Browser} selectedPage={tabs.tab.kind===SessionTabKind.Page?tabs.tab:undefined} openPage={page=>tabs.store.open(id,{kind:SessionTabKind.Page,...page})}/></div>:null}
    {session && terminalOpened ? <div hidden={!active||![SessionTabKind.Terminal,SessionTabKind.Terminals].includes(tabs.tab.kind)} className="session-app-panel session-terminal-pane"><SessionTerminals session={session} close={closeTerminal} tabbed selectedId={tabs.tab.kind===SessionTabKind.Terminal?tabs.tab.id:""} openTerminal={terminalId=>tabs.store.open(id,{kind:SessionTabKind.Terminal,id:terminalId})} active={active&&[SessionTabKind.Terminal,SessionTabKind.Terminals].includes(tabs.tab.kind)}/></div>:null}
    {tabs.store.sidechats(id).map(tab=>tab.kind===SessionTabKind.Sidechat?<div key={tab.id} hidden={!active||tabs.selected!==sessionTabKey(tab)} inert={!active||tabs.selected!==sessionTabKey(tab)} className="session-sidechat-pane"><SidechatPane id={tab.id} active={active&&tabs.selected===sessionTabKey(tab)}/></div>:null)}
    </div>
    </div>
    <aside hidden={tabs.tab.kind===SessionTabKind.Sidechat} ref={information} id={`info-${id}`} className="session-information" aria-labelledby={`info-title-${id}`}>
      <header><h2 ref={infoHeading} tabIndex={-1} id={`info-title-${id}`}>{copy("session.sessionInformation")}</h2></header>
      <div className="session-information-body">
        <div ref={setInfoToolsTarget} tabIndex={-1} />
        {session ? <>
          <Disclosure open className="session-information-section" onToggle={event => setPROpen(event.currentTarget.open)}><DisclosureSummary>{copy("session.pullRequests")}</DisclosureSummary><SessionPullRequests key={id} session={session} visible={prOpen} /></Disclosure>
          <Disclosure open className="session-information-section"><DisclosureSummary>{copy("session.executionSettings")}</DisclosureSummary><ExecutionConfiguration resource={session} /></Disclosure>
          <Disclosure open className="session-information-section"><DisclosureSummary>{copy("session.context")}</DisclosureSummary><SessionContext key={id} session={session} /></Disclosure>
          <Disclosure open className="session-information-section" onToggle={event => setSubagentsOpen(event.currentTarget.open)}><DisclosureSummary>{copy("session.subagents")}</DisclosureSummary><Subagents key={id} sessionId={id} revision={session.revision.toString()} visible={subagentsOpen} /></Disclosure>
          <Disclosure open ref={budgetDetails} className="session-information-section" tabIndex={-1}><DisclosureSummary>{copy("session.usageAndBudget")}</DisclosureSummary><NativeUsage session={session} /><SessionBudget resource={session} changed={setAcknowledged} blocked={setBudgetBlocked} /></Disclosure>
          <SessionStorageAction source={session} />
        </> : null}
      </div>
    </aside>

    </div>
  </section></SessionActivityProvider>;
}

function SidechatPane({id,active}:{id:string;active:boolean}) { const[draft,setDraft]=useState("");const[bindings,setBindings]=useState<SkillTokenBinding[]>([]);return <SessionActivityProvider active={active}><SessionView id={id} active={active} embedded draft={draft} setDraft={(value,skills)=>{setDraft(value);if(skills)setBindings(skills);}} initialSkills={bindings} changeSkills={setBindings}/></SessionActivityProvider>; }
