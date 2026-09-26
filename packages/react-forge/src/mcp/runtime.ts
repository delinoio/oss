import { SceneSession } from "../scene/session.js";
import { resolve } from "node:path";
import { matchesOutputExtension } from "../output-extension.js";
import { capabilities } from "../capabilities.js";
import { ForgeError, checkSignal } from "../errors.js";
import { DocumentSession } from "../session.js";
import { FigmaSession } from "../figma/session.js";
import { ErrorCode, Format, limits } from "../types.js";
import { TaskLoader } from "./loader.js";
import { TaskError, TaskPhase, type TaskDetails } from "./diagnostics.js";
import { InspectionView, Operation, SessionStatus, parse, type Input, type ToolValue } from "./contract.js";

export type McpSession = DocumentSession | FigmaSession | SceneSession;
export interface TaskContext {
  session: McpSession | undefined;
  state: Map<string, unknown>;
  data: unknown;
  signal: AbortSignal;
}
export type SessionTask = (context: TaskContext) => McpSession | void | Promise<McpSession | void>;
interface RecordEntry {
  session: McpSession;
  state: Map<string, unknown>;
  queue: Promise<unknown>;
  status: SessionStatus;
}

/** Lives in one child process, retaining the engine's shared publication queues. */
export class SessionRuntime {
  private readonly sessions = new Map<string, RecordEntry>();
  private readonly loader: TaskLoader;
  private stopped = false;

  constructor(private readonly cwd: string) { this.loader = new TaskLoader(cwd); }

  diagnose(error: unknown): TaskDetails | undefined { return this.loader.diagnose(error); }

  private metadata(record: RecordEntry): ToolValue {
    return { sessionId: record.session.documentId, format: record.session.format, revision: record.session.revision };
  }

  private record(id: string): RecordEntry {
    const record = this.sessions.get(id);
    if (!record) throw new ForgeError(ErrorCode.InvalidTarget, "Session is unknown or closed.");
    return record;
  }

  async call(operation: Operation, raw: unknown, signal: AbortSignal): Promise<ToolValue> {
    if (this.stopped) throw new ForgeError(ErrorCode.Disposed, "MCP runtime is shutting down.");
    checkSignal(signal);
    const input = parse(operation, raw);
    if (operation === Operation.Capabilities) return {
      ...capabilities,
      mcp: { transport: "stdio", memoryOnly: true, trustedCode: true, automaticTimeout: false, sourceAndDataBytes: limits.treeBytes,
        inspectionDefaultLimit: 100, inspectionMaxLimit: 500, textPreviewCharacters: 4096,
        callback: "A default-exported function receives { session, state, data, signal }. state is a Map; signal is an AbortSignal.",
        creation: "Return a new DocumentSession, SceneSession or FigmaSession.", update: "Return void or the same session. state is a session-owned Map.",
        inlineImports: "Relative to --cwd; automatic React JSX runtime. File imports are relative to the entry. Dependencies are cached; entries are reevaluated.",
        errorDiagnostics: "Compile, task and uncaught render failures include bounded caller messages and known one-based source positions in tool error results, never operational stderr.",
        cancellation: "Cooperative; the same session remains queued until its callback finishes. No rollback of completed side effects.",
        figmaAuthentication: "Existing macOS Keychain support only; no new credentials or platform expansion.",
      },
    };
    if (operation === Operation.Sessions) {
      const { offset, limit } = input as Input<Operation.Sessions>;
      const entries = [...this.sessions.values()];
      return { sessions: entries.slice(offset, offset + limit).map(entry => ({ ...this.metadata(entry), status: entry.status })), total: entries.length, truncated: offset + limit < entries.length, nextOffset: offset + limit < entries.length ? offset + limit : null };
    }
    const id = (input as { sessionId?: string }).sessionId;
    if (operation === Operation.Execute && !id) return this.execute(input as Input<Operation.Execute>, signal);
    const record = this.record(id!);
    const result = record.queue.catch(() => {}).then(async () => {
      checkSignal(signal);
      if (this.stopped || this.sessions.get(id!) !== record) throw new ForgeError(ErrorCode.Disposed, "Session is closed.");
      record.status = operation === Operation.Close ? SessionStatus.Closing : SessionStatus.Running;
      try { return await this.dispatch(operation, input, record, signal); }
      finally { if (record.status !== SessionStatus.Closing) record.status = SessionStatus.Idle; }
    });
    // Do not race this promise against cancellation: trusted callbacks may keep
    // mutating after abort. The next call must wait for their actual completion.
    record.queue = result.catch(() => {});
    return result;
  }

  private async execute(input: Input<Operation.Execute>, signal: AbortSignal, record?: RecordEntry): Promise<ToolValue> {
    const run = await this.loader.load(input, signal);
    if (typeof run !== "function") throw new ForgeError(ErrorCode.MalformedInput, "TSX must default-export a task function.");
    const state = record?.state ?? new Map<string, unknown>();
    let returned: unknown;
    try {
      returned = await (run as SessionTask)({ session: record?.session, state, data: input.data, signal });
      if (record) {
        if (returned !== undefined && returned !== record.session) throw new ForgeError(ErrorCode.InvalidTarget, "An update must return void or its existing session.");
        return this.metadata(record);
      }
      if (!(returned instanceof DocumentSession || returned instanceof FigmaSession || returned instanceof SceneSession)) throw new ForgeError(ErrorCode.MalformedInput, "A new task must return a document session.");
      if (this.sessions.has(returned.documentId)) throw new ForgeError(ErrorCode.Conflict, "This session is already registered.");
      checkSignal(signal);
      if (this.stopped) throw new ForgeError(ErrorCode.Disposed, "MCP runtime is shutting down.");
      returned.inspect(); // Reject an already disposed session before admission.
      const entry: RecordEntry = { session: returned, state, queue: Promise.resolve(), status: SessionStatus.Idle };
      this.sessions.set(returned.documentId, entry);
      return this.metadata(entry);
    } catch (error) {
      if (!record) state.clear();
      if ((returned instanceof DocumentSession || returned instanceof FigmaSession || returned instanceof SceneSession) && !this.sessions.has(returned.documentId)) {
        await returned.dispose().catch(() => {});
      }
      if (error instanceof ForgeError) throw error;
      const resolution = this.loader.resolutionFailure(error);
      if (resolution) throw resolution;
      throw this.loader.isCallerException(error)
        ? new TaskError(ErrorCode.Render, TaskPhase.Task, error)
        : new ForgeError(ErrorCode.Render, "Task execution failed. Correct the task and retry.");
    }
  }

  private async dispatch(operation: Operation, input: Input<Operation>, record: RecordEntry, signal: AbortSignal): Promise<ToolValue> {
    const session = record.session;
    switch (operation) {
      case Operation.Execute: return this.execute(input as Input<Operation.Execute>, signal, record);
      case Operation.Inspect: {
        const { view, offset, limit, nodeId, kind } = input as Input<Operation.Inspect>;
        if (view === InspectionView.Receipt) {
          this.figma(session);
          return { ...this.metadata(record), receipt: session.receipt ?? null };
        }
        const snapshot = session instanceof DocumentSession || session instanceof SceneSession ? await session.snapshot({ signal }) : session.inspect();
        const targets = snapshot.targets.filter(target => (!nodeId || target.nodeId === nodeId) && (!kind || target.kind === kind));
        return { ...this.metadata(record), revision: snapshot.revision,
          targets: targets.slice(offset, offset + limit).map(target => {
            if (!("text" in target) || typeof target.text !== "string" || target.text.length <= 4096) return target;
            return { ...target, text: target.text.slice(0, 4096), textTruncated: true };
          }), total: targets.length, truncated: offset + limit < targets.length, nextOffset: offset + limit < targets.length ? offset + limit : null };
      }
      case Operation.Measure: {
        const { nodeId, revision } = input as Input<Operation.Measure>;
        const handle = { documentId: session.documentId, nodeId };
        const geometry = await session.measure(handle, { revision, signal });
        return { ...this.metadata(record), revision: geometry.revision, geometry };
      }
      case Operation.Refresh: {
        this.figma(session);
        const { sessionId: _, ...selection } = input as Input<Operation.Refresh>;
        await session.refresh({ ...selection, signal });
        return { ...this.metadata(record), targetCount: session.inspect().targets.length };
      }
      case Operation.Export: {
        if (!(session instanceof DocumentSession || session instanceof SceneSession)) throw new ForgeError(ErrorCode.UnsupportedEdit, "Use publish for Figma sessions.");
        const { output, overwrite } = input as Input<Operation.Export>;
        if (!matchesOutputExtension(session.format, output)) throw new ForgeError(ErrorCode.MalformedInput, "Output extension must match the session format.");
        const result = await session.exportFile(resolve(this.cwd, output), { overwrite, signal });
        return { ...this.metadata(record), ...result };
      }
      case Operation.Publish: {
        this.figma(session);
        const { receiptPath, overwrite } = input as Input<Operation.Publish>;
        const receipt = receiptPath
          ? await session.exportFile(resolve(this.cwd, receiptPath), { overwrite, signal })
          : await session.publish({ signal });
        return { ...this.metadata(record), revision: receipt.revision, receipt };
      }
      case Operation.Close: {
        await session.dispose();
        record.state.clear();
        this.sessions.delete(session.documentId);
        return { sessionId: session.documentId, format: session.format, revision: session.revision, closed: true };
      }
      default: throw new ForgeError(ErrorCode.MalformedInput, "Unknown operation.");
    }
  }

  private figma(session: McpSession): asserts session is FigmaSession {
    if (session.format !== Format.Figma || !(session instanceof FigmaSession)) throw new ForgeError(ErrorCode.UnsupportedEdit, "This operation requires a Figma session.");
  }

  async dispose(): Promise<void> {
    this.stopped = true;
    const records = [...this.sessions.values()];
    await Promise.allSettled(records.map(async record => {
      record.status = SessionStatus.Closing;
      await record.session.dispose();
      await record.queue;
      record.state.clear();
    }));
    this.sessions.clear();
    this.loader.dispose();
  }
}
