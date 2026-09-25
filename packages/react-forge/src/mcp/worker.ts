import { SessionRuntime } from "./runtime.js";
import { ErrorCode } from "../types.js";
import { MessageKind, failure, success, type ParentMessage, type ChildMessage } from "./contract.js";

const runtime = new SessionRuntime(process.cwd());
const controllers = new Map<number, AbortController>();
const pending = new Set<Promise<void>>();
let stopping: Promise<void> | undefined;
const send = (message: ChildMessage) => {
  if (process.connected) process.send?.(message, undefined, undefined, (error: Error | null) => { if (error) void stop(); });
};
function stop(): Promise<void> {
  return stopping ??= (async () => {
    controllers.forEach(controller => controller.abort());
    // Start disposal before waiting for operations, so unresolved React work
    // can be released. The parent owns the bounded shutdown grace period.
    await runtime.dispose();
    await Promise.allSettled(pending);
    process.exit(0);
  })();
}
process.on("message", (message: ParentMessage) => {
  if (message.kind === MessageKind.Shutdown) { void stop(); return; }
  if (message.kind === MessageKind.Cancel) { controllers.get(message.id)?.abort(); return; }
  if (message.kind !== MessageKind.Call || stopping) return;
  const controller = new AbortController();
  controllers.set(message.id, controller);
  const started = performance.now();
  const work = (async () => {
    let code: ErrorCode | undefined;
    try {
      const value = await runtime.call(message.operation, message.input, controller.signal);
      send({ kind: MessageKind.Result, id: message.id, result: success(value) });
    } catch (error) {
      const result = failure(error);
      code = (result.structuredContent?.error as { code: ErrorCode }).code;
      send({ kind: MessageKind.Result, id: message.id, result });
    } finally {
      controllers.delete(message.id);
      send({ kind: MessageKind.Diagnostic, operation: message.operation, durationMs: performance.now() - started, code });
    }
  })();
  pending.add(work);
  void work.finally(() => pending.delete(work)).catch(() => {});
});
process.on("disconnect", () => void stop());
process.on("SIGINT", () => void stop());
process.on("SIGTERM", () => void stop());
if (process.platform === "win32") process.on("SIGBREAK", () => void stop());
send({ kind: MessageKind.Ready });
