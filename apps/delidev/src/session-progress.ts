// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document as readDocument, object, Workspace } from "./documents";

export enum SessionProgressPhase {
  Preparing = "preparing",
  Queued = "queued",
  Starting = "starting",
  Response = "response",
}
export enum ResponseEvidenceKind {
  InitialUser = "initial-user",
  Pending = "pending",
  Visible = "visible",
  Unavailable = "unavailable",
}
export interface ResponseEvidence {
  executionId: string;
  threadId: string;
  turnId: string;
  inputId?: string;
  kind: ResponseEvidenceKind;
  lastSequence?: number;
}
export interface ProgressMessage {
  id: string;
  revision: bigint;
  response?: ResponseEvidence;
}
const uuid = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(
    value,
  );
/**
 * The complete ordered accepted-input list is valid evidence only when it is a
 * bounded array of unique, digest-bound inputs whose first entry is the selected
 * original input. Shared by progress and startup-timing settlement.
 */
export function acceptedInputsBind(
  inputs: unknown,
  selectedInputId: unknown,
): boolean {
  if (!Array.isArray(inputs) || !inputs.length || inputs.length > 4095)
    return false;
  const accepted = inputs.map(object);
  return (
    accepted[0].input_id === selectedInputId &&
    accepted.every(
      (input) =>
        uuid(input.input_id) &&
        typeof input.prompt_digest === "string" &&
        /^[a-f0-9]{64}$/.test(input.prompt_digest),
    ) &&
    new Set(accepted.map((input) => input.input_id)).size === accepted.length
  );
}
const identity = (value: unknown): value is string =>
  typeof value === "string" &&
  value.trim().length > 0 &&
  new TextEncoder().encode(value).length <= 1024 &&
  !/[\u0000-\u001f\u007f\uD800-\uDFFF]/u.test(value);
const count = (value: unknown): value is number =>
  Number.isSafeInteger(value) && Number(value) >= 0;
/** A bounded suppression projection, never retained message text or native output. */
export function responseEvidence(
  row: Resource,
  sessionId: string,
): ResponseEvidence | undefined {
  const d = readDocument(row);
  if (
    row.kind !== EntityKind.MESSAGE ||
    row.sessionId !== sessionId ||
    row.schemaVersion !== 1 ||
    !uuid(row.id) ||
    row.revision <= 0n ||
    !uuid(d.execution_id) ||
    !identity(d.native_thread_id) ||
    !identity(d.native_turn_id)
  )
    return;
  const result: ResponseEvidence = {
    executionId: d.execution_id,
    threadId: d.native_thread_id,
    turnId: d.native_turn_id,
    kind: ResponseEvidenceKind.Unavailable,
  };
  if (
    !count(d.first_sequence) ||
    d.first_sequence === 0 ||
    !count(d.last_sequence) ||
    d.last_sequence < d.first_sequence ||
    !["streaming", "complete"].includes(String(d.state)) ||
    typeof d.text !== "string" ||
    new TextEncoder().encode(d.text).length > 256 * 1024 ||
    /[\u0000\uD800-\uDFFF]/u.test(d.text) ||
    d.inherited != null
  )
    return result;
  result.lastSequence = d.last_sequence;
  const families = [
    "tool",
    "artifact",
    "progress",
    "claude",
    "claude_tool",
    "claude_progress",
    "claude_interruption",
    "grok_tool",
    "grok_text",
    "grok_user",
  ].filter((key) => d[key] != null);
  if (d.role === "user") {
    if (!uuid(d.input_id) || families.some((key) => key !== "grok_user"))
      return result;
    return {
      ...result,
      inputId: d.input_id,
      kind: ResponseEvidenceKind.InitialUser,
    };
  }
  // Ambiguous/malformed native content suppresses generic activity too. Only
  // an ordinary empty assistant start remains eligible to wait for content.
  if (d.role === "assistant" && !families.length)
    return {
      ...result,
      kind: d.text.length
        ? ResponseEvidenceKind.Visible
        : ResponseEvidenceKind.Pending,
    };
  if (
    ["tool", "artifact", "progress"].includes(String(d.role)) &&
    !families.length &&
    d.text.length > 0
  )
    return { ...result, kind: ResponseEvidenceKind.Visible };
  return result;
}

export function progressMessages(
  base: readonly ProgressMessage[],
  live: ReadonlyMap<string, Resource>,
  removed: ReadonlySet<string>,
  sessionId: string,
): ProgressMessage[] {
  const result = new Map(
    base.filter((row) => !removed.has(row.id)).map((row) => [row.id, row]),
  );
  for (const row of live.values()) {
    if (
      row.kind !== EntityKind.MESSAGE ||
      row.sessionId !== sessionId ||
      removed.has(row.id) ||
      (result.get(row.id)?.revision ?? 0n) >= row.revision
    )
      continue;
    result.set(row.id, {
      id: row.id,
      revision: row.revision,
      response: responseEvidence(row, sessionId),
    });
  }
  return [...result.values()];
}
function originalQueue(input: Resource, sessionId: string): boolean {
  const d = readDocument(input);
  return (
    input.kind === EntityKind.QUEUE &&
    input.sessionId === sessionId &&
    input.schemaVersion === 1 &&
    input.revision > 0n &&
    input.documentJson.byteLength <= 1 << 20 &&
    uuid(input.id) &&
    count(d.sequence) &&
    d.sequence > 0 &&
    count(d.content_revision) &&
    d.content_revision > 0 &&
    typeof d.prompt === "string" &&
    ["execute", "plan"].includes(String(d.mode)) &&
    [
      "queued",
      "claimed",
      "accepted",
      "uncertain",
      "removed",
      "rejected-before-start",
    ].includes(String(d.delivery)) &&
    (d.execution_id === undefined || uuid(d.execution_id)) &&
    (d.native_request_id === undefined || uuid(d.native_request_id))
  );
}
/** Exact server initial-readiness sentinel, never a general unavailable error. */
export function initialExecutionPending(value: unknown): boolean {
  const problem = object(value);
  return (
    problem.code === "unavailable" &&
    problem.message ===
      "The first execution is waiting for its workspace, Runner Device or account." &&
    problem.guidance ===
      "Prepare the workspace, connect the selected Runner Device and validate the selected account. Inspect the retained session for the current blocking reason." &&
    Object.keys(problem).length === 3
  );
}
enum StartupState {
  Ready = 1,
}
enum StartupPhase {
  Settings = 4,
}
enum StartupDelivery {
  NotSent = 1,
}
function validReady(value: unknown, jobId: unknown): boolean {
  const ready = object(value);
  const protocols = {
    codex: "codex-app-server-v2",
    "claude-code": "claude-stream-json",
    opencode: "opencode-http",
    "grok-build": "grok-acp",
  };
  return (
    ready.state === StartupState.Ready &&
    ready.phase === StartupPhase.Settings &&
    ready.input_delivery === StartupDelivery.NotSent &&
    ready.correlation_id === jobId &&
    typeof ready.harness === "string" &&
    Object.hasOwn(protocols, ready.harness) &&
    ready.protocol === protocols[ready.harness as keyof typeof protocols] &&
    typeof ready.executable_sha256 === "string" &&
    /^[a-f0-9]{64}$/.test(ready.executable_sha256) &&
    (ready.problem_code === undefined || ready.problem_code === "") &&
    (ready.cleanup === undefined || ready.cleanup === 0) &&
    (ready.native_version === undefined ||
      (typeof ready.native_version === "string" &&
        /^[A-Za-z0-9.+_-]{1,64}$/.test(ready.native_version))) &&
    Object.keys(ready).every((key) =>
      [
        "state",
        "phase",
        "harness",
        "native_version",
        "executable_sha256",
        "protocol",
        "problem_code",
        "correlation_id",
        "input_delivery",
        "cleanup",
      ].includes(key),
    )
  );
}
export interface ProgressObservation {
  session?: Resource;
  sessionId: string;
  current: boolean;
  complete: boolean;
  blocked: boolean;
  messages: readonly ProgressMessage[];
  queueCurrent: boolean;
  queue: readonly Resource[];
}
export function sessionProgress(
  observation: ProgressObservation,
): SessionProgressPhase | undefined {
  const { session: row, sessionId, messages } = observation;
  if (
    !observation.current ||
    !observation.complete ||
    observation.blocked ||
    !row ||
    row.kind !== EntityKind.SESSION ||
    row.id !== sessionId ||
    row.schemaVersion !== 1 ||
    row.revision <= 0n ||
    row.revision > 0xffffffffffffffffn ||
    row.documentJson.byteLength > 1 << 20 ||
    !uuid(sessionId)
  )
    return;
  const d = readDocument(row),
    preparation = object(d.preparation),
    execution = object(d.execution),
    selected = object(d.current_execution ?? d.initial_execution),
    startup = object(d.startup);
  // The server retains blocked dispatch until the original first claim. This
  // exception projects only its untouched initial input, not retry readiness.
  const initialPending =
    initialExecutionPending(d.problem) &&
    d.dispatch === "blocked" &&
    d.outcome === "not-started" &&
    d.active_execution_id == null &&
    d.initial_execution == null &&
    d.current_execution == null &&
    d.execution == null &&
    d.startup == null &&
    d.last_input_sequence === 1 &&
    d.pending_inputs === 1 &&
    observation.queueCurrent &&
    observation.queue.length === 1 &&
    originalQueue(observation.queue[0]!, sessionId) &&
    readDocument(observation.queue[0]!).sequence === 1 &&
    readDocument(observation.queue[0]!).delivery === "queued" &&
    readDocument(observation.queue[0]!).execution_id == null &&
    readDocument(observation.queue[0]!).native_request_id == null;
  if (
    d.archive !== "active" ||
    d.recovery !== "none" ||
    (!initialPending && !["ready", "claimed"].includes(String(d.dispatch))) ||
    !["not-started", "running"].includes(String(d.outcome)) ||
    (d.problem != null && !initialPending) ||
    d.startup_rejection != null ||
    d.execution_recovery_job_id != null ||
    d.compaction_job_id != null ||
    startup.failure != null ||
    !count(d.pending_inputs)
  )
    return;
  if (
    (d.execution != null && !Object.keys(execution).length) ||
    (d.startup != null &&
      (!uuid(startup.job_id) ||
        !uuid(startup.execution_id) ||
        Object.keys(startup).some(
          (key) =>
            !["job_id", "execution_id", "ready", "failure"].includes(key),
        )))
  )
    return;
  if (startup.ready !== undefined && !validReady(startup.ready, startup.job_id))
    return;
  if (
    !uuid(preparation.job_id) ||
    preparation.recovery_job_id != null ||
    !["pending", "ready"].includes(String(preparation.state))
  )
    return;
  if (preparation.state === "pending")
    return !messages.length &&
      !d.active_execution_id &&
      (d.dispatch === "ready" || initialPending)
      ? SessionProgressPhase.Preparing
      : undefined;
  if (!d.active_execution_id) {
    if (
      (!initialPending && d.dispatch !== "ready") ||
      !observation.queueCurrent ||
      d.pending_inputs === 0 ||
      messages.length
    )
      return;
    // A pending count is not proof that its original queued input is eligible.
    const inputs = observation.queue.filter((input) =>
      originalQueue(input, sessionId),
    );
    if (
      inputs.length !== observation.queue.length ||
      inputs.some(
        (input) =>
          !["queued", "removed", "accepted", "rejected-before-start"].includes(
            String(readDocument(input).delivery),
          ),
      )
    )
      return;
    return inputs.some(
      (input) =>
        readDocument(input).delivery === "queued" &&
        !readDocument(input).execution_id,
    )
      ? SessionProgressPhase.Queued
      : undefined;
  }
  if (
    d.dispatch !== "claimed" ||
    !uuid(d.active_execution_id) ||
    selected.id !== d.active_execution_id ||
    !uuid(selected.input_id)
  )
    return;
  if (Object.keys(startup).length && startup.execution_id !== selected.id)
    return;
  if (!Object.keys(execution).length)
    return !messages.length ? SessionProgressPhase.Starting : undefined;
  const waiting = object(execution.waiting);
  if (
    execution.execution_id !== selected.id ||
    execution.input_id !== selected.input_id ||
    !uuid(execution.job_id) ||
    !count(execution.last_sequence) ||
    execution.last_sequence === 0 ||
    !identity(execution.native_thread_id) ||
    typeof waiting.user_input !== "boolean" ||
    typeof waiting.approval !== "boolean" ||
    waiting.user_input ||
    waiting.approval ||
    (execution.unconfirmed_responses !== undefined &&
      (!count(execution.unconfirmed_responses) ||
        execution.unconfirmed_responses !== 0)) ||
    execution.cleanup_verified === true ||
    [
      "claude_stop",
      "claude_denial",
      "claude_terminal",
      "claude_interruption",
      "opencode_stop",
      "grok_stop",
      "grok_terminal",
      "grok_tools_terminal",
    ].some((key) => execution[key] != null)
  )
    return;
  if (
    Object.keys(startup).length &&
    (startup.execution_id !== selected.id ||
      startup.job_id !== execution.job_id)
  )
    return;
  if (execution.outcome === "not-started")
    return !messages.length && !execution.native_turn_id
      ? SessionProgressPhase.Starting
      : undefined;
  if (execution.outcome !== "running" || !identity(execution.native_turn_id))
    return;
  if (execution.accepted_inputs !== undefined) {
    if (!acceptedInputsBind(execution.accepted_inputs, selected.input_id))
      return;
  } else {
    // Supported legacy progress needs an independently accepted original input.
    if (
      !observation.queueCurrent ||
      !observation.queue.some(
        (input) =>
          input.id === selected.input_id &&
          originalQueue(input, sessionId) &&
          readDocument(input).execution_id === selected.id &&
          readDocument(input).delivery === "accepted",
      )
    )
      return;
  }
  const lastSequence = Number(execution.last_sequence);
  if (
    messages.some(
      (message) =>
        !message.response ||
        message.response.lastSequence === undefined ||
        message.response.lastSequence > lastSequence ||
        message.response.executionId !== selected.id ||
        message.response.threadId !== execution.native_thread_id ||
        message.response.turnId !== execution.native_turn_id ||
        ![
          ResponseEvidenceKind.InitialUser,
          ResponseEvidenceKind.Pending,
        ].includes(message.response.kind) ||
        (message.response.kind === ResponseEvidenceKind.InitialUser &&
          message.response.inputId !== selected.input_id),
    )
  )
    return;
  return SessionProgressPhase.Response;
}

/** Presentation only: a validated progress phase must still own the first input. */
export function initialSessionStartup(
  row: Resource | undefined,
  phase: SessionProgressPhase | undefined,
  queueCurrent: boolean,
  queue: readonly Resource[],
): boolean {
  if (!row || phase === undefined) return false;
  const d = readDocument(row),
    initial = object(d.initial_execution),
    selected = object(d.current_execution ?? d.initial_execution),
    execution = object(d.execution);
  if (
    ![Workspace.Local, Workspace.Worktree, Workspace.GeneralChat].includes(
      d.workspace as Workspace,
    ) ||
    // Unknown first-input history is never pristine: require the durable counter.
    d.last_input_sequence !== 1 ||
    queue.some((input) => readDocument(input).sequence !== 1)
  )
    return false;
  if (
    phase === SessionProgressPhase.Preparing ||
    phase === SessionProgressPhase.Queued
  )
    return (
      d.initial_execution == null &&
      d.current_execution == null &&
      d.execution == null &&
      d.last_input_sequence === 1 &&
      d.pending_inputs === 1 &&
      queueCurrent &&
      queue.length === 1 &&
      originalQueue(queue[0]!, row.id) &&
      readDocument(queue[0]!).sequence === 1 &&
      readDocument(queue[0]!).delivery === "queued" &&
      readDocument(queue[0]!).execution_id == null &&
      readDocument(queue[0]!).native_request_id == null
    );
  if (
    !uuid(initial.id) ||
    !uuid(initial.input_id) ||
    initial.id !== d.active_execution_id ||
    selected.id !== initial.id ||
    selected.input_id !== initial.input_id
  )
    return false;
  if (phase === SessionProgressPhase.Starting)
    // The original queue row must be current and still owned by this execution;
    // rejected, removed, uncertain, foreign or later rows retain Info.
    return (
      queueCurrent &&
      queue.every((input) => {
        const queued = readDocument(input);
        return (
          input.id === initial.input_id &&
          originalQueue(input, row.id) &&
          queued.sequence === 1 &&
          ["queued", "claimed", "accepted"].includes(String(queued.delivery)) &&
          (queued.execution_id === undefined ||
            queued.execution_id === initial.id)
        );
      })
    );
  // Multiple accepted inputs are a later turn, even on the initial execution.
  if (Array.isArray(execution.accepted_inputs))
    return (
      execution.accepted_inputs.length === 1 &&
      object(execution.accepted_inputs[0]).input_id === initial.input_id
    );
  // Legacy evidence needs the retained first queue row and first-input counter.
  return (
    d.last_input_sequence === 1 &&
    queueCurrent &&
    queue.some(
      (input) =>
        input.id === initial.input_id &&
        originalQueue(input, row.id) &&
        readDocument(input).sequence === 1 &&
        readDocument(input).execution_id === initial.id &&
        readDocument(input).delivery === "accepted",
    )
  );
}

/** Original claim/turn identity only; revisions never create a new response owner. */
export function progressResponseOwner(
  row: Resource | undefined,
  sessionId: string,
) {
  if (
    !row ||
    row.kind !== EntityKind.SESSION ||
    row.id !== sessionId ||
    row.schemaVersion !== 1 ||
    row.revision <= 0n
  )
    return;
  const d = readDocument(row),
    selected = object(d.current_execution ?? d.initial_execution),
    execution = object(d.execution);
  if (
    !uuid(d.active_execution_id) ||
    selected.id !== d.active_execution_id ||
    !uuid(selected.input_id) ||
    execution.execution_id !== selected.id ||
    execution.input_id !== selected.input_id ||
    !uuid(execution.job_id) ||
    !identity(execution.native_thread_id) ||
    !identity(execution.native_turn_id) ||
    !count(execution.last_sequence)
  )
    return;
  return {
    key: JSON.stringify([
      sessionId,
      execution.job_id,
      selected.id,
      selected.input_id,
      execution.native_thread_id,
      execution.native_turn_id,
    ]),
    executionId: selected.id,
    threadId: execution.native_thread_id,
    turnId: execution.native_turn_id,
    lastSequence: execution.last_sequence,
  };
}
export function responseSuppressesProgress(
  messages: readonly ProgressMessage[],
  owner: ReturnType<typeof progressResponseOwner>,
): boolean {
  return Boolean(
    owner &&
    messages.some(
      (row) =>
        row.response &&
        row.response.executionId === owner.executionId &&
        row.response.threadId === owner.threadId &&
        row.response.turnId === owner.turnId &&
        (row.response.kind === ResponseEvidenceKind.Unavailable ||
          (row.response.kind === ResponseEvidenceKind.Visible &&
            row.response.lastSequence !== undefined &&
            row.response.lastSequence <= owner.lastSequence)),
    ),
  );
}
