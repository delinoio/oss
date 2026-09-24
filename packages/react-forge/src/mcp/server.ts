import { fork } from "node:child_process";
import { realpath, stat } from "node:fs/promises";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { CallToolRequestSchema, ListToolsRequestSchema, type CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { ForgeError } from "../errors.js";
import { ErrorCode, limits } from "../types.js";
import { MessageKind, Operation, failure, parse, tools, type ParentMessage, type ChildMessage } from "./contract.js";

interface Pending { resolve(result: CallToolResult): void; cleanup(): void }

export async function serve(cwd: string): Promise<number> {
  try {
    cwd = await realpath(cwd);
    if (!(await stat(cwd)).isDirectory()) throw new Error();
  } catch { throw new ForgeError(ErrorCode.Io, "MCP working directory must be an existing directory."); }
  const server = new Server({ name: "react-forge", version: "0.0.0" }, { capabilities: { tools: {} }, instructions: "Use react_forge_capabilities for the trusted TSX contract. Sessions are memory-only. Explicitly export local files or publish Figma. Never blindly retry uncertain writes." });
  // JSON escaping can expand a valid 16 MiB source sixfold on the wire. This
  // bounds unfinished frames while leaving room for that encoding and metadata.
  const transport = new StdioServerTransport(process.stdin, process.stdout, { maxBufferSize: limits.treeBytes * 6 + 64 * 1024 });
  const child = fork(new URL("./worker.js", import.meta.url), [], {
    cwd, execArgv: ["--import", import.meta.resolve("tsx")], stdio: ["ignore", "ignore", "ignore", "ipc"], serialization: "json",
  });
  const pending = new Map<number, Pending>();
  let nextId = 0;
  let stopping: Promise<void> | undefined;
  let code = 0;
  let exited = false;
  let resolveExit!: () => void;
  const exit = new Promise<void>(resolve => { resolveExit = resolve; });
  let resolveDone!: () => void;
  const done = new Promise<void>(resolve => { resolveDone = resolve; });
  let readyResolve!: () => void;
  let readyReject!: (error: unknown) => void;
  const ready = new Promise<void>((resolve, reject) => { readyResolve = resolve; readyReject = reject; });

  const log = (operation: string, durationMs: number, error?: ErrorCode) => {
    // Never forward child stderr, task errors, paths or arbitrary event fields.
    process.stderr.write(`${JSON.stringify({ source: "mcp", operation, stage: "tool", durationMs, ...(error ? { code: error } : {}) })}\n`);
  };
  const lost = () => {
    const result = failure(new ForgeError(ErrorCode.UnknownOutcome, "Execution process ended. Sessions are lost; inspect any file or remote publication before retrying."));
    pending.forEach(request => { request.cleanup(); request.resolve(result); });
    pending.clear();
  };
  const send = (message: ParentMessage) => {
    if (!child.connected) { lost(); return; }
    child.send(message, error => { if (error) { lost(); void stop(1); } });
  };
  function stop(status = code): Promise<void> {
    code = status;
    return stopping ??= (async () => {
      if (!exited) {
        send({ kind: MessageKind.Shutdown });
        // This is a shutdown deadline, never a document-operation timeout.
        const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
        try { await exit; } finally { clearTimeout(timer); }
      }
      lost();
      await server.close();
      resolveDone();
    })();
  }
  child.on("message", (message: ChildMessage) => {
    if (message.kind === MessageKind.Ready) { readyResolve(); return; }
    if (message.kind === MessageKind.Diagnostic) {
      if (Object.values(Operation).includes(message.operation) && Number.isFinite(message.durationMs)) {
        log(message.operation, message.durationMs, Object.values(ErrorCode).includes(message.code!) ? message.code : undefined);
      }
      return;
    }
    if (message.kind !== MessageKind.Result) return;
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    request.cleanup();
    request.resolve(message.result);
  });
  child.once("error", () => {
    readyReject(new ForgeError(ErrorCode.Io, "Unable to start the MCP execution process."));
    exited = true; resolveExit(); lost(); void stop(1);
  });
  child.once("exit", () => {
    exited = true; resolveExit();
    readyReject(new ForgeError(ErrorCode.Io, "MCP execution process stopped during startup."));
    lost();
    // Give in-flight tool errors one turn to reach their MCP request handlers.
    if (!stopping) setImmediate(() => void stop(1));
  });
  server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools }));
  server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
    try {
      if (stopping) throw new ForgeError(ErrorCode.Disposed, "MCP server is shutting down.");
      const name = request.params.name;
      const operation = name.startsWith("react_forge_") ? name.slice("react_forge_".length) as Operation : undefined;
      if (!operation || !Object.values(Operation).includes(operation)) throw new ForgeError(ErrorCode.MalformedInput, "Unknown React Forge tool.");
      const input = parse(operation, request.params.arguments ?? {});
      if (extra.signal.aborted) throw new ForgeError(ErrorCode.Cancelled, "The operation was cancelled.");
      return await new Promise<CallToolResult>(resolve => {
        const id = ++nextId;
        const cancel = () => send({ kind: MessageKind.Cancel, id });
        pending.set(id, { resolve, cleanup: () => extra.signal.removeEventListener("abort", cancel) });
        extra.signal.addEventListener("abort", cancel, { once: true });
        send({ kind: MessageKind.Call, id, operation, input });
      });
    } catch (error) { return failure(error); }
  });
  const interrupt = () => void stop(130);
  const terminate = () => void stop(143);
  const end = () => void stop();
  process.on("SIGINT", interrupt);
  process.on("SIGTERM", terminate);
  if (process.platform === "win32") process.on("SIGBREAK", interrupt);
  process.stdin.once("end", end);
  server.onclose = end;
  server.onerror = () => log("protocol", 0, ErrorCode.MalformedInput);
  try {
    await ready;
    if (!stopping) await server.connect(transport);
    await done;
    return code;
  } finally {
    await stop();
    process.off("SIGINT", interrupt);
    process.off("SIGTERM", terminate);
    if (process.platform === "win32") process.off("SIGBREAK", interrupt);
    process.stdin.off("end", end);
  }
}
