import { extname, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { tsImport } from "tsx/esm/api";
import { ForgeError, abortable } from "./errors.js";
import { ErrorCode, limits } from "./types.js";
import type { DocumentSession } from "./session.js";

const help = `React Forge 0.0.0

Usage:
  react-forge run <entry.tsx> --output <file> [--data <json>] [--overwrite] [--json]
  react-forge --help
  react-forge --version

The module's default task receives { data, signal } and returns a document session.
Output must have the session's format extension. Existing output requires --overwrite.
Imported source files cannot be overwritten; choose a separate output path.
Tasks execute trusted code with your permissions. No automatic timeout is applied.
`;

function write(stream: NodeJS.WriteStream, value: string): Promise<void> {
  return new Promise((resolve, reject) => stream.write(value, error => error ? reject(error) : resolve()));
}

export async function main(args: string[]): Promise<number> {
  const json = args.includes("--json");
  const controller = new AbortController();
  let cancelled = 0;
  const interrupt = () => { cancelled = 130; controller.abort(); };
  const terminate = () => { cancelled = 143; controller.abort(); };
  let session: DocumentSession | undefined;
  process.on("SIGINT", interrupt);
  process.on("SIGTERM", terminate);
  try {
    if (args.length === 1 && ["--help", "-h"].includes(args[0]!)) { await write(process.stdout, help); return 0; }
    if (args.length === 1 && args[0] === "--version") { await write(process.stdout, "0.0.0\n"); return 0; }
    if (args[0] !== "run" || !args[1] || args[1].startsWith("-") || extname(args[1]) !== ".tsx") {
      throw new ForgeError(ErrorCode.MalformedInput, "Expected run <entry.tsx>. Use --help for usage.");
    }
    const flags = new Map<string, string | boolean>();
    for (let at = 2; at < args.length; at++) {
      const flag = args[at]!;
      if (flags.has(flag)) throw new ForgeError(ErrorCode.MalformedInput, "Duplicate CLI option.");
      if (flag === "--json" || flag === "--overwrite") flags.set(flag, true);
      else if (flag === "--output" || flag === "--data") {
        const value = args[++at];
        if (!value || value.startsWith("--")) throw new ForgeError(ErrorCode.MalformedInput, "CLI option requires a value.");
        flags.set(flag, value);
      } else throw new ForgeError(ErrorCode.MalformedInput, "Unknown CLI option. Use --help for usage.");
    }
    const output = flags.get("--output");
    if (typeof output !== "string") throw new ForgeError(ErrorCode.MalformedInput, "--output is required.");
    let data: unknown;
    const rawData = flags.get("--data");
    if (typeof rawData === "string") {
      if (Buffer.byteLength(rawData) > limits.treeBytes) throw new ForgeError(ErrorCode.ResourceLimit, "Task data exceeds 16 MiB.");
      try { data = JSON.parse(rawData); } catch { throw new ForgeError(ErrorCode.MalformedInput, "--data must contain valid JSON."); }
    }
    const taskModule = await abortable(tsImport(pathToFileURL(resolve(args[1])).href, {
      parentURL: import.meta.url,
    }) as Promise<{ default?: unknown }>, controller.signal);
    // A TSX entry outside an ESM package is compiled as CommonJS by tsx;
    // Node exposes its TypeScript default export through the CJS namespace.
    const candidate = taskModule.default;
    const run = typeof candidate === "function" ? candidate
      : candidate && typeof candidate === "object" && "default" in candidate ? candidate.default : undefined;
    if (typeof run !== "function") throw new ForgeError(ErrorCode.MalformedInput, "TSX module must default-export a task function.");
    const task = Promise.resolve(run({ data, signal: controller.signal })) as Promise<DocumentSession>;
    void task.then(async result => { if (controller.signal.aborted && !session && typeof result?.dispose === "function") await result.dispose(); }).catch(() => {});
    const returned = await abortable(task, controller.signal);
    if (!returned || typeof returned.exportFile !== "function" || typeof returned.dispose !== "function") throw new ForgeError(ErrorCode.MalformedInput, "Task must return a document session.");
    session = returned;
    if (extname(output).toLowerCase() !== `.${session.format}`) throw new ForgeError(ErrorCode.MalformedInput, "Output extension must match the document session format.");
    const result = await session.exportFile(output, { overwrite: flags.has("--overwrite"), signal: controller.signal });
    await write(process.stdout, json ? `${JSON.stringify({ ok: true, format: session.format, ...result })}\n` : `Exported ${session.format.toUpperCase()} revision ${result.revision}.\n`);
    return 0;
  } catch (error) {
    const safe = error instanceof ForgeError ? error : new ForgeError(ErrorCode.Render, "Task execution failed. Correct the task and retry.");
    await write(json ? process.stdout : process.stderr, json ? `${JSON.stringify({ ok: false, error: safe.toJSON() })}\n` : `${safe.code}: ${safe.message}\n`);
    return cancelled || 1;
  } finally {
    await session?.dispose();
    process.off("SIGINT", interrupt);
    process.off("SIGTERM", terminate);
  }
}
