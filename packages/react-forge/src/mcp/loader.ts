import { registerHooks } from "node:module";
import { readFileSync } from "node:fs";
import { extname, isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { transformSync } from "esbuild";
import { v7 } from "uuid";
import { ForgeError, checkSignal } from "../errors.js";
import { readSource } from "../files.js";
import { ErrorCode, limits } from "../types.js";
import type { Input, Operation } from "./contract.js";
import { TaskError, TaskPhase, TaskSource, taskMessage, type CompilerIssue, type TaskDetails, type TaskDiagnostic } from "./diagnostics.js";

const resolutionCodes = new Set(["ERR_MODULE_NOT_FOUND", "MODULE_NOT_FOUND", "ERR_PACKAGE_PATH_NOT_EXPORTED", "ERR_UNKNOWN_FILE_EXTENSION", "ERR_UNSUPPORTED_DIR_IMPORT"]);

class SafeResolutionError extends Error {
  constructor(specifier: string | undefined, readonly code: string) {
    super(`Unable to resolve import${specifier ? ` ${JSON.stringify(specifier.slice(0, 900))}` : ""}.`);
  }
}

function compilerIssues(error: unknown): CompilerIssue[] {
  if (!error || typeof error !== "object" || !("errors" in error) || !Array.isArray(error.errors)) return [];
  return error.errors.filter((item): item is CompilerIssue => !!item && typeof item === "object" && typeof item.text === "string");
}

function framePath(line: string): { path: string; line: number; column: number } | undefined {
  const text = line.trim();
  const match = text.match(/\((.+):(\d+):(\d+)\)$/) ?? text.match(/^at (.+):(\d+):(\d+)$/);
  if (!match) return undefined;
  try {
    const path = match[1]!.startsWith("file:") ? fileURLToPath(match[1]!) : match[1]!;
    return { path, line: Number(match[2]), column: Number(match[3]) };
  } catch { return undefined; }
}

/** One loader per execution process; dependencies retain one module identity. */
export class TaskLoader {
  private readonly sources = new Map<string, string>();
  private readonly known = new Map<string, { source: TaskSource; file?: string }>();
  private readonly hooks = registerHooks({
    resolve: (specifier, context, nextResolve) => {
      if (this.sources.has(specifier)) return { url: specifier, shortCircuit: true };
      try {
        // Caller projects may have their own React or no React Forge installation.
        // Pin only the engine's public imports; ordinary caller imports keep Node
        // resolution. A second engine would split React and Figma rate/file queues.
        if (/^(?:react(?:\/.*)?|react-reconciler(?:\/.*)?|@delino\/react-forge(?:\/(?:pptx|docx|xlsx|pdf|figma|sprite|sfx|glb|fbx))?)$/.test(specifier)) {
          return nextResolve(specifier, { ...context, parentURL: import.meta.url });
        }
        return nextResolve(specifier, context);
      } catch (error) {
        const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
        if (!resolutionCodes.has(code as string)) throw error;
        // Node's resolver embeds host paths in its messages. Keep only the
        // caller's relative or package specifier in MCP task diagnostics.
        const safe = !isAbsolute(specifier) && !specifier.startsWith("file:") && !/^[a-zA-Z]:[\\/]/.test(specifier)
          ? specifier : undefined;
        throw new SafeResolutionError(safe, code as string);
      }
    },
    load: (url, context, nextLoad) => {
      // Node 24 rejects an undefined CommonJS source from tsx's synchronous
      // hook (notably React's jsx-runtime). Load ordinary CJS files directly;
      // typed .cts files still take the transform path below. Remove this when
      // the Node/tsx hook combination returns a valid CJS source on every host.
      if (context.format === "commonjs" && url.startsWith("file:") && /\.c?js$/.test(new URL(url).pathname)) {
        return { format: "commonjs", source: readFileSync(new URL(url), "utf8"), shortCircuit: true };
      }
      // tsx supplies extension/tsconfig resolution. Compile typed dependencies
      // here too: delegating a temporary caller's .ts module to its CommonJS
      // package scope loses named exports across a synchronous virtual entry.
      const typed = url.startsWith("file:") && /\.(?:tsx?|mts|cts)$/.test(new URL(url).pathname);
      const source = this.sources.get(url) ?? (typed ? readFileSync(new URL(url), "utf8") : undefined);
      if (source === undefined) return nextLoad(url, context);
      const path = fileURLToPath(url);
      if (!this.known.has(path)) this.register(path, this.sources.has(url) ? TaskSource.Entry : TaskSource.Import);
      try {
        const commonjs = new URL(url).pathname.endsWith(".cts");
        const compiled = transformSync(source, { loader: "tsx", format: commonjs ? "cjs" : "esm", target: "node24", jsx: "automatic", sourcefile: path, sourcemap: "inline", logLevel: "silent" });
        return { format: commonjs ? "commonjs" : "module", source: compiled.code, shortCircuit: true };
      } catch (error) { throw new TaskError(ErrorCode.MalformedInput, TaskPhase.Compile, error, path, compilerIssues(error)); }
    },
  });

  constructor(private readonly cwd: string) {}

  private register(path: string, source: TaskSource): void {
    const file = relative(this.cwd, path);
    this.known.set(path, { source, ...(!file.startsWith(`..${sep}`) && file !== ".." && !isAbsolute(file) ? { file: file.split(sep).join("/") } : {}) });
  }

  private location(path: string | undefined, line?: number, column?: number): Partial<TaskDiagnostic> {
    const known = path ? this.known.get(path) : undefined;
    if (!known) return {};
    return {
      source: known.source,
      ...(known.file && known.source !== TaskSource.Inline ? { file: known.file } : {}),
      ...(Number.isInteger(line) && line! > 0 ? { line } : {}),
      ...(Number.isInteger(column) && column! >= 0 ? { column: column! + 1 } : {}),
    };
  }

  private stackLocation(error: unknown): Partial<TaskDiagnostic> {
    let stack: string;
    try {
      if (!(error instanceof Error) || typeof error.stack !== "string") return {};
      stack = error.stack;
    } catch { return {}; }
    for (const line of stack.split("\n").slice(1)) {
      const frame = framePath(line);
      if (frame && this.known.has(frame.path)) return this.location(frame.path, frame.line, frame.column - 1);
    }
    return {};
  }

  isCallerException(error: unknown): boolean {
    return typeof error === "string" || this.stackLocation(error).source !== undefined;
  }

  diagnose(error: unknown): TaskDetails | undefined {
    let phase: TaskPhase;
    let original: unknown;
    if (error instanceof TaskError) { phase = error.phase; original = error.cause; }
    else if (error instanceof ForgeError && error.code === ErrorCode.Render && error.cause !== undefined) {
      phase = TaskPhase.Render; original = error.cause;
    } else return undefined;
    const compiler = error instanceof TaskError ? error.compilerIssues : undefined;
    const originPath = error instanceof TaskError ? error.originPath : undefined;
    if (compiler?.length) {
      return { diagnostics: compiler.slice(0, 10).map(issue => ({
        phase, message: taskMessage(issue.text),
        ...this.location(issue.location?.file || originPath, issue.location?.line, issue.location?.column),
      })), ...(compiler.length > 10 ? { diagnosticsTruncated: true } : {}) };
    }
    return { diagnostics: [{ phase, message: taskMessage(original), ...this.stackLocation(original) }] };
  }

  async load(input: Input<Operation.Execute>, signal: AbortSignal): Promise<unknown> {
    const base = input.entry ? resolve(this.cwd, input.entry) : resolve(this.cwd, `.react-forge-inline-${v7()}.tsx`);
    if (input.entry && extname(base) !== ".tsx") throw new ForgeError(ErrorCode.MalformedInput, "Task entries must have the .tsx extension.");
    const dataBytes = (input.data === undefined ? 0 : Buffer.byteLength(JSON.stringify(input.data)));
    const source = input.code ?? (await readSource({ path: base }, limits.treeBytes - dataBytes, signal)).bytes.toString("utf8");
    if (Buffer.byteLength(source) + dataBytes > limits.treeBytes) throw new ForgeError(ErrorCode.ResourceLimit, "TSX source and JSON data exceed 16 MiB.");
    checkSignal(signal);
    const url = pathToFileURL(base);
    url.searchParams.set("react-forge-call", v7());
    this.sources.set(url.href, source);
    this.register(base, input.entry ? TaskSource.Entry : TaskSource.Inline);
    try {
      let imported: { default?: unknown };
      try { imported = await import(url.href) as { default?: unknown }; }
      catch (error) {
        if (error instanceof ForgeError) throw error;
        const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
        if (resolutionCodes.has(code as string)) {
          throw new TaskError(ErrorCode.MalformedInput, TaskPhase.Compile,
            error instanceof SafeResolutionError ? error : new SafeResolutionError(undefined, code as string));
        }
        if (this.isCallerException(error)) throw new TaskError(ErrorCode.Render, TaskPhase.Task, error);
        throw new ForgeError(ErrorCode.Render, "Task execution failed. Correct the task and retry.");
      }
      checkSignal(signal);
      const candidate = imported.default;
      return typeof candidate === "function" ? candidate
        : candidate && typeof candidate === "object" && "default" in candidate ? candidate.default : undefined;
    } finally { this.sources.delete(url.href); }
  }

  dispose() { this.sources.clear(); this.known.clear(); this.hooks.deregister(); }
}
