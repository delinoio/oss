// Shared helpers for bundlewhy engine collector adapters.
//
// Adapters run inside the analyzed project's Node.js process with the user's
// permissions. They load the project's own bundler, run one production build
// into a bundlewhy-owned output directory and write a normalized collection
// file. They never write project source, configuration or lockfiles.
//
// Protocol: `node <adapter>.mjs <request.json>`. Progress and errors are JSON
// lines on stdout. The collection is written to `request.collectPath`.

import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const COLLECT_VERSION = 1;

/** Stable adapter error codes; keep aligned with the Rust ErrorCode enum. */
export const AdapterErrorCode = Object.freeze({
  BundlerNotInstalled: "BW_BUNDLER_NOT_INSTALLED",
  ConfigLoadFailed: "BW_CONFIG_LOAD_FAILED",
  ConfigAmbiguous: "BW_CONFIG_AMBIGUOUS",
  BuildFailed: "BW_BUILD_FAILED",
  CollectionFailed: "BW_COLLECTION_FAILED",
  EngineVersionUnsupported: "BW_ENGINE_VERSION_UNSUPPORTED",
  UnsupportedBuild: "BW_UNSUPPORTED_BUILD",
});

/** Prefix of protocol frames; project output on stdout never carries it. */
export const FRAME_PREFIX = "BUNDLEWHY-FRAME";
let protocolToken = "";

const isAdapterEntrypoint = () =>
  ["vite.mjs", "rspack.mjs"].includes(path.basename(process.argv[1] ?? ""));

export class AdapterError extends Error {
  constructor(code, message, recovery) {
    super(message);
    this.code = code;
    this.recovery = recovery;
  }
}

/**
 * Writes one protocol frame. Frames carry the per-run token from the request
 * so that JSON printed by project configuration or plugins cannot impersonate
 * adapter events.
 */
export function emit(event) {
  process.stdout.write(
    `${FRAME_PREFIX} ${protocolToken} ${JSON.stringify(event)}\n`,
  );
}

export function progress(stage, message) {
  emit({ event: "progress", stage, message });
}

export function readRequest() {
  const requestPath = process.argv[2];
  if (!requestPath) {
    throw new AdapterError(
      AdapterErrorCode.CollectionFailed,
      "Adapter request path argument is missing",
      "Run adapters only through `bundlewhy analyze`.",
    );
  }
  return JSON.parse(fs.readFileSync(requestPath, "utf8"));
}

/** Resolves a package from the analyzed project, never from bundlewhy itself. */
export function resolveProjectPackage(projectRoot, name) {
  const projectRequire = createRequire(path.join(projectRoot, "package.json"));
  let manifestPath;
  try {
    manifestPath = projectRequire.resolve(`${name}/package.json`);
  } catch {
    throw new AdapterError(
      AdapterErrorCode.BundlerNotInstalled,
      `${name} is not installed in the selected project`,
      `Install the project's dependencies so that ${name} resolves from the project root.`,
    );
  }
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  return {
    require: projectRequire,
    manifestPath,
    packageRoot: path.dirname(manifestPath),
    version: manifest.version,
  };
}

/**
 * Rejects engines outside the frozen matrix unless the user explicitly selected
 * analysis with an unsupported engine. Runs before any build starts.
 */
export function checkEngineVersion(request, packageName, version) {
  emit({ event: "engine", package: packageName, version });
  if (!request.supportedVersion || version === request.supportedVersion) return;
  if (request.allowUnsupportedEngine) return;
  throw new AdapterError(
    AdapterErrorCode.EngineVersionUnsupported,
    `${packageName} ${version} is not in the bundlewhy compatibility matrix (supported: ${request.supportedVersion})`,
    `Install ${packageName}@${request.supportedVersion}, or rerun with --allow-unsupported-engine to analyze with ${version} explicitly.`,
  );
}

export async function importFile(file) {
  return import(pathToFileURL(file).href);
}

const packageCache = new Map();

/** Returns the nearest package.json identity for a module file. */
export function nearestPackage(file) {
  if (!file || !path.isAbsolute(file)) return null;
  let dir = path.dirname(file);
  const visited = [];
  while (true) {
    if (packageCache.has(dir)) {
      const cached = packageCache.get(dir);
      for (const entry of visited) packageCache.set(entry, cached);
      return cached;
    }
    visited.push(dir);
    const manifestPath = path.join(dir, "package.json");
    let found = null;
    if (fs.existsSync(manifestPath)) {
      try {
        const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
        if (typeof manifest.name === "string") {
          found = {
            name: manifest.name,
            version:
              typeof manifest.version === "string" ? manifest.version : "",
            dir,
            sideEffects: manifest.sideEffects ?? null,
          };
        }
      } catch {
        found = null;
      }
    }
    const parent = path.dirname(dir);
    if (found || parent === dir) {
      for (const entry of visited) packageCache.set(entry, found);
      return found;
    }
    dir = parent;
  }
}

/** Strips loader prefixes and queries from a bundler module identifier. */
export function moduleFilePath(id) {
  if (typeof id !== "string" || id.startsWith("\0")) return null;
  const withoutLoaders = id.includes("!")
    ? id.slice(id.lastIndexOf("!") + 1)
    : id;
  const withoutQuery = withoutLoaders.split("?")[0];
  return path.isAbsolute(withoutQuery) ? withoutQuery : null;
}

export function writeCollection(request, collection) {
  fs.writeFileSync(
    request.collectPath,
    JSON.stringify({ collectVersion: COLLECT_VERSION, ...collection }),
  );
}

const ANSI_PATTERN = new RegExp(String.raw`\u001b\[[0-9;]*m`, "g");

export function stripAnsi(text) {
  return text.replace(ANSI_PATTERN, "");
}

export function summarizeValue(value) {
  if (value === undefined) return "undefined";
  if (value === null) return "null";
  if (typeof value === "function") return "[function]";
  if (typeof value === "object") {
    if (Array.isArray(value)) return `[array of ${value.length}]`;
    if (value.constructor && value.constructor !== Object) {
      return `[${value.constructor.name}]`;
    }
    return "[object]";
  }
  return String(value);
}

export async function runAdapter(
  main,
  { execute = isAdapterEntrypoint() } = {},
) {
  if (!execute) return;
  const started = Date.now();
  try {
    const request = readRequest();
    protocolToken = String(request.protocolToken ?? "");
    await main(request);
    emit({ event: "done", durationMs: Date.now() - started });
  } catch (error) {
    // Also report on stderr: before the request is read no token is known and
    // the frame below cannot be authenticated.
    process.stderr.write(
      `bundlewhy adapter error: ${String(error?.message ?? error).slice(0, 4000)}\n`,
    );
    const code =
      error instanceof AdapterError ? error.code : AdapterErrorCode.BuildFailed;
    const recovery =
      error instanceof AdapterError && error.recovery
        ? error.recovery
        : "Fix the build error above, then start a new run with `bundlewhy analyze`.";
    emit({
      event: "error",
      code,
      message: String(error?.message ?? error).slice(0, 4000),
      recovery,
    });
    process.exitCode = 1;
  }
}
