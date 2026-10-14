import { LinkedPre } from "./external-link-text";
import {
  useProductLabels,
  productResourceName,
} from "./product-identity-labels";
import { createPortal } from "react-dom";
import { useSessionInboxRead } from "./session-inbox-read";
import { NativeDynamicTool } from "./native-dynamic-tool";
import { WaitingQueue } from "./waiting-queue";

import { FlatDisclosureScope } from "./disclosure";
import { useSessionNameEditor } from "./session-name-editor";
import {
  EntranceMotion,
  useActivationCount,
  useEntranceActivation,
} from "./entrance-motion";
import { SessionToolMenu } from "./session-tool-menu";
import { NativeAutoReview } from "./native-auto-review";
import { useSessionRevert } from "./session-revert";
import { isImageStartupRejectedInput } from "./startup-rejection";
import { SessionActivityProvider } from "./session-activity";
import { SessionTabBar } from "./session-tab-bar";
import {
  useSessionTabs,
  useSessionTabsStore,
  SessionTabKind,
  sessionTabKey,
} from "./session-tabs";
import {
  initialExecutionPending,
  initialSessionStartup,
  SessionProgressPhase,
  sessionProgress,
  progressMessages,
  progressResponseOwner,
  responseSuppressesProgress,
} from "./session-progress";
import { useStartupTimes } from "./session-startup-timing";
import {
  useStartupLeaseExpiry,
  useStartupPresence,
} from "./session-startup-presence";
import {
  startupOperations,
  startupWorkerCurrent,
} from "./session-startup-operations";
import {
  SessionProgressStatus,
  StartupObservedOperation,
  RetainedStartupTimes,
} from "./session-progress-status";
import { currentTurn, turnTimingConfirmed } from "./turn-timing";
import { ToolTurnTranscript } from "./tool-turn-transcript";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { NativeImageView } from "./native-image-view";
import { nativeSleepDuration } from "./native-sleep";
import { SessionHarness } from "./session-harness";
import { useSkillCompletion, type SkillTokenBinding } from "./skill-completion";
import {
  acknowledgeSessionSubmission,
  nativeSubmissionInput,
  submissionQueueReadable,
  SubmissionPhase,
  useSessionSubmissions,
} from "./session-submissions";
import { imageMime } from "./image-input";
import { NativeImageGeneration } from "./native-image-generation";
import {
  ImageAttachmentInput,
  RetainedImages,
  imageEntryHandlers,
} from "./image-attachments";
import { useImageDraft, useImageRoute } from "./image-drafts";
import { RunnerTaskRemediation } from "./session-runner-remediation";
import {
  sessionControlEligibility,
  useSessionControl,
} from "./session-control";
import {
  paginationIdentity,
  paginationRevision,
  pagesResident,
  ReadStage,
} from "./scroll-pagination";
import { useConversationDrafts } from "./conversation-drafts";
import {
  initialInteractionDraft,
  interactionRequestIdentity,
  type InboxInteractionDraft,
} from "./inbox-drafts";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import {
  executionStartupFailure,
  canRetryExecutionStartup,
  StartupRecoverySummary,
  ExecutionStartupDetails,
} from "./execution-startup";

import { useShortcuts } from "./shortcut-provider";
import {
  sessionShortcutBindings,
  bindingKeys,
  ShortcutExecution,
  ShortcutId,
  ShortcutInput,
} from "./shortcuts";
import { Surface } from "./surface";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { Subagents } from "./subagents";
import { NativeGrokTool } from "./native-grok-interactions";
import { NativeGrokText, NativeGrokUser } from "./native-grok";
import { SessionBrowser, type BrowserHandle } from "./session-browser";
import { SessionFiles, SessionFilePreview } from "./session-files";
import { RequestDiagnostics } from "./request-diagnostics";
import { SessionDiff } from "./session-diff";
import { NativeBuiltin, NativeWorkspaceEvent } from "./native-builtin";
import { NativeChanges, NativeRevision } from "./native-changes";
import { NativeTodo, NativeTodoProgress } from "./native-todo";
import { NativeUsage } from "./native-usage";
import { NativeRead } from "./native-read";
import { NativeShell } from "./native-shell";
import {
  NativeClaudeMessage,
  validNativeClaudeMessage,
} from "./native-claude-message";
import { NativeClaudeInterruption } from "./native-claude-interruption";
import { NativeContextCompaction } from "./native-context-compaction";
import { NativeClaudeProgress } from "./native-claude-progress";
import { NativeClaudeTool } from "./native-claude-tool";
import { NativeReasoning } from "./native-reasoning";
import { CodexReasoning, codexReasoningMessage } from "./codex-reasoning";
import { SessionContext } from "./session-context";
import { BudgetEvidence, SessionBudget } from "./session-budget";
import {
  ExecutionConfiguration,
  SavedExecutionChips,
} from "./execution-configuration";
import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  ConnectionState,
  EntityKind,
  ResourceService,
  SessionAction,
  SessionQuery,
  SystemQuery,
  SystemCapability,
  SyncKind,
  newRequestId,
  synchronizeResources,
  clientFailure,
  supportsResourceSchema,
  type ClientFailure,
  type Resource,
} from "@delinoio/delidev-api-client";
import {
  document as readDocument,
  encode,
  items,
  Mode,
  object,
  text,
  Workspace,
} from "./documents";
import {
  useRetainedMutation,
  useRetainedMutationNotifications,
  useRetainedMutationIntents,
} from "./mutation";
import { ServiceProblem, Failure, Problem, failureSummary } from "./ui";
import {
  SessionActions,
  SessionIcon,
  SessionIconKind,
  SessionNotice,
  SessionProblemNotice,
  sessionStreamFailureTitle,
  SessionNoticeSeverity,
} from "./session-presentation";
import "./session.css";
import {
  Interaction,
  PendingInteractionReceipt,
  advancesInteraction,
} from "./interactions";
import {
  InteractionPresentationStore,
  InteractionPresentation,
  type InteractionProjection,
  displayedInteraction,
  completeEmptyInteractionTray,
  missingQuestionReceipts as selectMissingQuestionReceipts,
  orderedPageEntries,
  PageEntryKind,
  unpagedQuestionReceipts,
} from "./interaction-presentation";
import { SessionTerminals } from "./session-terminals";
import {
  PendingSidechatPane,
  SessionCreationAction,
  SessionForkAction,
} from "./session-fork";
import { SidechatAuthoring } from "./sidechat-authoring";
import { SidechatFindings } from "./sidechat";
import {
  useSidechatQuestionRetry,
  SidechatRetryAction,
  sidechatAnswerFilter,
} from "./sidechat-retry";
import { SessionTools } from "./session-tools";
import { SessionStorageAction } from "./session-storage";
import { SessionLifetimeUsage } from "./session-lifetime-usage";
import { SessionPullRequests } from "./session-pull-requests";
import {
  PendingQueueInputs,
  QueuedInput,
  type QueuedInputDraft,
} from "./queue";
import { StartupRejection } from "./startup-rejection";

function useSessionStream(
  id: string,
  active: boolean,
  presentation: InteractionPresentationStore,
) {
  const transport = useTransport();
  const [resources, setResources] = useState<ReadonlyMap<string, Resource>>(
    new Map(),
  );
  const [removed, setRemoved] = useState<ReadonlySet<string>>(new Set());
  const [newMessageIds, setNewMessageIds] = useState<readonly string[]>([]);
  const [newQueueIds, setNewQueueIds] = useState<readonly string[]>([]);
  const [newInteractionIds, setNewInteractionIds] = useState<readonly string[]>(
    [],
  );
  const [state, setState] = useState(ConnectionState.Connecting);
  const [error, setError] = useState<ClientFailure>();
  const [generation, setGeneration] = useState(0);
  const [restart, setRestart] = useState(0);
  const readOwner = useMemo(() => ({}), [id, transport, restart, active]);
  const [snapshotOwner, setSnapshotOwner] = useState<object>();
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    const client = createClient(ResourceService, transport);
    const tombstones = new Set<string>();
    const known = new Set<string>();
    const run = async () => {
      for await (const change of synchronizeResources(
        client,
        { kind: EntityKind.SESSION, sessionId: id },
        {
          signal: controller.signal,
          watchKinds: [
            EntityKind.MESSAGE,
            EntityKind.QUEUE,
            EntityKind.INTERACTION,
          ],
          maxResources: 2000,
          maxDocumentBytes: 8 << 20,
        },
      )) {
        if (controller.signal.aborted) return;
        if (change.kind === SyncKind.Snapshot) {
          setSnapshotOwner(readOwner);
          known.clear();
          for (const row of change.resources) known.add(row.id);
          setResources(new Map(change.resources.map((r) => [r.id, r])));
          setRemoved(new Set());
          setNewMessageIds((current) => current.filter((id) => known.has(id)));
          setNewQueueIds((current) => current.filter((id) => known.has(id)));
          setNewInteractionIds((current) =>
            current.filter((id) => known.has(id)),
          );
          tombstones.clear();
          setGeneration((value) => value + 1);
        } else if (change.kind === SyncKind.Upsert) {
          if (!known.has(change.resource.id)) {
            if (change.resource.kind === EntityKind.MESSAGE)
              setNewMessageIds((current) =>
                current.length >= 2000
                  ? current
                  : [...current, change.resource.id],
              );
            if (change.resource.kind === EntityKind.QUEUE)
              setNewQueueIds((current) =>
                current.length >= 2000
                  ? current
                  : [...current, change.resource.id],
              );
            if (change.resource.kind === EntityKind.INTERACTION)
              setNewInteractionIds((current) =>
                current.length >= 2000
                  ? current
                  : [...current, change.resource.id],
              );
          }
          known.add(change.resource.id);
          setResources((current) =>
            new Map(current).set(change.resource.id, change.resource),
          );
        } else if (change.kind === SyncKind.Remove) {
          known.delete(change.id);
          // Only confirmed interaction removals reach the durable fence, and it
          // must exist before any following Snapshot clears the stream-scoped
          // tombstones below.
          if (change.entityKind === EntityKind.INTERACTION)
            presentation.remove(change.id);
          setNewMessageIds((current) =>
            current.filter((id) => id !== change.id),
          );
          setNewQueueIds((current) => current.filter((id) => id !== change.id));
          setNewInteractionIds((current) =>
            current.filter((id) => id !== change.id),
          );
          if (!tombstones.has(change.id) && tombstones.size >= 2000)
            throw new ConnectError(
              "Reopen this session to refresh its retained history.",
              Code.ResourceExhausted,
            );
          tombstones.add(change.id);
          setResources((current) => {
            const next = new Map(current);
            next.delete(change.id);
            return next;
          });
          setRemoved(new Set(tombstones));
        } else {
          setState(change.state);
          // Safe connection failures are presentation values, never raw errors.
          setError(change.failure);
        }
      }
    };
    void run().catch((reason) => {
      if (!controller.signal.aborted) {
        setError(clientFailure(reason));
        setState(ConnectionState.Failed);
      }
    });
    return () => controller.abort();
  }, [id, transport, restart, active, readOwner, presentation]);
  return {
    currentSnapshot: snapshotOwner === readOwner,
    resources,
    removed,
    newMessageIds,
    newQueueIds,
    newInteractionIds,
    state,
    error,
    generation,
    retry: () => setRestart((value) => value + 1),
  };
}

function currentRows(
  base: readonly Resource[],
  live: ReadonlyMap<string, Resource>,
  removed: ReadonlySet<string>,
  kind: EntityKind,
): Resource[] {
  return base
    .filter((row) => !removed.has(row.id))
    .map((row) => {
      const update = live.get(row.id);
      return update?.kind === kind && update.revision > row.revision
        ? update
        : row;
    });
}

function appendedRows(
  base: readonly Resource[],
  live: ReadonlyMap<string, Resource>,
  removed: ReadonlySet<string>,
  arrivals: readonly string[],
  sessionId: string,
  lastPage: boolean,
  kind: EntityKind,
): Resource[] {
  const result = currentRows(base, live, removed, kind);
  if (!lastPage) return result;
  const ids = new Set(result.map((row) => row.id));
  for (const id of arrivals) {
    const row = live.get(id);
    if (
      row?.kind === kind &&
      row.sessionId === sessionId &&
      !ids.has(id) &&
      !removed.has(id)
    ) {
      result.push(row);
      ids.add(id);
    }
  }
  return result;
}

export function messageRows(
  base: readonly Resource[],
  live: ReadonlyMap<string, Resource>,
  removed: ReadonlySet<string>,
  arrivals: readonly string[],
  sessionId: string,
  lastPage: boolean,
): Resource[] {
  return appendedRows(
    base,
    live,
    removed,
    arrivals,
    sessionId,
    lastPage,
    EntityKind.MESSAGE,
  );
}

/** Queue history remains retained; only authoritative waiting inputs are presented. */
export function isQueuedInput(row: Resource): boolean {
  return text(readDocument(row).delivery) === "queued";
}

export function queueRows(
  base: readonly Resource[],
  live: ReadonlyMap<string, Resource>,
  removed: ReadonlySet<string>,
  arrivals: readonly string[],
  sessionId: string,
  lastPage: boolean,
): Resource[] {
  return appendedRows(
    base,
    live,
    removed,
    arrivals,
    sessionId,
    lastPage,
    EntityKind.QUEUE,
  );
}

export function interactionRows(
  base: readonly Resource[],
  live: ReadonlyMap<string, Resource>,
  removed: ReadonlySet<string>,
  arrivals: readonly string[],
  sessionId: string,
  lastPage: boolean,
): Resource[] {
  return appendedRows(
    base,
    live,
    removed,
    arrivals,
    sessionId,
    lastPage,
    EntityKind.INTERACTION,
  );
}

// Completion suppression belongs only to recognized conversation presentation.
function ConversationStatus({ state }: { state: string }) {
  return !state || state === "complete" || state === "completed" ? null : (
    <header>
      <small>{statusLabel(state)}</small>
    </header>
  );
}

export const TranscriptItem = memo(function TranscriptItem({
  resource,
  active = true,
  actions,
  revertAction,
  contextRevision = 0,
}: {
  resource: Resource;
  active?: boolean;
  actions?: ReactNode;
  revertAction?: ReactNode;
  contextRevision?: number;
}) {
  useLocale();
  const data = readDocument(resource);
  if (Object.hasOwn(data, "dynamic_tool"))
    return (
      <NativeDynamicTool
        content={
          data.role === "tool" &&
          data.text === "" &&
          ![
            "tool",
            "function_output",
            "claude_tool",
            "grok_tool",
            "artifact",
            "progress",
            "input_id",
            "phase",
            "claude",
            "claude_progress",
            "claude_interruption",
            "grok_text",
            "grok_user",
          ].some((key) => Object.hasOwn(data, key))
            ? data.dynamic_tool
            : undefined
        }
      />
    );
  if (Object.hasOwn(data, "grok_tool")) return <NativeGrokTool data={data} />;
  if (Object.hasOwn(data, "grok_user")) return <NativeGrokUser data={data} />;
  if (data.grok_text != null) return <NativeGrokText data={data} />;
  if (data.claude_progress != null) return <NativeClaudeProgress data={data} />;
  if (data.claude_interruption != null)
    return <NativeClaudeInterruption data={data} />;
  if (data.claude_tool != null) {
    const valid =
      data.role === "tool" &&
      data.text === "" &&
      data.input_id == null &&
      data.phase == null &&
      data.tool == null &&
      data.artifact == null &&
      data.progress == null &&
      data.claude == null;
    return (
      <article
        className="message"
        aria-label={copy("session.toolMessage_adbec1")}
      >
        <header>
          <strong>{copy("session.tool_7c9bbe")}</strong>
          <small>{statusLabel(text(data.state))}</small>
        </header>
        <NativeClaudeTool
          content={valid ? data.claude_tool : undefined}
          state={text(data.state)}
          id={resource.id}
          native={text(data.native_id)}
          parent={text(data.native_parent_id)}
        />
      </article>
    );
  }
  if (data.claude != null) {
    const valid =
      data.role === "assistant" &&
      data.text === "" &&
      data.native_parent_id == null &&
      data.input_id == null &&
      data.phase == null &&
      data.tool == null &&
      data.artifact == null &&
      data.progress == null &&
      validNativeClaudeMessage(data.claude, text(data.state));
    return (
      <article
        className={valid ? "message message-claude-assistant" : "message"}
        aria-label={copy("session.assistantMessage_8352f5")}
      >
        {valid ? (
          <ConversationStatus state={text(data.state)} />
        ) : (
          <header>
            <strong>{copy("session.assistant_a39a7f")}</strong>
            <small>{statusLabel(text(data.state))}</small>
          </header>
        )}
        <NativeClaudeMessage
          content={valid ? data.claude : undefined}
          state={text(data.state)}
        />
      </article>
    );
  }
  const tool = object(data.tool);
  const toolStarted = object(tool.started);
  const toolCompleted = object(tool.completed);
  const sleepDuration = nativeSleepDuration(toolStarted, toolCompleted);
  const command = object(
    (tool.completed ? toolCompleted : toolStarted).command,
  );
  const artifact = object(data.artifact);
  const completed = object(artifact.completed);
  const started = object(artifact.started);
  const reasoning = codexReasoningMessage(data, text(data.state));
  if (reasoning)
    return (
      <article
        className="message message-codex-reasoning"
        aria-label={copy("native-reasoning.codexReasoning")}
      >
        <ConversationStatus state={text(data.state)} />
        {Number(data.context_revision ?? 0) < contextRevision ? (
          <small>{copy("session.previousContext")}</small>
        ) : null}
        <RetainedImages
          value={data.attachments}
          sessionId={resource.sessionId}
          active={active}
        />
        <CodexReasoning data={reasoning} />
      </article>
    );
  const progress = object(data.progress);
  const plan = object(progress.plan);
  const textRole =
    data.tool == null &&
    data.artifact == null &&
    data.progress == null &&
    [
      "grok_text",
      "claude",
      "claude_tool",
      "claude_progress",
      "claude_interruption",
    ].every((key) => !Object.hasOwn(data, key));
  const roleClass =
    textRole && data.role === "user"
      ? " message-user"
      : textRole && data.role === "assistant"
        ? " message-assistant"
        : "";
  const message = (
    <article
      className={`message${roleClass}`}
      aria-label={
        roleClass
          ? copy(
              data.role === "user"
                ? "session.userMessage"
                : "session.assistantMessage_8352f5",
            )
          : copy("session.message_e9ca2b", { v0: text(data.role) || "Agent" })
      }
    >
      {roleClass ? (
        <ConversationStatus state={text(data.state)} />
      ) : (
        <header>
          <strong>
            {text(data.role) || copy("session.extra.11b39c93777e")}
          </strong>
          <small>{statusLabel(text(data.state))}</small>
        </header>
      )}
      {text(data.text) ? (
        // Only plain user and assistant text is link-activatable; every other
        // role or evidence-bearing record keeps inert preformatted text.
        roleClass ? (
          <LinkedPre text={text(data.text)} />
        ) : (
          <pre>{text(data.text)}</pre>
        )
      ) : null}
      {Number(data.context_revision ?? 0) < contextRevision ? (
        <small>{copy("session.previousContext")}</small>
      ) : null}
      {data.role === "user" ? actions : null}
      <RetainedImages
        value={data.attachments}
        sessionId={resource.sessionId}
        active={active}
      />
      {toolStarted.kind === "image-view" ? (
        <NativeImageView tool={tool} state={text(data.state)} />
      ) : toolStarted.kind === "opencode-builtin" ? (
        <NativeBuiltin tool={tool} state={text(data.state)} />
      ) : toolStarted.kind === "opencode-todo" ? (
        <NativeTodo tool={tool} state={text(data.state)} />
      ) : toolStarted.kind === "opencode-read" ? (
        <NativeRead tool={tool} state={text(data.state)} />
      ) : toolStarted.kind === "opencode-shell" ? (
        <NativeShell tool={tool} state={text(data.state)} />
      ) : Object.keys(tool).length ? (
        <Disclosure>
          <DisclosureSummary>
            <LocalizedText
              id="session.tool_844a02"
              components={{
                s0: (
                  <>
                    {text(toolStarted.kind) ||
                      copy("session.extra.fa176576233d")}
                  </>
                ),
                s1: (
                  <>{text(toolCompleted.status) || text(toolStarted.status)}</>
                ),
              }}
            />
          </DisclosureSummary>
          {sleepDuration ? (
            <p>
              <LocalizedText
                id="session.sleepDuration"
                components={{ s0: <>{sleepDuration}</> }}
              />
            </p>
          ) : null}
          {text(command.command) ? <pre>{text(command.command)}</pre> : null}
          {text(command.cwd) ? (
            <p>
              <LocalizedText
                id="session.directory_369f13"
                components={{ s0: <>{text(command.cwd)}</> }}
              />
            </p>
          ) : null}
          {text(tool.output) ? <pre>{text(tool.output)}</pre> : null}
          {typeof command.aggregated_output === "string" ? (
            <Disclosure>
              <DisclosureSummary>
                {copy("session.nativeAggregateOutput_2d06e4")}
              </DisclosureSummary>
              <pre>{command.aggregated_output}</pre>
            </Disclosure>
          ) : null}
          {items((tool.completed ? toolCompleted : toolStarted).changes).map(
            (item, index) => (
              <pre key={index}>
                {text(object(item).path)}
                {"\n"}
                {text(object(item).diff)}
              </pre>
            ),
          )}
          {items(tool.patches).map((patch, index) => (
            <Disclosure key={index}>
              <DisclosureSummary>
                <LocalizedText
                  id="session.patchObservation_8b886e"
                  components={{ s0: <>{index + 1}</> }}
                />
              </DisclosureSummary>
              {items(object(patch).changes).map((item, part) => (
                <pre key={part}>
                  {text(object(item).path)}
                  {"\n"}
                  {text(object(item).diff)}
                </pre>
              ))}
            </Disclosure>
          ))}
          {items(tool.inputs).map((input, index) => (
            <pre key={index}>
              Tool input: {text(object(object(input).input).text)}
            </pre>
          ))}
        </Disclosure>
      ) : null}
      {started.kind === "image-generation" ? (
        <NativeImageGeneration
          artifact={artifact}
          sessionId={resource.sessionId}
          active={active}
          state={text(data.state)}
        />
      ) : started.kind === "opencode-revision" ? (
        <NativeRevision artifact={artifact} state={text(data.state)} />
      ) : started.kind === "reasoning-text" ? (
        <NativeReasoning artifact={artifact} state={text(data.state)} />
      ) : Object.keys(artifact).length ? (
        <Disclosure open>
          <DisclosureSummary>
            {text(started.kind) || copy("session.extra.a3e40dda2eb1")}
          </DisclosureSummary>
          {text(started.text) ? <pre>{text(started.text)}</pre> : null}
          {[...items(started.summary), ...items(started.content)].map(
            (part, index) => (
              <pre key={index}>{text(part)}</pre>
            ),
          )}
          {items(artifact.deltas).length ? (
            <Disclosure>
              <DisclosureSummary>
                {copy("session.streamedObservations_589dae")}
              </DisclosureSummary>
              {items(artifact.deltas).map((item, index) => {
                const delta = object(object(item).delta);
                return (
                  <pre key={index}>
                    {text(delta.kind)}
                    {typeof delta.index === "number"
                      ? ` ${delta.index}`
                      : ""}: {text(delta.text)}
                  </pre>
                );
              })}
            </Disclosure>
          ) : null}
          {artifact.completed ? (
            <section aria-label={copy("session.completedArtifact_de26fd")}>
              <h3>{copy("session.completedArtifact_de26fd")}</h3>
              <pre>{text(completed.text)}</pre>
              {[...items(completed.summary), ...items(completed.content)].map(
                (part, index) => (
                  <pre key={index}>{text(part)}</pre>
                ),
              )}
            </section>
          ) : null}
        </Disclosure>
      ) : null}
      {progress.kind === "codex-auto-review" ? (
        <NativeAutoReview progress={progress} state={text(data.state)} />
      ) : progress.kind === "native-compaction" ? (
        <NativeContextCompaction progress={progress} state={text(data.state)} />
      ) : progress.kind === "opencode-workspace" ? (
        <NativeWorkspaceEvent progress={progress} state={text(data.state)} />
      ) : progress.kind === "opencode-changes" ? (
        <NativeChanges
          progress={progress}
          state={text(data.state)}
          turn={text(data.native_turn_id)}
        />
      ) : progress.kind === "opencode-todo" ? (
        <NativeTodoProgress progress={progress} state={text(data.state)} />
      ) : Object.keys(progress).length ? (
        <Disclosure open>
          <DisclosureSummary>
            <LocalizedText
              id="session.progress_a4d877"
              components={{ s0: <>{text(progress.kind)}</> }}
            />
          </DisclosureSummary>
          <pre>{text(progress.diff) || text(plan.explanation)}</pre>
          <ol>
            {items(plan.steps).map((step, index) => (
              <li key={index}>
                {text(object(step).step)} · {text(object(step).status)}
              </li>
            ))}
          </ol>
        </Disclosure>
      ) : null}
    </article>
  );
  if (!textRole || data.role !== "user") return message;
  return (
    <div
      className="user-transcript-item"
      role="group"
      aria-label={copy("session.userTurn")}
    >
      {message}
      {revertAction ? (
        <div className="user-transcript-actions">{revertAction}</div>
      ) : null}
    </div>
  );
});

const submissionLabels = {
  [SubmissionPhase.Preparing]: "session.submissionPreparing",
  [SubmissionPhase.Sending]: "session.submissionSending",
  [SubmissionPhase.Queued]: "session.submissionQueued",
  [SubmissionPhase.Claimed]: "session.submissionClaimed",
  [SubmissionPhase.Accepted]: "session.submissionAccepted",
  [SubmissionPhase.Uncertain]: "session.submissionUncertain",
  [SubmissionPhase.Rejected]: "session.submissionRejected",
  [SubmissionPhase.Removed]: "session.submissionRemoved",
} as const;

const tabShortcutIds = [
  ShortcutId.SessionTab1,
  ShortcutId.SessionTab2,
  ShortcutId.SessionTab3,
  ShortcutId.SessionTab4,
  ShortcutId.SessionTab5,
  ShortcutId.SessionTab6,
  ShortcutId.SessionTab7,
  ShortcutId.SessionTab8,
  ShortcutId.SessionTab9,
] as const;
enum SessionPanel {
  Closed = "closed",
  Files = "files",
  Diff = "diff",
  Terminals = "terminals",
  Browser = "browser",
  Diagnostics = "diagnostics",
}
enum InfoTarget {
  Status = "status",
  Recovery = "recovery",
  Budget = "budget",
}

export function SubmissionStatus({ phase }: { phase: SubmissionPhase }) {
  useLocale();
  return (
    <header>
      <small role="status">{copy(submissionLabels[phase])}</small>
    </header>
  );
}

export function SessionView({
  id,
  draft,
  setDraft,
  draftGeneration,
  initialSkills,
  changeSkills,
  active = true,
  connectionReady = true,
  embedded = false,
  activateOnMount = !embedded,
}: {
  id: string;
  draft: string;
  setDraft: (
    value: string,
    bindings?: SkillTokenBinding[],
    userEdit?: boolean,
  ) => boolean | void;
  draftGeneration?: () => bigint;
  initialSkills?: SkillTokenBinding[];
  changeSkills?: (bindings: SkillTokenBinding[]) => void;
  active?: boolean;
  connectionReady?: boolean;
  embedded?: boolean;
  /**
   * Whether a mount that starts active is an explicit activation. Embedded
   * panes default to false so automatic publication never animates.
   */
  activateOnMount?: boolean;
  openRunnerSettings?: () => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const tabs = useSessionTabs(id);
  const conversationActive =
    active && (embedded || tabs.tab.kind === SessionTabKind.Conversation);
  // Session metadata owns account and native presentation authority even when
  // a resource pane hides the conversation. Only transcript reads pause there.
  // The presentation store outlives stream snapshots and receives authoritative
  // interaction removals from the stream before any following Snapshot.
  const [interactionPresentation] = useState(
    () => new InteractionPresentationStore(),
  );
  const live = useSessionStream(
    id,
    active &&
      ![SessionTabKind.Sidechat, SessionTabKind.PendingSidechat].includes(
        tabs.tab.kind,
      ),
    interactionPresentation,
  );
  const submissions = useSessionSubmissions();
  const [submissionError, setSubmissionError] = useState<unknown>();
  const [revealSubmission, setRevealSubmission] = useState<string>();
  const submissionTransport = useTransport();
  useEffect(() => {
    if (!conversationActive) return;
    const controller = new AbortController();
    const client = createClient(ResourceService, submissionTransport);
    const originals = submissions.store
      .snapshot()
      .filter((row) => row.sessionId === id && row.queueId && !row.native);
    // ListQueue excludes removed tombstones. Reinspect only retained original
    // IDs, serially, when this view or its authenticated transport is replaced.
    void (async () => {
      for (const original of originals) {
        if (controller.signal.aborted || !submissions.store.alive) return;
        try {
          const result = await client.getResource(
            { kind: EntityKind.QUEUE, id: original.queueId! },
            { signal: controller.signal },
          );
          if (controller.signal.aborted || !submissions.store.alive) return;
          const row = result.resource;
          if (
            !row ||
            row.id !== original.queueId ||
            row.sessionId !== id ||
            row.kind !== EntityKind.QUEUE ||
            row.revision < 1n ||
            !supportsResourceSchema(row) ||
            !submissionQueueReadable(row)
          )
            throw new ConnectError(
              "The original input status could not be verified.",
              Code.DataLoss,
            );
          submissions.store.observe(id, [row], true);
        } catch (error) {
          if (controller.signal.aborted || !submissions.store.alive) return;
          submissions.store.observationFailed(original.queueId!);
          console.warn("delidev.session_input.observation_failed", {
            classification: clientFailure(error).code,
          });
        }
      }
    })();
    return () => controller.abort();
  }, [id, conversationActive, submissionTransport, submissions.store]);
  const panel =
    tabs.tab.kind === SessionTabKind.Conversation
      ? SessionPanel.Closed
      : tabs.tab.kind === SessionTabKind.Files
        ? SessionPanel.Files
        : tabs.tab.kind === SessionTabKind.Diff
          ? SessionPanel.Diff
          : tabs.tab.kind === SessionTabKind.Diagnostics
            ? SessionPanel.Diagnostics
            : tabs.tab.kind === SessionTabKind.Page
              ? SessionPanel.Browser
              : SessionPanel.Closed;
  const conversationRegion = useRef<HTMLDivElement>(null);
  const upperContent = useRef<HTMLDivElement>(null);
  const [filesOpened, setFilesOpened] = useState(false);
  const [terminalOpened, setTerminalOpened] = useState(false);
  // An explicit Terminals gesture keeps the current pane. Its unsent intent
  // remembers that pane so a later explicit selection can cancel it.
  const [pendingTerminalOpen, setPendingTerminalOpen] = useState<{
    requestId: string;
    origin: string;
  }>();
  const [terminalOpenIntent, setTerminalOpenIntent] = useState<{
    requestId: string;
    revision: bigint;
    origin: string;
  }>();
  const [terminalStatusTarget, setTerminalStatusTarget] =
    useState<HTMLDivElement | null>(null);
  // The New browser page dialog is shared by the workspace + and Open tool.
  // Opening it creates nothing; the browser manager owns any accepted request.
  const [browserDialog, setBrowserDialog] = useState(false);
  const browserHandle = useRef<BrowserHandle | undefined>(undefined);
  useEffect(() => {
    if (!active) setBrowserDialog(false);
  }, [active]);
  const [recoveryLauncherTarget, setRecoveryLauncherTarget] =
    useState<HTMLDivElement | null>(null);
  const [infoToolsTarget, setInfoToolsTarget] = useState<HTMLDivElement | null>(
    null,
  );
  const terminalsButton = useRef<HTMLButtonElement>(null);
  const filesButton = useRef<HTMLButtonElement>(null);
  const diffButton = useRef<HTMLButtonElement>(null);
  const diagnosticsButton = useRef<HTMLButtonElement>(null);
  const browserButton = useRef<HTMLButtonElement>(null);
  const infoHeading = useRef<HTMLHeadingElement>(null);
  const infoEvidence = useRef<HTMLDivElement>(null);
  const budgetDetails = useRef<HTMLElement>(null);
  const information = useRef<HTMLElement>(null);
  const sessionHeading = useRef<HTMLHeadingElement>(null);
  const [startupInfoHidden, setStartupInfoHidden] = useState(false);
  // Explicit session activation (selection, or returning to a retained session)
  // owns one 120ms Y2 header entrance. Reads, streaming, edits and preference
  // changes never restart it, and nothing waits for it to finish. An embedded
  // Sidechat pane plays the same entrance on its own header for each rising
  // edge of its tab selection. Its mount counts only when the owner marks it
  // explicit (`activateOnMount`): a pane that mounts selected because its
  // pending tab was promoted, or one that is published in the background,
  // must not animate until a later explicit re-selection.
  const sessionActivations = useActivationCount(active, activateOnMount);
  const headerEntrance = useEntranceActivation(
    `${id}:${sessionActivations}`,
    active && sessionActivations > 0,
    EntranceMotion.Y2,
  );
  // Only an explicit Info reveal (Show details or a recovery/budget entry)
  // counts as opening; Info itself stays mounted and persistent.
  const [infoOpenings, setInfoOpenings] = useState(0);
  const [prEmpty, setPREmpty] = useState(false),
    [subagentsEmpty, setSubagentsEmpty] = useState(false);
  const [diagnosticsTarget, setDiagnosticsTarget] =
    useState<HTMLDivElement | null>(null);
  const editName = useSessionNameEditor();
  const [infoReveal, setInfoReveal] = useState<{ target: InfoTarget }>();
  useLayoutEffect(() => {
    if (!infoReveal) return;
    const target =
      infoReveal.target === InfoTarget.Budget
        ? budgetDetails.current
        : infoReveal.target === InfoTarget.Recovery
          ? infoToolsTarget
          : infoEvidence.current;
    if (target instanceof HTMLDetailsElement) target.open = true;
    else {
      target?.querySelectorAll("details").forEach((details) => {
        details.open = true;
      });
      const recovery =
        information.current?.querySelector<HTMLDetailsElement>(
          ".session-tools",
        );
      if (recovery) recovery.open = true;
    }
    const focusTarget =
      infoReveal.target === InfoTarget.Recovery
        ? target?.querySelector<HTMLButtonElement>(".notice button")
        : target;
    focusTarget?.focus({ preventScroll: true });
    focusTarget?.scrollIntoView?.({ block: "nearest" });
  }, [infoReveal]);
  const [budgetBlocked, setBudgetBlocked] = useState(false);
  // Startup Info hiding needs current successful budget evidence, not merely
  // the absence of a threshold-reached report. Evidence is scoped to the session.
  const [budgetEvidence, setBudgetEvidence] = useState<{
    id: string;
    status: BudgetEvidence;
  }>();
  const reportBudgetEvidence = useCallback(
    (sessionId: string, status: BudgetEvidence) =>
      setBudgetEvidence((previous) =>
        previous?.id === sessionId && previous.status === status
          ? previous
          : { id: sessionId, status },
      ),
    [],
  );
  const [runnerRemediationPending, setRunnerRemediationPending] =
    useState(false);
  const queueDrafts = useConversationDrafts<QueuedInputDraft>();
  const requestDrafts = useConversationDrafts<InboxInteractionDraft>();
  const [responseResources, setResponseResources] = useState<
    ReadonlyMap<string, Resource>
  >(new Map());
  const responseIntents = useRetainedMutationIntents("");
  const observeResponse = (result: Resource) => {
    if (
      result.sessionId !== id ||
      result.kind !== EntityKind.INTERACTION ||
      !supportsResourceSchema(result)
    )
      return;
    interactionPresentation.observe(result);
    setResponseResources((current) => {
      const prior = current.get(result.id);
      if (prior && prior.revision >= result.revision) return current;
      return new Map(current).set(result.id, result);
    });
  };
  const latestInteractions = new Map(live.resources);
  for (const [responseId, result] of responseResources) {
    const observed = latestInteractions.get(responseId);
    if (!observed || result.revision > observed.revision)
      latestInteractions.set(responseId, result);
  }
  for (const row of latestInteractions.values())
    if (row.kind === EntityKind.INTERACTION && row.sessionId === id)
      interactionPresentation.observe(row);
  const interactionRow = (row: Resource) => {
    const projection = interactionPresentation.observe(row);
    if (!displayedInteraction(projection, pendingQuestionKeys)) return null;
    const draft =
      requestDrafts.values.get(row.id) ?? initialInteractionDraft(row);
    return (
      <Interaction
        key={row.id}
        resource={row}
        activeTray
        projection={projection}
        observe={observeResponse}
        refresh={interactions.refresh}
        draft={draft}
        saveDraft={(editable) => {
          if (draft) requestDrafts.save(row.id, { ...draft, editable });
        }}
        clearDraft={() => requestDrafts.save(row.id)}
        submissionAllowed={
          !interactions.error &&
          (!draft || draft.requestIdentity === interactionRequestIdentity(row))
        }
      />
    );
  };
  const transcriptRoot = useRef<HTMLDivElement>(null),
    requestsRoot = useRef<HTMLDivElement>(null),
    queueRoot = useRef<HTMLDivElement>(null);
  const [requestsOpen, setRequestsOpen] = useState(false);
  const [mode, setMode] = useState(Mode.Execute);
  const messages = useConversationPages(
    EntityKind.MESSAGE,
    id,
    conversationActive && live.generation > 0,
  );
  const queue = useConversationPages(
    EntityKind.QUEUE,
    id,
    conversationActive && live.generation > 0,
  );
  const queueStatus = useQuery(
    SystemQuery.getStatus,
    {},
    { enabled: conversationActive },
  );
  const waitingSupported =
    queueStatus.data?.capabilities.includes(
      SystemCapability.WAITING_QUEUE_ORDER_V1,
    ) === true;
  const interactions = useConversationPages(
    EntityKind.INTERACTION,
    id,
    conversationActive && live.generation > 0,
    20,
    undefined,
    undefined,
    // Accepted list reads update compact interaction projections for every
    // reached page, including pages whose payload is about to be evicted.
    (rows) => {
      for (const row of rows)
        if (
          row.sessionId === id &&
          row.kind === EntityKind.INTERACTION &&
          supportsResourceSchema(row)
        )
          interactionPresentation.observe(row);
    },
  );
  const [acknowledged, setAcknowledged] = useState<Resource>();
  useRetainedMutationNotifications((key, _request, result) => {
    if (
      ["answer:", "opencode-answer:", "claude-answer:", "grok-answer:"].some(
        (prefix) => key.startsWith(prefix),
      )
    ) {
      const response = (result as { interaction?: Resource }).interaction;
      const mutation = (
        _request as { mutation?: { id?: string; expectedRevision?: bigint } }
      ).mutation;
      if (
        response &&
        response.id === mutation?.id &&
        response.revision > (mutation.expectedRevision ?? 0n)
      ) {
        observeResponse(response);
        // Retire the request's edit draft here, not in a presenter callback:
        // payload restoration can replace the compact receipt while the retry
        // is in flight, and the retained mutation then skips its unmounted
        // callback. The baseline is the submitted request revision, not the
        // mutable presentation revision, so an exact replay whose result equals
        // an already advanced projection still retires its own draft.
        if (
          advancesInteraction(response, {
            id: mutation.id,
            sessionId: id,
            kind: EntityKind.INTERACTION,
            revision: mutation.expectedRevision ?? 0n,
          }) &&
          supportsResourceSchema(response)
        )
          requestDrafts.save(response.id);
      }
      return;
    }
    if (key !== `session-name:${id}`) return;
    const renamed = (result as { change?: { session?: Resource } }).change
      ?.session;
    if (renamed?.id === id)
      setAcknowledged((previous) =>
        !previous || renamed.revision > previous.revision ? renamed : previous,
      );
  });
  const observed = live.resources.get(id);
  const original =
    acknowledged && (!observed || acknowledged.revision > observed.revision)
      ? acknowledged
      : observed;
  const {
    resource: session,
    control,
    action,
  } = useSessionControl(
    id,
    original,
    active &&
      ![SessionTabKind.Sidechat, SessionTabKind.PendingSidechat].includes(
        tabs.tab.kind,
      ),
  );
  useSessionInboxRead(
    id,
    conversationActive && !embedded,
    Boolean(
      live.currentSnapshot &&
      live.state === ConnectionState.Live &&
      !live.error &&
      observed?.id === id &&
      observed.kind === EntityKind.SESSION &&
      observed.revision > 0n &&
      supportsResourceSchema(observed) &&
      messages.loaded &&
      !messages.error &&
      !messages.isFetching &&
      messages.observed,
    ),
  );
  // A toolbar gesture can precede the initial session snapshot. Capture its
  // revision only after authenticated metadata arrives, while the same tool
  // remains selected; navigation cancels this unsent intent.
  useEffect(() => {
    if (!pendingTerminalOpen) return;
    if (!active || tabs.selected !== pendingTerminalOpen.origin) {
      setPendingTerminalOpen(undefined);
      return;
    }
    if (session) {
      setTerminalOpenIntent(
        (current) =>
          current ?? {
            requestId: pendingTerminalOpen.requestId,
            revision: session.revision,
            origin: pendingTerminalOpen.origin,
          },
      );
      setPendingTerminalOpen(undefined);
    }
  }, [pendingTerminalOpen, active, tabs.selected, session]);
  // Departure or a later explicit pane selection cancels an unsent intent. A
  // sent request keeps its retained controller and finishes on its own.
  useEffect(() => {
    if (
      terminalOpenIntent &&
      (!active || tabs.selected !== terminalOpenIntent.origin)
    )
      setTerminalOpenIntent(undefined);
  }, [terminalOpenIntent, active, tabs.selected]);
  const waitingRevision = `${session?.revision ?? 0n}:${live.generation}:${[
    ...live.resources.values(),
  ]
    .filter((row) => row.kind === EntityKind.QUEUE)
    .map((row) => `${row.id}:${row.revision}`)
    .join(",")}:${[...live.removed].join(",")}`;
  const retryQuestion = useSidechatQuestionRetry(session, conversationActive);
  const historyHeights = useRef(new Map<string, number>());
  const historyRoot = useRef<HTMLDivElement>(null);
  const data = readDocument(session);
  const hasStartupOperations = Boolean(data.startup_progress);
  const startupFailure = executionStartupFailure(data);
  const startupRetry = canRetryExecutionStartup(data);
  const inlineRecovery = Boolean(
    startupFailure ||
    Object.hasOwn(data, "startup_rejection") ||
    data.recovery === "required" ||
    ["failed", "canceled", "uncertain"].includes(
      text(object(data.preparation).state),
    ),
  );
  // A paused account switch selects the next account without rewriting the
  // preceding execution. Match the server's continuation selection immediately.
  const accountChanges = items(data.account_changes);
  const browserAccountId = accountChanges.length
    ? text(object(accountChanges.at(-1)).account_id)
    : text(object(data.current_execution).account_id) ||
      text(object(data.initial_execution).initial_account_id);
  const rows = useMemo(() => {
    // The stream records creation order. UUIDs from different Workers are not
    // an append sequence, even when each Worker generates UUID-v7 values.
    return messageRows(
      messages.data?.resources ?? [],
      live.resources,
      live.removed,
      live.newMessageIds,
      id,
      !!messages.data && !messages.data.nextPageToken,
    );
  }, [messages.data, live.resources, live.removed, live.newMessageIds, id]);
  useEffect(() => {
    if (conversationActive && live.generation > 1) {
      void messages.refresh();
      void queue.refresh();
      void interactions.refresh();
    }
  }, [live.generation, conversationActive]);
  const images = useImageDraft(`session:${id}`);
  const [imageTextLimit, setImageTextLimit] = useState(false);
  const imageRoute = useImageRoute(
    text(data.machine_id),
    text(data.agent_id),
    conversationActive,
    images.images.length > 0,
    object(data.fork).sidechat_parent_snapshot
      ? "sidechat"
      : text(object(object(data.initial_execution).configuration).harness) ||
          text(
            object(object(object(data.fork).snapshot).configuration).harness,
          ),
  );
  const send = useRetainedMutation(
    `enqueue:${id}`,
    SessionQuery.enqueueInput,
    (_result, request) => {
      images.controller.accepted(
        request.requestId,
        request.attachments.map((image) => image.id),
      );
      setDraft("");
      skills.clearAccepted();
      void queue.refresh();
    },
    acknowledgeSessionSubmission,
  );
  useEffect(() => {
    if (send.error && !send.uncertain && !send.busy)
      images.controller.operationId = undefined;
  }, [send.error, send.uncertain, send.busy, images.controller]);
  const locked =
    send.busy ||
    send.uncertain ||
    images.busy ||
    submissions.store.preparing(id);
  const composer = useRef<HTMLTextAreaElement>(null),
    composerComposing = useRef(false);
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
      if (workspace && workspace.clientHeight > 0)
        workspace.style.setProperty(
          "--session-composer-cap",
          `${workspace.clientHeight / 2}px`,
        );
    };
    const fit = () => {
      cap();
      input.style.height = "auto";
      const maximum = Number.parseFloat(getComputedStyle(input).maxHeight);
      input.style.height = `${Math.min(input.scrollHeight, Number.isFinite(maximum) ? maximum : 180)}px`;
    };
    const observer =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(() => {
            cap();
            if (width !== input.clientWidth) {
              width = input.clientWidth;
              fit();
            }
          });
    observer?.observe(input);
    if (workspace) observer?.observe(workspace);
    cap();
    window.addEventListener("resize", fit);
    return () => {
      observer?.disconnect();
      workspace?.style.removeProperty("--session-composer-cap");
      window.removeEventListener("resize", fit);
    };
  }, [session?.id]);
  const localDraftGeneration = useRef(0n);
  const skills = useSkillCompletion({
    value: draft,
    change: (value, bindings) => {
      if (new TextEncoder().encode(value).byteLength > 256 << 10) {
        setImageTextLimit(true);
        return false;
      }
      setImageTextLimit(false);
      const accepted = setDraft(value, bindings, true);
      if (accepted !== false) localDraftGeneration.current += 1n;
      return accepted;
    },
    textarea: composer,
    machineId: text(data.machine_id),
    agentId: text(data.agent_id),
    sessionId: id,
    initialBindings: initialSkills,
    bindingsChanged: changeSkills,
    retainTransportContext: Boolean(changeSkills),
    active: conversationActive,
    disabled: locked,
  });
  const revert = useSessionRevert({
    session,
    active: conversationActive,
    draft,
    draftGeneration: draftGeneration ?? (() => localDraftGeneration.current),
    composer,
    blocked: locked || images.images.length > 0,
    changed: setAcknowledged,
    restore: (prompt) => {
      if (images.images.length || images.busy) return false;
      if (setDraft(prompt, []) === false) return false;
      skills.clearAccepted();
      return true;
    },
  });
  const contextLocked =
    Boolean(data.compaction_job_id) || revert.pending || revert.uncertain;
  const canSend =
    !contextLocked &&
    !locked &&
    !skills.blocked &&
    new TextEncoder().encode(draft).byteLength <= 256 << 10 &&
    Boolean(draft.trim() || images.images.length) &&
    (!images.images.length || imageRoute.ready) &&
    text(data.archive) === "active";
  const enqueue = async () => {
    if (!canSend) return;
    const requestId = images.images.length
      ? (images.controller.operationId ?? newRequestId())
      : newRequestId();
    const original = encode({ prompt: draft, mode });
    const selections = skills.selections.map((selection) => ({ ...selection }));
    const attachmentCount = images.images.length,
      machine = imageRoute.machine;
    try {
      if (
        !submissions.store.freeze(requestId, id, draft, mode, attachmentCount)
      )
        return;
    } catch (error) {
      setSubmissionError(error);
      return;
    }
    setSubmissionError(undefined);
    setRevealSubmission(requestId);
    let attachments;
    try {
      attachments =
        attachmentCount && machine
          ? await images.controller.prepare(machine, requestId)
          : [];
    } catch {
      submissions.store.preparationFailed(requestId);
      return;
    }
    if (!submissions.store.alive) return;
    submissions.store.prepared(requestId, attachments);
    void send.send({
      requestId,
      sessionId: id,
      documentJson: original,
      skills: selections.length ? { selections } : undefined,
      attachments,
    });
  };
  const terminalsDisabled = Boolean(object(data.fork).sidechat_parent_snapshot);
  const shortcuts = useShortcuts([
    {
      id: ShortcutId.OpenFiles,
      scope: Surface.Sessions,
      label: "shortcuts.openFiles",
      bindings: sessionShortcutBindings[ShortcutId.OpenFiles],
      input: ShortcutInput.Allow,
      active: active && !embedded,
      // Reuse the original Files entry callback; it selects the singleton tab and never closes it.
      run: () => togglePanel(SessionPanel.Files),
    },
    {
      id: ShortcutId.OpenTerminals,
      scope: Surface.Sessions,
      label: "shortcuts.openTerminals",
      bindings: sessionShortcutBindings[ShortcutId.OpenTerminals],
      input: ShortcutInput.Allow,
      active: active && !embedded,
      enabled: !terminalsDisabled,
      unavailableReason: "shortcuts.terminalsUnavailable",
      run: () => togglePanel(SessionPanel.Terminals),
    },
    ...(!embedded
      ? Array.from({ length: 9 }, (_, index) => ({
          id: tabShortcutIds[index]!,
          scope: Surface.Sessions,
          label:
            `shortcuts.tab${index + 1}` as import("./localization").MessageKey,
          bindings: [{ key: String(index + 1), primary: true }],
          input: ShortcutInput.Allow,
          terminal: true,
          active,
          enabled: index < tabs.tabs.length,
          run: () => {
            document
              .getElementById(`session-tab-${id}-${index}`)
              ?.focus({ preventScroll: true });
            tabs.store.position(id, index + 1);
          },
        }))
      : []),
    // Fixed primary+W closes the selected closeable tool tab; Conversation stays pinned and the chord never reaches the window.
    ...(!embedded
      ? [
          {
            id: ShortcutId.SessionCloseTab,
            scope: Surface.Sessions,
            label: "shortcuts.closeTab" as import("./localization").MessageKey,
            bindings: [{ key: "w", primary: true }],
            input: ShortcutInput.Allow,
            terminal: true,
            active,
            enabled:
              tabs.tabs.findIndex(
                (tab) => sessionTabKey(tab) === tabs.selected,
              ) > 0,
            unavailableReason:
              "shortcuts.conversationTabPinned" as import("./localization").MessageKey,
            run: () => closeTab(tabs.selected),
          },
        ]
      : []),
    {
      id: ShortcutId.SessionFocus,
      scope: Surface.Sessions,
      label: "shortcuts.focusMessage",
      bindings: [{ key: "i", primary: true }],
      input: ShortcutInput.Allow,
      active: conversationActive,
      enabled: !locked,
      unavailableReason: "shortcuts.pending",
      run: () => composer.current?.focus(),
    },
    {
      id: ShortcutId.SessionSend,
      scope: Surface.Sessions,
      label: "shortcuts.queueMessage",
      bindings: [{ key: "Enter" }, { key: "Enter", primary: true }],
      target: composer,
      input: ShortcutInput.Target,
      active: conversationActive,
      enabled: canSend,
      unavailableReason: locked
        ? "shortcuts.pending"
        : text(data.archive) !== "active"
          ? "shortcuts.activeSessionRequired"
          : "shortcuts.messageRequired",
      run: () => composer.current?.form?.requestSubmit(),
    },
    {
      id: ShortcutId.SessionNewline,
      scope: Surface.Sessions,
      label: "shortcuts.newline",
      bindings: [{ key: "Enter", shift: true }],
      target: composer,
      input: ShortcutInput.Target,
      execution: ShortcutExecution.Native,
      active: conversationActive,
      enabled: !locked,
      unavailableReason: "shortcuts.pending",
    },
  ]);
  // Stream arrivals have exact identities even when their JSON sequence exceeds
  // JavaScript's safe-integer range. Append only arrivals on the final page.
  const pending = queueRows(
    queue.data?.inputs ?? [],
    live.resources,
    live.removed,
    live.newQueueIds,
    id,
    !!queue.data && !queue.data.nextPageToken,
  );
  useEffect(() => {
    submissions.store.observe(id, [
      ...(messages.data?.resources ?? []),
      ...pending,
      ...live.resources.values(),
    ]);
  }, [
    id,
    messages.data,
    queue.data,
    live.resources,
    submissions.store,
    submissions.rows,
  ]);
  useLayoutEffect(() => {
    if (!revealSubmission || !active) return;
    const target = transcriptRoot.current?.querySelector<HTMLElement>(
      `[data-submission="${revealSubmission}"]`,
    );
    target?.scrollIntoView?.({ block: "nearest" });
    setRevealSubmission(undefined);
  }, [revealSubmission, active]);
  // Stream-scoped removals plus the durable fence that survives Snapshots.
  const removedInteractions = new Set([
    ...live.removed,
    ...interactionPresentation.removed(),
  ]);
  const requests = interactionRows(
    interactions.data?.resources ?? [],
    latestInteractions,
    removedInteractions,
    live.newInteractionIds,
    id,
    !!interactions.data && !interactions.data.nextPageToken,
  );
  for (const row of requests) interactionPresentation.observe(row);
  const pendingQuestionKeys = new Set(
    responseIntents
      .filter((intent) =>
        [...interactionPresentation.entries()].some(
          ([, projection]) =>
            projection.question && projection.mutationKey === intent.key,
        ),
      )
      .map((intent) => intent.key),
  );
  const displayedRequests = requests.filter((row) =>
    displayedInteraction(
      interactionPresentation.observe(row),
      pendingQuestionKeys,
    ),
  );
  const missingQuestionReceipts = selectMissingQuestionReceipts(
    interactionPresentation.entries(),
    new Set(requests.map((row) => row.id)),
    removedInteractions,
    pendingQuestionKeys,
  );
  // Receipts render at their original reached page and row position, including
  // after the owning payload page is evicted. Only questions that no reached
  // page owns (for example new live arrivals) trail the final page.
  const receiptsById = new Map(missingQuestionReceipts);
  const trailingQuestionReceipts = unpagedQuestionReceipts(
    missingQuestionReceipts,
    new Set(interactions.pages.flatMap((page) => page.rows.map((r) => r.id))),
  );
  const questionReceipt = (
    requestId: string,
    projection: InteractionProjection,
  ) => (
    <PendingInteractionReceipt
      key={requestId}
      projection={projection}
      accepted={(result) => {
        if (result) observeResponse(result);
        // Draft retirement for a recovered answer lives in the session-level
        // accepted-result notification, which survives receipt replacement.
        void interactions.refresh();
      }}
    />
  );
  const requestsExpanded =
    requests.some((row) => {
      const projection = interactionPresentation.observe(row);
      return (
        displayedInteraction(projection, pendingQuestionKeys) &&
        projection.presentation === InteractionPresentation.Form &&
        readDocument(row).closure === "open"
      );
    }) || requestsOpen;
  // A read failure stays sticky until a later read settles successfully. An
  // explicit Retry or Reload clears the error as soon as it starts, so the
  // retained empty snapshot must not look like a healthy empty tray while
  // recovery is still pending (including a stalled read). Suspending the chain
  // (for example by switching sessions) cancels that read without settling it,
  // so only an advanced settled-read count releases the latch. A healthy
  // background Refresh never sets this flag.
  const requestsFailed = useRef(false);
  const requestsFailedAt = useRef<number | undefined>(undefined);
  const requestsFailure = useRef(interactions.error);
  if (interactions.error) {
    requestsFailed.current = true;
    requestsFailedAt.current = interactions.settledReads;
    requestsFailure.current = interactions.error;
  } else if (
    requestsFailed.current &&
    interactions.settledReads !== requestsFailedAt.current &&
    !interactions.isPending
  )
    requestsFailed.current = false;
  // Retry and Reload clear the error as soon as they start, which would unmount
  // the recovery control and its keyboard focus. While the latched read is in
  // flight, keep presenting the prior failure so the control stays mounted
  // (disabled by the pending read) until the read settles.
  const requestsRecovery: typeof interactions =
    requestsFailed.current &&
    !interactions.error &&
    interactions.loading &&
    requestsFailure.current
      ? { ...interactions, error: requestsFailure.current }
      : interactions;
  const confirmedEmptyRequests = completeEmptyInteractionTray({
    loaded: Boolean(interactions.data),
    loading: Boolean(
      interactions.loading &&
      interactions.loading !== ReadStage.Refresh &&
      interactions.loading !== ReadStage.Reload,
    ),
    failed:
      requestsFailed.current ||
      Boolean(interactions.error || live.error) ||
      live.state !== ConnectionState.Live,
    continuation: Boolean(interactions.nextPageToken),
    resident: pagesResident(interactions),
    displayed: displayedRequests.length,
    pending: new Set([
      ...pendingQuestionKeys,
      ...missingQuestionReceipts.map(
        ([, projection]) => projection.mutationKey,
      ),
    ]).size,
  });
  // Drop RPC payloads after their resident page expires or a list/live revision
  // catches up. Monotonic metadata survives without retaining answer documents.
  useEffect(() => {
    setResponseResources((current) => {
      const resident = new Map(
        (interactions.data?.resources ?? []).map((row) => [row.id, row]),
      );
      const next = new Map(
        [...current].filter(([key, value]) => {
          const row = resident.get(key),
            observed = live.resources.get(key);
          return (
            row &&
            row.revision < value.revision &&
            (!observed || observed.revision < value.revision)
          );
        }),
      );
      return next.size === current.size ? current : next;
    });
  }, [interactions.data, live.resources]);
  const requestFocus =
    typeof window === "undefined" ? null : window.document.activeElement;
  const retiringFocus =
    requestFocus instanceof HTMLElement &&
    Boolean(requestFocus.closest(".requests"))
      ? requestFocus
      : null;
  useLayoutEffect(() => {
    if (!retiringFocus || retiringFocus.isConnected) return;
    const focused = window.document.activeElement;
    if (
      focused &&
      focused !== window.document.body &&
      focused !== retiringFocus &&
      focused.isConnected
    )
      return;
    const next = requestsRoot.current?.querySelector<HTMLElement>(
      "input:not(:disabled), textarea:not(:disabled), select:not(:disabled), button:not(:disabled)",
    );
    (next ?? composer.current)?.focus({ preventScroll: true });
  });
  useEffect(() => {
    if (confirmedEmptyRequests) setRequestsOpen(false);
  }, [confirmedEmptyRequests]);
  const queued = pending.filter(isQueuedInput);
  const imageRejected = pending.filter((row) =>
    isImageStartupRejectedInput(row, session),
  );
  // Evicted payloads can contain waiting inputs. Keep their exact restoration
  // controls reachable; retained IDs alone cannot establish an empty queue.
  const completeQueuePayloads = pagesResident(queue);
  const queueFailed = useRef(false);
  if (queue.error) queueFailed.current = true;
  else if (!queue.isPending && !queue.loading) queueFailed.current = false;
  const confirmedEmptyQueue =
    !queueFailed.current &&
    live.state === ConnectionState.Live &&
    !live.error &&
    Boolean(queue.data) &&
    !queue.error &&
    !queue.isPending &&
    (!queue.loading ||
      queue.loading === ReadStage.Refresh ||
      queue.loading === ReadStage.Reload) &&
    !queue.nextPageToken &&
    completeQueuePayloads &&
    !queued.length;
  const presentedQueueIds = new Set(
    queue.payloadPages
      .flatMap((page) =>
        queueRows(page.payload, live.resources, live.removed, [], id, false)
          .filter(
            (row) =>
              isQueuedInput(row) || isImageStartupRejectedInput(row, session),
          )
          .map((row) => row.id),
      )
      .concat(
        !queue.nextPageToken
          ? [...queued, ...imageRejected]
              .filter((row) => !queue.rows.some((known) => known.id === row.id))
              .map((row) => row.id)
          : [],
      ),
  );
  const panelButtons = {
    [SessionPanel.Files]: filesButton,
    [SessionPanel.Diff]: diffButton,
    [SessionPanel.Terminals]: terminalsButton,
    [SessionPanel.Browser]: browserButton,
    [SessionPanel.Diagnostics]: diagnosticsButton,
  };
  const closeTab = (key: string) => {
    const index = tabs.tabs.findIndex((tab) => sessionTabKey(tab) === key);
    if (index <= 0) return;
    const page = tabs.tabs[index];
    if (page?.kind === SessionTabKind.Page) {
      // Page × closes the actual native page; its descriptor is removed only
      // after the original operation confirms absence and native cleanup.
      browserHandle.current?.closePage(page.profile, page.id);
      return;
    }
    const explorer =
      tabs.selected === key && tabs.tabs[index]?.kind === SessionTabKind.File
        ? tabs.tabs.findIndex((tab) => tab.kind === SessionTabKind.Files)
        : -1;
    const target =
      tabs.selected === key
        ? explorer >= 0
          ? explorer
          : index - 1
        : tabs.tabs.findIndex((tab) => sessionTabKey(tab) === tabs.selected);
    document
      .getElementById(`session-tab-${id}-${target}`)
      ?.focus({ preventScroll: true });
    tabs.store.close(id, key);
    if (explorer >= 0) tabs.store.select(id, SessionTabKind.Files);
  };
  const closePanel = () => closeTab(tabs.selected);
  const closeTerminal = closePanel;
  // Tab-bar DOM ids are positional, so focus waits for the tab bar to commit
  // and re-resolves the captured selection by key. A changed selection or a
  // removed target suppresses focus.
  const focusSelectedTabAfterCommit = (target: string) =>
    requestAnimationFrame(() => {
      const current = tabs.store.snapshot(id);
      if (current.selected !== target) return;
      const index = current.tabs.findIndex(
        (tab) => sessionTabKey(tab) === target,
      );
      if (index < 0) return;
      document
        .getElementById(`session-tab-${id}-${index}`)
        ?.focus({ preventScroll: true });
    });
  const hideEmptyTerminals = () => {
    // Final dismissal never closes a live terminal content tab, including one
    // that is absent from the current inventory; only tombstoned tabs close.
    if (tabs.store.liveTerminals(id).length) return;
    for (const tab of tabs.store.snapshot(id).tabs) {
      if (tab.kind === SessionTabKind.Terminal)
        tabs.store.close(id, sessionTabKey(tab));
    }
    focusSelectedTabAfterCommit(tabs.store.snapshot(id).selected);
  };
  const togglePanel = (next: Exclude<SessionPanel, SessionPanel.Closed>) => {
    if (next === SessionPanel.Files) setFilesOpened(true);
    if (next === SessionPanel.Terminals) {
      if (terminalsDisabled) return;
      setTerminalOpened(true);
      const origin = tabs.selected;
      if (session)
        setTerminalOpenIntent(
          (current) =>
            current ?? {
              requestId: newRequestId(),
              revision: session.revision,
              origin,
            },
        );
      else
        setPendingTerminalOpen(
          (current) => current ?? { requestId: newRequestId(), origin },
        );
      // No management tab exists: keep the current pane until the verified
      // original terminal is admitted, then its direct tab is selected.
      return;
    }
    if (next === SessionPanel.Browser) {
      // The Browser entry opens address entry, never a Browser workspace tab.
      setBrowserDialog(true);
      return;
    }
    const kinds = {
      [SessionPanel.Files]: SessionTabKind.Files,
      [SessionPanel.Diff]: SessionTabKind.Diff,
      [SessionPanel.Diagnostics]: SessionTabKind.Diagnostics,
    } as const;
    const target = { kind: kinds[next] };
    const targetKey = sessionTabKey(target);
    tabs.store.open(id, target);
    requestAnimationFrame(() => {
      const current = tabs.store.snapshot(id);
      if (current.selected !== targetKey) return;
      const index = current.tabs.findIndex(
        (tab) => sessionTabKey(tab) === targetKey,
      );
      document
        .getElementById(`session-tab-${id}-${index}`)
        ?.focus({ preventScroll: true });
    });
  };
  // Revealing Info preserves the selected resource and its original controller.
  const showInfo = (_opener: HTMLButtonElement, target = InfoTarget.Status) => {
    setInfoOpenings((count) => count + 1);
    setInfoReveal({ target });
  };
  const problem = object(data.problem);
  const recovering =
    text(data.recovery) !== "none" && Boolean(text(data.recovery));
  const connectionLabel =
    live.state === ConnectionState.Live
      ? copy("session.connected_229655")
      : live.state === ConnectionState.Reconnecting
        ? copy("session.connectionLostRetainedStateShown_8cc737")
        : live.state === ConnectionState.Failed
          ? copy("session.connectionRequiresAttention_160d4a")
          : copy("session.connecting_72021e");
  const projectedSubmissions = submissions.rows.filter(
    (row) =>
      row.sessionId === id &&
      !row.native &&
      !rows.some((message) => {
        return (
          Boolean(row.queueId) && row.queueId === nativeSubmissionInput(message)
        );
      }),
  );
  const tools = [
    {
      panel: SessionPanel.Diff,
      icon: SessionIconKind.Diff,
      label: copy("session.diff_7ecf46"),
    },
    {
      panel: SessionPanel.Files,
      icon: SessionIconKind.Files,
      label: copy("session.files_abc7e9"),
    },
    {
      panel: SessionPanel.Terminals,
      icon: SessionIconKind.Terminals,
      label: copy("session.terminals_7482c4"),
    },
    {
      panel: SessionPanel.Browser,
      icon: SessionIconKind.Browser,
      label: copy("session.browser_d31de1"),
    },
    {
      panel: SessionPanel.Diagnostics,
      icon: SessionIconKind.Diagnostics,
      label: copy("session.diagnostics_268f14"),
    },
  ] as const;
  const progressRevision = useRef({ id, revision: 0n });
  const progressResponse = useRef<{ owner?: string; seen: boolean }>({
    seen: false,
  });
  const progressRows = progressMessages(
    messages.rows,
    live.resources,
    live.removed,
    id,
  );
  const progressOwner = progressResponseOwner(session, id);
  useLayoutEffect(() => {
    if (
      !progressOwner ||
      (session &&
        progressRevision.current.id === id &&
        session.revision < progressRevision.current.revision)
    )
      return;
    if (progressResponse.current.owner !== progressOwner.key)
      progressResponse.current = { owner: progressOwner.key, seen: false };
    if (responseSuppressesProgress(progressRows, progressOwner))
      progressResponse.current.seen = true;
  });
  useLayoutEffect(() => {
    if (progressRevision.current.id !== id)
      progressRevision.current = { id, revision: 0n };
    if (
      session?.id === id &&
      session.revision > progressRevision.current.revision
    )
      progressRevision.current.revision = session.revision;
  }, [id, session]);
  const timing = currentTurn(session, id);
  // Only loss of synchronization of the owning Session resource unconfirms
  // timing. Definitive control rejections and unrelated queue/interaction read
  // failures stay visible through their own surfaces without freezing the clock.
  const timingConfirmed = turnTimingConfirmed({
    liveLive: live.state === ConnectionState.Live,
    liveFailed: Boolean(live.error),
    messagesFailed: Boolean(messages.error),
    controlUncertain: Boolean(control.uncertain),
    recoveryRequired: data.recovery === "required",
    unconfirmedResponses: Number(object(data.execution).unconfirmed_responses),
  });
  const observedProgress = sessionProgress({
    session,
    sessionId: id,
    current:
      (progressRevision.current.id !== id ||
        Boolean(
          session && session.revision >= progressRevision.current.revision,
        )) &&
      conversationActive &&
      live.state === ConnectionState.Live &&
      !live.error &&
      !queue.error &&
      !interactions.error &&
      !messages.error &&
      !messages.isPending,
    complete: Boolean(messages.data) && !messages.nextPageToken,
    blocked:
      Boolean(
        progressOwner &&
        progressResponse.current.owner === progressOwner.key &&
        progressResponse.current.seen,
      ) ||
      budgetBlocked ||
      runnerRemediationPending ||
      control.busy ||
      control.uncertain ||
      Boolean(control.error) ||
      requests.some((row) => readDocument(row).closure === "open") ||
      pending.some((row) => readDocument(row).delivery === "uncertain") ||
      projectedSubmissions.some(
        (row) =>
          row.observationUnavailable || row.phase === SubmissionPhase.Uncertain,
      ),
    messages: progressRows,
    queueCurrent:
      Boolean(queue.data) &&
      !queue.error &&
      !queue.isPending &&
      !queue.nextPageToken,
    queue: pending,
  });
  // Hide-boundary freshness only. A terminal token proves traversal, not
  // payload completeness: every retained page must still have its payload
  // resident (at most three stay), else evicted pages could hide a response,
  // follow-up or interaction. A refresh keeps its retained pages displayed
  // (the startup list stays), but retained pages are not current proof that no
  // response, follow-up or interaction arrived, so Info stays visible meanwhile.
  const startupTranscriptCurrent =
    Boolean(messages.data) &&
    !messages.error &&
    !messages.isPending &&
    !messages.isFetching &&
    !messages.nextPageToken &&
    pagesResident(messages);
  const startupQueueCurrent =
    Boolean(queue.data) &&
    !queue.error &&
    !queue.isPending &&
    !queue.isFetching &&
    !queue.nextPageToken &&
    pagesResident(queue);
  const startupInteractionsCurrent =
    Boolean(interactions.data) &&
    !interactions.error &&
    !interactions.isPending &&
    !interactions.isFetching &&
    !interactions.nextPageToken &&
    pagesResident(interactions);
  const startupBudgetCurrent =
    budgetEvidence?.id === id && budgetEvidence.status === BudgetEvidence.Ready;
  // A projected later follow-up (not the original Queued/Claimed/Accepted
  // input) is local evidence that startup is no longer pristine.
  const startupLocalFollowUp = projectedSubmissions.some((row) =>
    [
      SubmissionPhase.Preparing,
      SubmissionPhase.Sending,
      SubmissionPhase.Rejected,
      SubmissionPhase.Uncertain,
    ].includes(row.phase),
  );
  // Sidechat children keep their own Info in every initial execution phase.
  const sidechatChild =
    embedded || Boolean(object(data.fork).sidechat_parent_snapshot);
  // Retained startup metadata is historical. Presence is consumed only while
  // the original progress projection still has a current startup to display.
  const observeStartupOwner =
    conversationActive &&
    hasStartupOperations &&
    Boolean(observedProgress) &&
    Boolean(data.machine_id);
  const startupScopes = object(data.startup_progress);
  const startupOwner = JSON.stringify({
    session: id,
    execution: object(data.current_execution ?? data.initial_execution).id,
    scopes: [startupScopes.workspace, startupScopes.native].map((value) => {
      const scope = object(value);
      return [
        scope.job_id,
        scope.execution_id,
        scope.machine_id,
        scope.instance_id,
        scope.device_id,
        scope.server_epoch,
        scope.assignment_revision,
      ];
    }),
  });
  const startupMachine = useStartupPresence(
    text(data.machine_id),
    startupOwner,
    observeStartupOwner,
  );
  const startupTransport = useTransport();
  // A settled successful pair stays the retained original presentation while a
  // healthy reread is in flight (previous data is kept by the query). Failed,
  // missing, pending-first or other-transport reads never qualify.
  const startupPairRetained =
    observeStartupOwner &&
    Boolean(startupMachine.data) &&
    startupMachine.data?.transport === startupTransport &&
    !startupMachine.error &&
    !startupMachine.isPending;
  // Only a complete settled pair may animate or advance startup clocks; a pending
  // reread suspends the animation without refreshing the original lease.
  const startupPairAnimating =
    startupPairRetained && !startupMachine.isFetching;
  useStartupLeaseExpiry(session, startupMachine.data, startupPairRetained);
  const startupTimes = useStartupTimes(
    session,
    startupMachine.data,
    startupPairAnimating,
    startupOwner,
    startupPairRetained,
  );
  // Presentation currency is measured from the original read start and is
  // independent of isFetching so routine rereads cause no startup-list flicker.
  const startupOwnerCurrent =
    !hasStartupOperations ||
    startupWorkerCurrent(
      session,
      startupMachine.data?.resource,
      startupPairRetained,
      startupMachine.data?.observedAt,
      startupMachine.data
        ? performance.now() - startupMachine.data.startedAt
        : NaN,
    );
  const progress = startupOwnerCurrent ? observedProgress : undefined;
  const hideStartupInfo =
    active &&
    conversationActive &&
    !budgetBlocked &&
    startupBudgetCurrent &&
    !sidechatChild &&
    !startupLocalFollowUp &&
    startupTranscriptCurrent &&
    startupQueueCurrent &&
    startupInteractionsCurrent &&
    initialSessionStartup(session, progress, startupQueueCurrent, pending);
  useLayoutEffect(() => {
    // Commit visibility only after moving focus out of the still-visible Info.
    // Keep the original controllers active; presentation must not recreate reads.
    if (
      hideStartupInfo &&
      information.current?.contains(document.activeElement)
    ) {
      const input = composer.current;
      if (
        input?.isConnected &&
        !input.disabled &&
        !input.closest("[hidden], [inert]")
      )
        input.focus({ preventScroll: true });
      else sessionHeading.current?.focus({ preventScroll: true });
    }
    setStartupInfoHidden(hideStartupInfo);
  }, [hideStartupInfo]);
  const infoHidden =
    [SessionTabKind.Sidechat, SessionTabKind.PendingSidechat].includes(
      tabs.tab.kind,
    ) ||
    (hideStartupInfo && startupInfoHidden);
  const infoEntrance = useEntranceActivation(
    `${id}:info:${infoOpenings}`,
    infoOpenings > 0 && !infoHidden,
    EntranceMotion.X4,
  );
  return (
    <SessionActivityProvider
      active={
        active &&
        ![SessionTabKind.Sidechat, SessionTabKind.PendingSidechat].includes(
          tabs.tab.kind,
        )
      }
    >
      <section
        className="session-workspace session-tabbed"
        aria-label={copy("session.currentSession_a32789")}
        onKeyDown={(event) => {
          if (
            event.key === "Escape" &&
            event.target instanceof Node &&
            upperContent.current?.contains(event.target) &&
            !(
              event.target instanceof Element &&
              event.target.closest("[data-shortcuts=passthrough]")
            ) &&
            tabs.tab.kind !== SessionTabKind.Conversation &&
            // Escape never closes a native browser page: Page × is the one
            // explicit resource closure.
            tabs.tab.kind !== SessionTabKind.Page &&
            !(
              event.target instanceof Element &&
              event.target.closest("dialog[open]")
            )
          ) {
            event.stopPropagation();
            if (
              event.target instanceof Element &&
              event.target.closest(".terminal-dock")
            )
              closeTerminal();
            else if (panel !== SessionPanel.Closed) closePanel();
            else closeTerminal();
          }
        }}
      >
        <header className="session-header">
          <div className="session-header-entrance" {...headerEntrance}>
            <div className="session-heading">
              <SessionHarness resource={session}>
                <div className="session-heading-line">
                  <h2
                    ref={sessionHeading}
                    tabIndex={-1}
                    onDoubleClick={(event) => {
                      if (session) editName?.(session.id, event.currentTarget);
                    }}
                  >
                    {productResourceName(productLabel, session)}
                  </h2>
                  <p
                    className={`connection${live.state === ConnectionState.Live ? " is-live" : ""}`}
                    role="status"
                  >
                    {connectionLabel}
                  </p>
                </div>
              </SessionHarness>
            </div>
            <div
              className="session-controls"
              hidden={
                !embedded &&
                [
                  SessionTabKind.Sidechat,
                  SessionTabKind.PendingSidechat,
                ].includes(tabs.tab.kind)
              }
              inert={
                !embedded &&
                [
                  SessionTabKind.Sidechat,
                  SessionTabKind.PendingSidechat,
                ].includes(tabs.tab.kind)
              }
            >
              <button
                type="button"
                disabled={!session || control.busy || control.uncertain}
                onClick={() => action(SessionAction.STOP)}
              >
                {copy("session.stop_cae7d5")}
              </button>
              <button
                type="button"
                disabled={
                  !session ||
                  control.busy ||
                  control.uncertain ||
                  runnerRemediationPending ||
                  !sessionControlEligibility(session, budgetBlocked).resume
                }
                onClick={() => action(SessionAction.RESUME)}
              >
                {startupRetry
                  ? copy("session.startupRetry")
                  : copy("session.resume_d640c7")}
              </button>
              <SessionActions>
                {session ? (
                  <SessionForkAction
                    source={session}
                    disabled={control.busy || control.uncertain}
                  />
                ) : null}
                <button
                  type="button"
                  disabled={!session || control.busy || control.uncertain}
                  onClick={() =>
                    action(
                      text(data.archive) === "archived"
                        ? SessionAction.RESTORE
                        : SessionAction.ARCHIVE,
                    )
                  }
                >
                  {text(data.archive) === "archived"
                    ? copy("session.restore_a76e13")
                    : copy("session.archive_66f480")}
                </button>
              </SessionActions>
            </div>
          </div>
        </header>
        {!embedded ? (
          <div className="session-navigation">
            <SessionTabBar
              id={id}
              tabs={tabs.tabs}
              selected={tabs.selected}
              select={(key) => tabs.store.select(id, key)}
              close={closeTab}
              closeAria={shortcuts.aria(ShortcutId.SessionCloseTab)}
            />
            <SessionToolMenu
              key={id}
              active={active}
              ready={connectionReady}
              recoveryFocus={() => sessionHeading.current}
            >
              {tools.map((tool) => (
                <button
                  role="menuitem"
                  key={tool.panel}
                  type="button"
                  ref={panelButtons[tool.panel]}
                  disabled={
                    tool.panel === SessionPanel.Terminals && terminalsDisabled
                  }
                  aria-keyshortcuts={
                    tool.panel === SessionPanel.Files
                      ? shortcuts.aria(ShortcutId.OpenFiles) || undefined
                      : tool.panel === SessionPanel.Terminals
                        ? shortcuts.aria(ShortcutId.OpenTerminals)
                        : undefined
                  }
                  onClick={() => togglePanel(tool.panel)}
                >
                  <SessionIcon kind={tool.icon} />
                  <span className="session-tool-label">{tool.label}</span>
                  {tool.panel === SessionPanel.Files &&
                  shortcuts.bindings(ShortcutId.OpenFiles).length ? (
                    <kbd className="session-tool-shortcut" aria-hidden="true">
                      {shortcuts
                        .bindings(ShortcutId.OpenFiles)
                        .map((binding) =>
                          bindingKeys(binding, shortcuts.platform).join(" + "),
                        )
                        .join(` ${copy("shortcuts.or")} `)}
                    </kbd>
                  ) : null}
                </button>
              ))}
              {session &&
              tabs.tab.kind !== SessionTabKind.Sidechat &&
              tabs.tab.kind !== SessionTabKind.PendingSidechat ? (
                <SessionForkAction
                  source={session}
                  action={SessionCreationAction.Sidechat}
                  disabled={control.busy || control.uncertain}
                />
              ) : null}
            </SessionToolMenu>
          </div>
        ) : null}
        {!embedded ? (
          <div
            ref={setTerminalStatusTarget}
            className="session-terminal-status"
          />
        ) : null}
        <div className="session-content">
          <div
            id={`session-pane-${id}`}
            role={embedded ? undefined : "tabpanel"}
            aria-labelledby={
              embedded
                ? undefined
                : `session-tab-${id}-${tabs.tabs.findIndex((tab) => sessionTabKey(tab) === tabs.selected)}`
            }
            ref={upperContent}
            className="session-upper-content"
          >
            <div
              ref={conversationRegion}
              className="session-conversation-region"
            >
              <SessionActivityProvider active={conversationActive}>
                <div
                  className="session-body"
                  hidden={!conversationActive}
                  inert={!conversationActive}
                >
                  {session ? (
                    <SessionActivityProvider
                      active={
                        active &&
                        ![
                          SessionTabKind.Sidechat,
                          SessionTabKind.PendingSidechat,
                        ].includes(tabs.tab.kind)
                      }
                    >
                      <SessionTools
                        resource={session}
                        changed={setAcknowledged}
                        initiallyOpen
                        diagnosticsTarget={diagnosticsTarget}
                        target={infoToolsTarget}
                        launcherTarget={
                          inlineRecovery && conversationActive
                            ? recoveryLauncherTarget
                            : undefined
                        }
                        openRecovery={(opener) =>
                          showInfo(opener, InfoTarget.Recovery)
                        }
                      >
                        <div
                          className="session-information-evidence"
                          ref={infoEvidence}
                          tabIndex={-1}
                        >
                          <Failure failure={live.error} />
                          {live.state === ConnectionState.Failed ? (
                            <button onClick={live.retry}>
                              {copy("session.refreshConnection_73791f")}
                            </button>
                          ) : null}
                          {recovering ? (
                            <p className="notice">
                              <LocalizedText
                                id="session.recoveryExecutionRemainsUnderServerControl_d80aa1"
                                components={{
                                  s0: <>{statusLabel(text(data.recovery))}</>,
                                }}
                              />
                            </p>
                          ) : null}
                          {text(problem.message) ? (
                            <ServiceProblem
                              code={
                                text(problem.code) || text(problem.problem_code)
                              }
                            >
                              <p>
                                {text(problem.message)} {text(problem.guidance)}
                              </p>
                            </ServiceProblem>
                          ) : null}
                          <Problem error={control.error} />
                          <Problem error={send.error} />
                        </div>
                      </SessionTools>
                    </SessionActivityProvider>
                  ) : null}
                  <div
                    ref={transcriptRoot}
                    className="transcript"
                    aria-label={copy("session.conversation_ccca18")}
                  >
                    <Failure failure={messages.error?.failure} />
                    {messages.error && messages.data ? (
                      <p className="notice">
                        {copy("session.retainedConversation")}
                      </p>
                    ) : null}
                    {messages.isPending ? (
                      <p role="status">
                        {copy("session.loadingConversation_5eb1e4")}
                      </p>
                    ) : rows.length ||
                      messages.rows.length ||
                      timing ? null : messages.error ? (
                      <p>{copy("session.conversationUnavailable")}</p>
                    ) : progress ? null : projectedSubmissions.length ? null : (
                      <div className="session-empty">
                        <SessionIcon kind={SessionIconKind.Conversation} />
                        <h3>{copy("session.emptyConversation")}</h3>
                        <p>
                          {copy(
                            "session.theConversationWillAppearHereAfter_24857a",
                          )}
                        </p>
                      </div>
                    )}
                    {rows.length || messages.rows.length || timing ? (
                      <ToolTurnTranscript
                        key={`tools:${id}`}
                        sessionId={id}
                        active={conversationActive}
                        query={messages}
                        live={live.resources}
                        removed={live.removed}
                        arrivals={live.newMessageIds}
                        root={transcriptRoot}
                        include={sidechatAnswerFilter(session)}
                        current={timing}
                        confirmed={timingConfirmed}
                        render={(row) => (
                          <TranscriptItem
                            key={row.id}
                            resource={row}
                            active={conversationActive}
                            contextRevision={Number(data.context_revision ?? 0)}
                            revertAction={revert.action(row)}
                            actions={
                              <SidechatRetryAction
                                controller={retryQuestion}
                                inputId={text(readDocument(row).input_id)}
                              />
                            }
                          />
                        )}
                      />
                    ) : null}
                    {observeStartupOwner &&
                    !startupOwnerCurrent &&
                    !startupMachine.isPending &&
                    !inlineRecovery &&
                    !budgetBlocked &&
                    data.archive === "active" &&
                    ["not-started", "running"].includes(text(data.outcome)) ? (
                      <p role="status">
                        {copy("session.connectionRequiresAttention_160d4a")}
                      </p>
                    ) : null}
                    {!progress &&
                    hasStartupOperations &&
                    data.archive === "active" &&
                    ["not-started", "running"].includes(text(data.outcome)) &&
                    !projectedSubmissions.length &&
                    !rows.length &&
                    !messages.rows.length &&
                    (startupTimes.workspace?.seconds !== undefined ||
                      startupTimes.agent?.seconds !== undefined) ? (
                      <RetainedStartupTimes timing={startupTimes} />
                    ) : null}
                    {progress ? (
                      <SessionProgressStatus
                        phase={progress}
                        operations={startupOperations(session, progress)}
                        timing={startupTimes}
                        animating={
                          !hasStartupOperations || startupPairAnimating
                        }
                        compact={Boolean(
                          rows.length ||
                          messages.rows.length ||
                          projectedSubmissions.length,
                        )}
                      />
                    ) : null}
                    {items(data.sidechat_retries).length ? (
                      <Disclosure className="sidechat-answer-history">
                        <DisclosureSummary>
                          {copy("sidechat.retry.history")}
                        </DisclosureSummary>
                        <p>{copy("sidechat.retry.retainedHistory")}</p>
                        <div
                          className="sidechat-history-content"
                          ref={historyRoot}
                        >
                          <ToolTurnTranscript
                            sessionId={id}
                            active={conversationActive}
                            query={{
                              ...messages,
                              pages: messages.pages.map((page) => ({
                                ...page,
                                height: historyHeights.current.get(page.token),
                              })),
                              measure: (token, height) => {
                                historyHeights.current.set(token, height);
                              },
                            }}
                            live={live.resources}
                            removed={live.removed}
                            arrivals={live.newMessageIds}
                            root={historyRoot}
                            include={sidechatAnswerFilter(session, true)}
                            render={(row) => (
                              <TranscriptItem
                                resource={row}
                                active={conversationActive}
                              />
                            )}
                          />
                        </div>
                      </Disclosure>
                    ) : null}
                    <ScrollContinuation
                      query={messages}
                      root={transcriptRoot}
                      active={conversationActive && live.generation > 0}
                      label={copy("session.conversationPages_72b1b9")}
                    />
                    {retryQuestion.sidechat && retryQuestion.unsupported ? (
                      <p role="status">{copy("sidechat.retry.unsupported")}</p>
                    ) : null}
                    {session ? (
                      <SidechatFindings
                        key={id}
                        session={session}
                        messages={rows}
                      />
                    ) : null}
                    {projectedSubmissions.map((row) => (
                      <article
                        key={row.requestId}
                        data-submission={row.requestId}
                        className="message message-user"
                        aria-label={copy("session.submittedUserMessage")}
                      >
                        <SubmissionStatus
                          phase={
                            row.observationUnavailable
                              ? SubmissionPhase.Uncertain
                              : row.phase
                          }
                        />
                        {row.prompt ? <pre>{row.prompt}</pre> : null}
                        {row.attachments.length ? (
                          <RetainedImages
                            value={row.attachments.map((image) => ({
                              id: image.id,
                              machine_id: image.machineId,
                              media_type: imageMime[image.mediaType],
                              byte_length: Number(image.byteLength),
                              sha256: image.sha256,
                            }))}
                            sessionId={id}
                            active={conversationActive}
                          />
                        ) : row.attachmentCount ? (
                          <p>
                            {copy("session.submittedImages", {
                              count: row.attachmentCount,
                            })}
                          </p>
                        ) : null}
                      </article>
                    ))}
                  </div>
                  <div className="session-input-tray">
                    {!confirmedEmptyRequests ? (
                      <Disclosure
                        className="requests"
                        open={requestsExpanded}
                        onToggle={(event) =>
                          setRequestsOpen(event.currentTarget.open)
                        }
                      >
                        <DisclosureSummary>
                          {interactions.isPending ? (
                            copy("session.loadingRequests")
                          ) : (
                            <LocalizedText
                              id="session.agentRequestsOnThisPage_5e8644"
                              components={{
                                s0: (
                                  <>
                                    {displayedRequests.length +
                                      missingQuestionReceipts.length}
                                  </>
                                ),
                              }}
                            />
                          )}
                        </DisclosureSummary>
                        <div
                          ref={requestsRoot}
                          className="session-tray-content"
                        >
                          <Failure failure={requestsRecovery.error?.failure} />
                          <ScrollPayloadWindow
                            identity={paginationIdentity}
                            revision={paginationRevision}
                            query={interactions}
                            root={requestsRoot}
                            active={conversationActive && requestsExpanded}
                            projected={(rows, payload) =>
                              orderedPageEntries(
                                rows.map((row) => row.id),
                                payload
                                  ? interactionRows(
                                      payload,
                                      latestInteractions,
                                      removedInteractions,
                                      [],
                                      id,
                                      false,
                                    )
                                  : [],
                                receiptsById,
                              ).map((entry) =>
                                entry.kind === PageEntryKind.Receipt
                                  ? questionReceipt(entry.id, entry.value)
                                  : interactionRow(entry.value),
                              )
                            }
                          />
                          {!interactions.nextPageToken
                            ? requests
                                .filter(
                                  (row) =>
                                    !interactions.rows.some(
                                      (known) => known.id === row.id,
                                    ),
                                )
                                .map(interactionRow)
                            : null}
                          {trailingQuestionReceipts.map(
                            ([requestId, projection]) =>
                              questionReceipt(requestId, projection),
                          )}
                          <ScrollContinuation
                            query={requestsRecovery}
                            root={requestsRoot}
                            active={conversationActive && requestsExpanded}
                            label={copy("session.requestPages_d06a30")}
                          />
                        </div>
                      </Disclosure>
                    ) : null}
                    <PendingQueueInputs
                      sessionId={id}
                      presentInputIds={presentedQueueIds}
                      refresh={queue.refresh}
                    />
                    {waitingSupported ? (
                      <WaitingQueue
                        sessionId={id}
                        session={session}
                        active={conversationActive}
                        revision={waitingRevision}
                        drafts={queueDrafts.values}
                        saveDraft={queueDrafts.save}
                        readOnly={
                          live.state !== ConnectionState.Live ||
                          Boolean(live.error)
                        }
                        refreshHistory={queue.refresh}
                      />
                    ) : null}
                    {!waitingSupported ? (
                      <p
                        className="sidebar-sr-only"
                        role="status"
                        aria-live="polite"
                      >
                        {confirmedEmptyQueue && queue.loading
                          ? copy("session.loadingQueue")
                          : ""}
                      </p>
                    ) : null}
                    <div
                      ref={queueRoot}
                      className={`session-tray-content ${queued.some(isQueuedInput) ? "queue-compact-list" : "queue-read-state"}`}
                      hidden={waitingSupported || confirmedEmptyQueue}
                      aria-label={
                        waitingSupported || confirmedEmptyQueue
                          ? undefined
                          : copy("queue.waitingInputs")
                      }
                    >
                      {queue.isPending ? (
                        <p role="status">{copy("session.loadingQueue")}</p>
                      ) : null}
                      <Failure failure={queue.error?.failure} />
                      <ScrollPayloadWindow
                        identity={paginationIdentity}
                        revision={paginationRevision}
                        query={queue}
                        root={queueRoot}
                        active={conversationActive}
                      >
                        {(payload) =>
                          queueRows(
                            payload,
                            live.resources,
                            live.removed,
                            [],
                            id,
                            false,
                          )
                            .filter(isQueuedInput)
                            .map((row) => (
                              <QueuedInput
                                compact
                                active={conversationActive}
                                key={row.id}
                                resource={row}
                                session={session}
                                refresh={queue.refresh}
                                draft={queueDrafts.values.get(row.id)}
                                changeDraft={(value) =>
                                  queueDrafts.save(row.id, value)
                                }
                                readOnly={Boolean(queue.error)}
                              />
                            ))
                        }
                      </ScrollPayloadWindow>
                      {!queue.nextPageToken
                        ? [...queued, ...imageRejected]
                            .filter(
                              (row) =>
                                !queue.rows.some(
                                  (known) => known.id === row.id,
                                ),
                            )
                            .map((row) => (
                              <QueuedInput
                                compact
                                active={conversationActive}
                                key={row.id}
                                resource={row}
                                session={session}
                                refresh={queue.refresh}
                                draft={queueDrafts.values.get(row.id)}
                                changeDraft={(value) =>
                                  queueDrafts.save(row.id, value)
                                }
                                readOnly={Boolean(queue.error)}
                              />
                            ))
                        : null}
                      <ScrollContinuation
                        query={queue}
                        root={queueRoot}
                        active={conversationActive && !confirmedEmptyQueue}
                        label={copy("session.queuePages_1acdd8")}
                      />
                    </div>
                    {imageRejected.length ? (
                      <section
                        className="queue-startup-recovery"
                        aria-label={copy("session.startupImageInput")}
                      >
                        <p role="status">
                          {copy("queue.rejectedImageInputs", {
                            count: imageRejected.length,
                          })}
                        </p>
                        {imageRejected.map((row) => (
                          <QueuedInput
                            key={row.id}
                            resource={row}
                            session={session}
                            refresh={queue.refresh}
                            active={conversationActive}
                            readOnly
                          />
                        ))}
                      </section>
                    ) : null}
                  </div>
                  <div
                    className="session-notices"
                    role="region"
                    aria-label={copy("session.recoveryRegion")}
                  >
                    {live.error || live.state === ConnectionState.Failed ? (
                      <SessionNotice
                        title={sessionStreamFailureTitle(live.error?.code)}
                        details={(opener) => showInfo(opener)}
                      >
                        <p>{connectionLabel}</p>
                        {live.error ? <p>{live.error.message}</p> : null}
                        {live.error?.guidance ? (
                          <p>{live.error.guidance}</p>
                        ) : null}
                        {live.state === ConnectionState.Failed ? (
                          <button type="button" onClick={live.retry}>
                            {copy("session.refreshConnection_73791f")}
                          </button>
                        ) : null}
                      </SessionNotice>
                    ) : null}
                    {startupFailure ? (
                      <SessionNotice
                        title={copy(
                          startupFailure.state === 2
                            ? "session.executionFailureTitle"
                            : "session.recoveryTitle",
                        )}
                        severity={
                          startupFailure.state === 2
                            ? SessionNoticeSeverity.Error
                            : SessionNoticeSeverity.Warning
                        }
                        details={(opener) => showInfo(opener)}
                      >
                        <StartupRecoverySummary failure={startupFailure} />
                        <StartupObservedOperation session={session} />
                        <ExecutionStartupDetails
                          key={text(startupFailure.correlation_id)}
                          failure={startupFailure}
                        />
                        {startupFailure.state === 2 ? (
                          <p>{copy("session.startupManualSteps")}</p>
                        ) : null}
                      </SessionNotice>
                    ) : Object.keys(object(object(data.startup).failure))
                        .length ? (
                      <SessionNotice
                        title={copy("session.attentionRequired")}
                        details={(opener) => showInfo(opener)}
                      >
                        {copy("session.startupInvalidEvidence")}
                      </SessionNotice>
                    ) : null}
                    {text(problem.message) &&
                    !startupFailure &&
                    !(
                      initialExecutionPending(data.problem) &&
                      (progress === SessionProgressPhase.Preparing ||
                        progress === SessionProgressPhase.Queued)
                    ) ? (
                      <SessionProblemNotice
                        fallbackTitle={copy(
                          text(data.dispatch) === "blocked"
                            ? "session.executionBlocked"
                            : "session.attentionRequired",
                        )}
                        code={problem.code ?? problem.problem_code}
                        cause={problem.cause}
                        message={text(problem.message)}
                        guidance={text(problem.guidance)}
                        correlationId={text(problem.correlation_id)}
                        details={(opener) => showInfo(opener)}
                      >
                        <StartupObservedOperation session={session} />
                      </SessionProblemNotice>
                    ) : null}
                    {recovering ? (
                      <SessionNotice
                        title={copy("session.recoveryTitle")}
                        details={(opener) =>
                          showInfo(opener, InfoTarget.Recovery)
                        }
                      >
                        <LocalizedText
                          id="session.recoveryExecutionRemainsUnderServerControl_d80aa1"
                          components={{
                            s0: <>{statusLabel(text(data.recovery))}</>,
                          }}
                        />
                      </SessionNotice>
                    ) : null}
                    {session && Object.hasOwn(data, "startup_rejection") ? (
                      <SessionNotice
                        title={copy("session.executionFailureTitle")}
                        severity={SessionNoticeSeverity.Error}
                        details={(opener) => showInfo(opener)}
                      >
                        <StartupRejection session={session} />
                      </SessionNotice>
                    ) : null}
                    {budgetBlocked ? (
                      <SessionNotice
                        title={copy("session.budgetTitle")}
                        details={(opener) =>
                          showInfo(opener, InfoTarget.Budget)
                        }
                      >
                        {copy(
                          "session-budget.budgetThresholdReachedNewTurnsAnd_6236ce",
                        )}
                      </SessionNotice>
                    ) : null}
                    {control.error ? (
                      <SessionProblemNotice
                        {...clientFailure(control.error)}
                        details={(opener) => showInfo(opener)}
                      />
                    ) : null}
                    {control.uncertain ? (
                      <SessionNotice
                        title={copy("session.recoveryTitle")}
                        details={(opener) =>
                          showInfo(opener, InfoTarget.Recovery)
                        }
                      >
                        <span>{copy("session.startupControlUncertain")}</span>
                        <button
                          type="button"
                          onClick={control.retry}
                          disabled={control.busy}
                        >
                          {copy("session.retryTheSameControlRequest_609aff")}
                        </button>
                      </SessionNotice>
                    ) : null}
                    {submissionError ? (
                      <SessionProblemNotice
                        {...clientFailure(submissionError)}
                      />
                    ) : null}
                    {send.error ? (
                      <SessionProblemNotice
                        {...clientFailure(send.error)}
                        details={(opener) => showInfo(opener)}
                      />
                    ) : null}
                    <RunnerTaskRemediation
                      active={conversationActive}
                      machineId={text(data.machine_id)}
                      disabled={control.busy || control.uncertain}
                      visible={Boolean(startupFailure)}
                      onPending={setRunnerRemediationPending}
                    />
                    <div
                      ref={setRecoveryLauncherTarget}
                      hidden={!inlineRecovery}
                    />
                  </div>
                  <form
                    className="composer"
                    {...imageEntryHandlers(
                      images,
                      locked || !imageRoute.systemSupported,
                    )}
                    onSubmit={(event) => {
                      event.preventDefault();
                      enqueue();
                    }}
                  >
                    <label className="sidebar-sr-only" htmlFor={`prompt-${id}`}>
                      {copy("session.message_2f7766")}
                    </label>
                    <ImageAttachmentInput
                      compact
                      active={conversationActive}
                      draft={images}
                      disabled={locked}
                      available={imageRoute.systemSupported}
                      routeReady={imageRoute.ready}
                      routeLoading={imageRoute.loading}
                      machineId={text(data.machine_id)}
                      controls={
                        <>
                          <label className="plan-mode">
                            <input
                              type="checkbox"
                              checked={mode === Mode.Plan}
                              disabled={locked}
                              onChange={(event) =>
                                setMode(
                                  event.target.checked
                                    ? Mode.Plan
                                    : Mode.Execute,
                                )
                              }
                            />
                            {copy("session.planMode")}
                          </label>
                          <button
                            className="primary composer-submit"
                            aria-label={copy("session.queueMessage_891d4e")}
                            title={copy("session.queueMessage_891d4e")}
                            aria-keyshortcuts={shortcuts.aria(
                              ShortcutId.SessionSend,
                            )}
                            disabled={!canSend}
                          >
                            <svg
                              width="20"
                              height="20"
                              viewBox="0 0 20 20"
                              fill="none"
                              stroke="currentColor"
                              strokeWidth="2"
                              aria-hidden="true"
                            >
                              <path d="M10 16V4m-5 5 5-5 5 5" />
                            </svg>
                          </button>
                        </>
                      }
                    >
                      {skills.wrap(
                        <textarea
                          ref={composer}
                          onKeyDown={(event) => {
                            if (
                              composerComposing.current ||
                              event.nativeEvent.isComposing
                            ) {
                              event.stopPropagation();
                              return;
                            }
                            if (!skills.onKeyDown(event))
                              shortcuts.onKeyDown(event);
                          }}
                          onSelect={skills.onSelect}
                          onCompositionStart={() => {
                            composerComposing.current = true;
                            skills.onCompositionStart();
                          }}
                          onCompositionEnd={() => {
                            composerComposing.current = false;
                            skills.onCompositionEnd();
                          }}
                          {...skills.attributes}
                          aria-keyshortcuts={shortcuts.aria(
                            ShortcutId.SessionFocus,
                            ShortcutId.SessionSend,
                            ShortcutId.SessionNewline,
                          )}
                          id={`prompt-${id}`}
                          value={draft}
                          onChange={(event) =>
                            skills.onChange(
                              event.target.value,
                              event.target.selectionStart,
                            )
                          }
                          disabled={locked}
                          placeholder={copy(
                            "session.sendAFollowUpToThis_c9d723",
                          )}
                          rows={1}
                        />,
                      )}
                      {skills.list}
                      {skills.warning}
                      {imageTextLimit ? (
                        <p role="alert">{copy("image-input.textLimit")}</p>
                      ) : null}
                    </ImageAttachmentInput>
                    {revert.content}
                    {send.uncertain ? (
                      <button
                        className="composer-original-retry"
                        type="button"
                        disabled={send.busy}
                        onClick={send.retry}
                      >
                        {copy("session.retryTheSameMessage_5656d9")}
                      </button>
                    ) : null}
                  </form>
                </div>
              </SessionActivityProvider>
              {active && tabs.tab.kind === SessionTabKind.File ? (
                <div className="session-app-panel">
                  <SessionFilePreview
                    sessionId={id}
                    repository={tabs.tab.repository}
                    path={tabs.tab.path}
                    close={closePanel}
                  />
                </div>
              ) : null}
              {active &&
              (tabs.tab.kind === SessionTabKind.Diff ||
                tabs.tab.kind === SessionTabKind.Comparison) ? (
                <div className="session-app-panel">
                  <SessionDiff
                    sessionId={id}
                    worktree={data.workspace === Workspace.Worktree}
                    close={closePanel}
                    selected={
                      tabs.tab.kind === SessionTabKind.Comparison
                        ? tabs.tab
                        : undefined
                    }
                    openComparison={(value, options) =>
                      tabs.store.openComparison(
                        id,
                        { kind: SessionTabKind.Comparison, ...value },
                        options?.inheritDropped,
                      )
                    }
                    restoreBase={(repository, reference) =>
                      tabs.store.clearDroppedBase(id, repository, reference)
                    }
                  />
                </div>
              ) : null}
              {filesOpened ? (
                <div
                  hidden={!active || panel !== SessionPanel.Files}
                  inert={!active || panel !== SessionPanel.Files}
                  className="session-app-panel"
                >
                  <SessionFiles
                    active={active && panel === SessionPanel.Files}
                    previewing={tabs.tab.kind === SessionTabKind.File}
                    sessionId={id}
                    close={closePanel}
                    openFile={(repository, path) =>
                      tabs.store.open(id, {
                        kind: SessionTabKind.File,
                        repository,
                        path,
                      })
                    }
                  />
                </div>
              ) : null}
              {active && panel === SessionPanel.Diagnostics ? (
                <div className="session-app-panel session-diagnostics-panel">
                  <div className="session-diagnostics">
                    <div ref={setDiagnosticsTarget} />
                    <FlatDisclosureScope>
                      <RequestDiagnostics sessionId={id} close={closePanel} />
                    </FlatDisclosureScope>
                  </div>
                </div>
              ) : null}
              {session && !embedded ? (
                <SessionBrowser
                  key={`${id}:${browserAccountId}`}
                  session={session}
                  accountId={browserAccountId}
                  active={active}
                  dialogOpen={browserDialog && active}
                  dismissDialog={() => setBrowserDialog(false)}
                  handle={browserHandle}
                  statusTarget={terminalStatusTarget}
                  selectedPage={
                    tabs.tab.kind === SessionTabKind.Page ? tabs.tab : undefined
                  }
                />
              ) : null}
              {session && terminalOpened ? (
                <div
                  hidden={!active || tabs.tab.kind !== SessionTabKind.Terminal}
                  className="session-app-panel session-terminal-pane"
                >
                  <SessionTerminals
                    session={session}
                    close={closeTerminal}
                    openIntent={terminalOpenIntent}
                    finishOpenIntent={() => setTerminalOpenIntent(undefined)}
                    sessionActive={active}
                    statusTarget={terminalStatusTarget}
                    tabbed
                    selectedId={
                      tabs.tab.kind === SessionTabKind.Terminal
                        ? tabs.tab.id
                        : ""
                    }
                    openTerminal={(terminalId) =>
                      tabs.store.open(id, {
                        kind: SessionTabKind.Terminal,
                        id: terminalId,
                      })
                    }
                    hideEmpty={hideEmptyTerminals}
                    dismissTerminal={(terminalId, fallback) => {
                      const selected =
                        tabs.store.snapshot(id).selected ===
                        sessionTabKey({
                          kind: SessionTabKind.Terminal,
                          id: terminalId,
                        });
                      const target = tabs.store.dismissTerminal(
                        id,
                        terminalId,
                        fallback,
                      );
                      if (selected) focusSelectedTabAfterCommit(target);
                    }}
                    active={active && tabs.tab.kind === SessionTabKind.Terminal}
                  />
                </div>
              ) : null}
              {tabs.tabs
                .filter((tab) => tab.kind === SessionTabKind.PendingSidechat)
                .map((tab) =>
                  tab.kind === SessionTabKind.PendingSidechat ? (
                    <div
                      key={tab.requestId}
                      hidden={!active || tabs.selected !== sessionTabKey(tab)}
                      inert={!active || tabs.selected !== sessionTabKey(tab)}
                      className="session-sidechat-pane"
                    >
                      <PendingSidechatPane
                        requestId={tab.requestId}
                        active={active && tabs.selected === sessionTabKey(tab)}
                      />
                    </div>
                  ) : null,
                )}
              {tabs.store.sidechats(id).map((tab) =>
                tab.kind === SessionTabKind.Sidechat ? (
                  <div
                    key={tab.id}
                    hidden={!active || tabs.selected !== sessionTabKey(tab)}
                    inert={!active || tabs.selected !== sessionTabKey(tab)}
                    className="session-sidechat-pane"
                  >
                    <SidechatPane
                      id={tab.id}
                      active={active && tabs.selected === sessionTabKey(tab)}
                    />
                  </div>
                ) : null,
              )}
            </div>
          </div>
          <aside
            hidden={infoHidden}
            inert={infoHidden}
            ref={information}
            id={`info-${id}`}
            className="session-information"
            aria-labelledby={`info-title-${id}`}
          >
            <header {...infoEntrance}>
              <h2 ref={infoHeading} tabIndex={-1} id={`info-title-${id}`}>
                {copy("session.sessionInformation")}
              </h2>
              {session ? <SavedExecutionChips resource={session} /> : null}
            </header>
            <SessionActivityProvider
              active={
                active &&
                ![
                  SessionTabKind.Sidechat,
                  SessionTabKind.PendingSidechat,
                ].includes(tabs.tab.kind)
              }
            >
              <FlatDisclosureScope>
                <div className="session-information-body">
                  <div ref={setInfoToolsTarget} tabIndex={-1} />
                  {session ? (
                    <>
                      <section
                        hidden={prEmpty}
                        className="session-information-section"
                      >
                        <h3>{copy("session.pullRequests")}</h3>
                        <SessionPullRequests
                          key={id}
                          session={session}
                          emptyChanged={setPREmpty}
                          diagnosticsTarget={diagnosticsTarget}
                        />
                      </section>
                      <section
                        hidden={subagentsEmpty}
                        className="session-information-section"
                      >
                        <h3>{copy("session.subagents")}</h3>
                        <Subagents
                          key={id}
                          sessionId={id}
                          revision={session.revision.toString()}
                          emptyChanged={setSubagentsEmpty}
                          diagnosticsTarget={diagnosticsTarget}
                        />
                      </section>
                      <section className="session-information-section">
                        <h3>{copy("session.context")}</h3>
                        <SessionContext key={id} session={session} />
                      </section>
                      <section
                        ref={budgetDetails}
                        className="session-information-section"
                        tabIndex={-1}
                      >
                        <h3>{copy("session.usageAndBudget")}</h3>
                        <SessionLifetimeUsage
                          session={session}
                          diagnosticsTarget={diagnosticsTarget}
                        />
                        {diagnosticsTarget
                          ? createPortal(
                              <NativeUsage
                                session={session}
                                diagnosticsTarget={diagnosticsTarget}
                              />,
                              diagnosticsTarget,
                            )
                          : null}
                        <SessionBudget
                          resource={session}
                          changed={setAcknowledged}
                          blocked={setBudgetBlocked}
                          evidence={reportBudgetEvidence}
                        />
                      </section>
                      <ExecutionConfiguration
                        resource={session}
                        diagnosticsTarget={diagnosticsTarget}
                      />
                      <SessionStorageAction source={session} />
                    </>
                  ) : null}
                </div>
              </FlatDisclosureScope>
            </SessionActivityProvider>
          </aside>
        </div>
      </section>
    </SessionActivityProvider>
  );
}

function SidechatPane({ id, active }: { id: string; active: boolean }) {
  const store = useSessionTabsStore();
  // Decided once at mount: a Sidechat that `publishSidechat` promoted or
  // published automatically does not animate on mount, while one the user
  // opened (`open`, for example from a Search result) does.
  const [activateOnMount] = useState(() => !store.automaticSidechat(id));
  return (
    <SidechatAuthoring id={id} active={active}>
      {({ draft, setDraft, bindings, setBindings }) => (
        <SessionActivityProvider active={active}>
          <SessionView
            id={id}
            active={active}
            embedded
            activateOnMount={activateOnMount}
            draft={draft}
            setDraft={setDraft}
            initialSkills={bindings}
            changeSkills={setBindings}
          />
        </SessionActivityProvider>
      )}
    </SidechatAuthoring>
  );
}
