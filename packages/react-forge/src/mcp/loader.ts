import { registerHooks } from "node:module";
import { readFileSync } from "node:fs";
import { extname, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { transformSync } from "esbuild";
import { v7 } from "uuid";
import { ForgeError, checkSignal } from "../errors.js";
import { readSource } from "../files.js";
import { ErrorCode, limits } from "../types.js";
import type { Input, Operation } from "./contract.js";

/** One loader per execution process; dependencies retain one module identity. */
export class TaskLoader {
  private readonly sources = new Map<string, string>();
  private readonly hooks = registerHooks({
    resolve: (specifier, context, nextResolve) => {
      if (this.sources.has(specifier)) return { url: specifier, shortCircuit: true };
      // Caller projects may have their own React or no React Forge installation.
      // Pin only the engine's public imports; ordinary caller imports keep Node
      // resolution. A second engine would split React and Figma rate/file queues.
      if (/^(?:react(?:\/.*)?|react-reconciler(?:\/.*)?|@delino\/react-forge(?:\/(?:pptx|docx|xlsx|pdf|figma|sprite|sfx|glb|fbx))?)$/.test(specifier)) {
        return nextResolve(specifier, { ...context, parentURL: import.meta.url });
      }
      return nextResolve(specifier, context);
    },
    load: (url, context, nextLoad) => {
      // tsx supplies extension/tsconfig resolution. Compile typed dependencies
      // here too: delegating a temporary caller's .ts module to its CommonJS
      // package scope loses named exports across a synchronous virtual entry.
      const typed = url.startsWith("file:") && /\.(?:tsx?|mts|cts)$/.test(new URL(url).pathname);
      const source = this.sources.get(url) ?? (typed ? readFileSync(new URL(url), "utf8") : undefined);
      if (source === undefined) return nextLoad(url, context);
      try {
        const commonjs = new URL(url).pathname.endsWith(".cts");
        const compiled = transformSync(source, { loader: "tsx", format: commonjs ? "cjs" : "esm", target: "node24", jsx: "automatic", sourcefile: new URL(url).pathname, logLevel: "silent" });
        return { format: commonjs ? "commonjs" : "module", source: compiled.code, shortCircuit: true };
      } catch { throw new ForgeError(ErrorCode.MalformedInput, "Unable to compile the TSX entry. Correct its syntax and retry."); }
    },
  });

  constructor(private readonly cwd: string) {}

  async load(input: Input<Operation.Execute>, signal: AbortSignal): Promise<unknown> {
    const base = input.entry ? resolve(this.cwd, input.entry) : resolve(this.cwd, ".react-forge-inline.tsx");
    if (input.entry && extname(base) !== ".tsx") throw new ForgeError(ErrorCode.MalformedInput, "Task entries must have the .tsx extension.");
    const dataBytes = (input.data === undefined ? 0 : Buffer.byteLength(JSON.stringify(input.data)));
    const source = input.code ?? (await readSource({ path: base }, limits.treeBytes - dataBytes, signal)).bytes.toString("utf8");
    if (Buffer.byteLength(source) + dataBytes > limits.treeBytes) throw new ForgeError(ErrorCode.ResourceLimit, "TSX source and JSON data exceed 16 MiB.");
    checkSignal(signal);
    const url = pathToFileURL(base);
    url.searchParams.set("react-forge-call", v7());
    this.sources.set(url.href, source);
    try {
      const imported = await import(url.href) as { default?: unknown };
      checkSignal(signal);
      const candidate = imported.default;
      return typeof candidate === "function" ? candidate
        : candidate && typeof candidate === "object" && "default" in candidate ? candidate.default : undefined;
    } finally { this.sources.delete(url.href); }
  }

  dispose() { this.sources.clear(); this.hooks.deregister(); }
}
