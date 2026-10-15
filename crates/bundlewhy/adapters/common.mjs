// Shared helpers for bundlewhy engine collector adapters.
//
// Adapters run inside the analyzed project's Node.js process with the user's
// permissions. They load the project's own bundler, run one production build
// into a bundlewhy-owned output directory and write a normalized collection
// file. They never write project source, configuration or lockfiles.
//
// Protocol: `node <adapter>.mjs <request.json>`. Progress and errors are JSON
// lines on stdout. The collection is written to `request.collectPath`.

import { spawn } from "node:child_process";
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
  // A repeat process reports only its failure: the parent adapter process has
  // already reported the engine, the progress stages and the end of the run.
  if (isRepeatProcess() && event.event !== "error") return;
  process.stdout.write(
    `${FRAME_PREFIX} ${protocolToken} ${JSON.stringify(event)}\n`,
  );
}

/**
 * Wait until the Rust supervisor has captured the files named by a protection
 * event before allowing the adapter to invoke user configuration hooks.
 */
export function waitForProtectionAck() {
  const expected = `BUNDLEWHY-PROTECTION-ACK ${protocolToken}`;
  return new Promise((resolve, reject) => {
    let buffer = "";
    const onData = (chunk) => {
      buffer += chunk;
      const newline = buffer.indexOf("\n");
      if (newline < 0) return;
      const line = buffer.slice(0, newline).replace(/\r$/, "");
      process.stdin.off("data", onData);
      process.stdin.off("end", onEnd);
      process.stdin.pause();
      if (line === expected) {
        resolve();
      } else {
        reject(
          new Error("The protection snapshot acknowledgement was invalid"),
        );
      }
    };
    const onEnd = () => {
      process.stdin.off("data", onData);
      reject(new Error("The protection snapshot acknowledgement was closed"));
    };
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", onData);
    process.stdin.once("end", onEnd);
    process.stdin.resume();
  });
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

/** Imports a module file by path. */
export function importFile(file) {
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
    // The repeat process already reported this failure in its own frame.
    if (error instanceof ReportedFailure) {
      process.exitCode = 1;
      return;
    }
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
    process.exitCode = isRepeatProcess() ? REPORTED_FAILURE_EXIT_CODE : 1;
  } finally {
    // The supervisor keeps the acknowledgement pipe open while the adapter
    // runs. Close our reader after the final frame so it cannot keep Node
    // alive once the build is complete.
    process.stdin.destroy();
  }
}

/**
 * Repeat process. A compiler can use up state that the project's
 * configuration created, and imported modules stay cached for the lifetime of
 * a Node.js process, so a failed build is never repeated in the same process.
 * The adapter starts itself again with `REPEAT_ARGUMENT` instead: the repeat
 * loads the configuration and everything it imports from scratch, builds once
 * without the export usage observer, writes the collection and exits.
 *
 * Protocol between the two processes:
 * - the parent writes one JSON line to the repeat's stdin and keeps the pipe
 *   open; the repeat ends when the pipe closes, so a parent that is killed
 *   (on every platform) never leaves the repeat behind;
 * - the repeat shares stdout and stderr with the parent, so project output
 *   and frames reach the same reader; it frames only an `error` event;
 * - the exit status says what happened: 0 for a written collection,
 *   `REPORTED_FAILURE_EXIT_CODE` for a failure already framed, anything else
 *   (a crash, a signal, no process at all) for a failure that nobody framed.
 */
export const REPEAT_ARGUMENT = "--repeat-without-observer";

/** Exit status of a repeat process that framed its own failure. */
export const REPORTED_FAILURE_EXIT_CODE = 64;

/** How the repeat process ended, as the parent sees it. */
export const RepeatOutcome = Object.freeze({
  Completed: "completed",
  Reported: "reported",
  Lost: "lost",
});

/** Thrown by the parent when the repeat process already framed the failure. */
export class ReportedFailure extends Error {}

export function isRepeatProcess() {
  return process.argv[3] === REPEAT_ARGUMENT;
}

/**
 * Runs the adapter again in a new process. `payload` is JSON-serializable and
 * reaches the repeat through `readRepeatPayload`.
 *
 * @param {string} adapterFile path of the adapter module to start
 * @param {unknown} payload
 * @returns {Promise<string>} one of `RepeatOutcome`
 */
export function runRepeatProcess(adapterFile, payload) {
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(
        process.execPath,
        [...process.execArgv, adapterFile, process.argv[2], REPEAT_ARGUMENT],
        {
          stdio: ["pipe", "inherit", "inherit"],
          cwd: process.cwd(),
          env: process.env,
          windowsHide: true,
        },
      );
    } catch {
      resolve(RepeatOutcome.Lost);
      return;
    }
    child.on("error", () => resolve(RepeatOutcome.Lost));
    child.on("close", (code) =>
      resolve(
        code === 0
          ? RepeatOutcome.Completed
          : code === REPORTED_FAILURE_EXIT_CODE
            ? RepeatOutcome.Reported
            : RepeatOutcome.Lost,
      ),
    );
    // A child that exits before it reads its input is reported by its status.
    child.stdin.on("error", () => {});
    child.stdin.write(`${JSON.stringify(payload)}\n`);
  });
}

/**
 * Reads the parent's payload in a repeat process, then keeps watching stdin:
 * when the parent goes away the repeat process ends too.
 */
export function readRepeatPayload() {
  return new Promise((resolve, reject) => {
    const stdin = process.stdin;
    let buffered = "";
    let received = false;
    stdin.setEncoding("utf8");
    stdin.on("data", (chunk) => {
      if (received) return;
      buffered += chunk;
      const end = buffered.indexOf("\n");
      if (end < 0) return;
      received = true;
      // Watching must not keep the process alive once the work is done.
      stdin.unref?.();
      try {
        resolve(JSON.parse(buffered.slice(0, end)));
      } catch {
        reject(
          new AdapterError(
            AdapterErrorCode.CollectionFailed,
            "The repeat build process got an unreadable request from its parent",
            "Start a new run with `bundlewhy analyze`.",
          ),
        );
      }
    });
    const parentGone = () => {
      if (received) process.exit(1);
      reject(
        new AdapterError(
          AdapterErrorCode.CollectionFailed,
          "The repeat build process got no request from its parent",
          "Start a new run with `bundlewhy analyze`.",
        ),
      );
    };
    stdin.on("end", parentGone);
    stdin.on("error", parentGone);
  });
}

/** Largest export usage edge list an adapter stores in one collection. */
export const MAX_EXPORT_USAGE_EDGES = 100_000;

/**
 * Stable reason codes for export usage that could not be collected. The
 * collection stores only these codes; the analysis maps each to fixed text.
 * No error message, source text or project path is ever stored as a reason.
 */
export const ExportUsageReason = Object.freeze({
  PluginApiMissing: "plugin-api-missing",
  PluginAlreadyConfigured: "plugin-already-configured",
  PluginsNotArray: "plugins-not-array",
  PluginConstructionFailed: "plugin-construction-failed",
  PluginApplyFailed: "plugin-apply-failed",
  HookRegistrationFailed: "hook-registration-failed",
  BuildFailedWithObserver: "build-failed-with-observer",
  GraphNotDelivered: "graph-not-delivered",
  ModulesPayloadInvalid: "modules-payload-invalid",
  EdgesPayloadInvalid: "edges-payload-invalid",
  EdgeLimitExceeded: "edge-limit-exceeded",
});

/** What a skipped edge says about its provider export. */
export const SkippedProviderScope = Object.freeze({
  Named: "named",
  Module: "module",
  Unknown: "unknown",
});

/**
 * Decodes Rspack's positional export usage tuples into collection edges.
 *
 * Each tuple is `[consumerKey, consumerExport, providerKey, providerExport,
 * dependencyId, location]`. Module keys are numeric and only meaningful
 * inside the payload that carried them, so `identifiers` maps them to bundler
 * module identifiers. A `null` export path means module or namespace scope.
 * Edges that cannot be decoded, have an empty or non-string export path
 * segment, or whose modules are not collected modules (for example a
 * concatenated container) are skipped and counted, never guessed. Every
 * skipped edge is also attributed so that a chain end is never reported as a
 * start when an earlier edge was dropped:
 *
 * - its provider is a collected module: counted in `skippedProviders` for
 *   that module and for the provider export when it can be read (`named`
 *   path, `module` scope for a `null` path), else with scope `unknown`: an
 *   earlier edge into that export exists but cannot be shown, and an
 *   `unknown` export may be any export of the module;
 * - its provider is a known module that is not collected (for example a
 *   concatenated container): counted only in `skippedEdges`, because no
 *   chain through a collected module can end at it;
 * - its provider cannot be determined (malformed tuple or unknown module
 *   key): counted in `unattributedEdges`, which may hide an earlier edge
 *   anywhere.
 *
 * The result never contains source text, and an unavailable result carries
 * only an `ExportUsageReason` code.
 *
 * @param {unknown} rawEdges payload field `exportUsageEdges`
 * @param {Map<number, string>} identifiers module key to bundler identifier
 * @param {Set<string>} collectedIds identifiers of collected modules
 */
export function decodeExportUsageEdges(rawEdges, identifiers, collectedIds) {
  if (!Array.isArray(rawEdges)) {
    return unavailableExportUsage(ExportUsageReason.EdgesPayloadInvalid);
  }
  const exportPath = (value) => {
    if (value === null) return { ok: true, value: null };
    if (
      Array.isArray(value) &&
      value.length > 0 &&
      value.every((part) => typeof part === "string" && part.length > 0)
    ) {
      return { ok: true, value: [...value] };
    }
    return { ok: false };
  };
  const edges = [];
  const seen = new Set();
  let skippedEdges = 0;
  let unattributedEdges = 0;
  const skippedByProvider = new Map();
  const skip = (raw) => {
    skippedEdges += 1;
    const providerModule = Array.isArray(raw)
      ? identifiers.get(raw[2])
      : undefined;
    if (!providerModule) {
      unattributedEdges += 1;
      return;
    }
    if (!collectedIds.has(providerModule)) return;
    // The provider export is kept only when it can be read: a tuple that is
    // too short or has an invalid path names no export.
    const provider =
      Array.isArray(raw) && raw.length > 3 ? exportPath(raw[3]) : { ok: false };
    const entry = !provider.ok
      ? {
          module: providerModule,
          scope: SkippedProviderScope.Unknown,
          export: [],
        }
      : provider.value === null
        ? {
            module: providerModule,
            scope: SkippedProviderScope.Module,
            export: [],
          }
        : {
            module: providerModule,
            scope: SkippedProviderScope.Named,
            export: provider.value,
          };
    const key = JSON.stringify(entry);
    const known = skippedByProvider.get(key);
    if (known) known.edges += 1;
    else skippedByProvider.set(key, { ...entry, edges: 1 });
  };
  for (const raw of rawEdges) {
    if (!Array.isArray(raw) || raw.length < 6) {
      skip(raw);
      continue;
    }
    const [consumerKey, consumerExport, providerKey, providerExport] = raw;
    const consumerModule = identifiers.get(consumerKey);
    const providerModule = identifiers.get(providerKey);
    const consumer = exportPath(consumerExport);
    const provider = exportPath(providerExport);
    if (
      !consumerModule ||
      !providerModule ||
      !collectedIds.has(consumerModule) ||
      !collectedIds.has(providerModule) ||
      !consumer.ok ||
      !provider.ok
    ) {
      skip(raw);
      continue;
    }
    const edge = {
      consumer: { module: consumerModule, export: consumer.value },
      provider: { module: providerModule, export: provider.value },
      dependencyId: typeof raw[4] === "string" ? raw[4] : null,
      location: typeof raw[5] === "string" ? raw[5] : null,
    };
    const key = JSON.stringify(edge);
    if (seen.has(key)) continue;
    seen.add(key);
    edges.push(edge);
    // Stop at the first edge beyond the limit: the decoder never holds more
    // than the limit plus one distinct edge, whatever the payload size.
    if (edges.length > MAX_EXPORT_USAGE_EDGES) {
      return unavailableExportUsage(ExportUsageReason.EdgeLimitExceeded);
    }
  }
  edges.sort((left, right) => {
    const a = JSON.stringify(left);
    const b = JSON.stringify(right);
    return a < b ? -1 : a > b ? 1 : 0;
  });
  const skippedProviders = [...skippedByProvider]
    .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0))
    .map(([, entry]) => entry);
  return {
    available: true,
    reason: null,
    skippedEdges,
    skippedProviders,
    unattributedEdges,
    edges,
  };
}

/**
 * Collection value for export usage that could not be collected.
 *
 * @param {string} reason one of `ExportUsageReason`
 */
export function unavailableExportUsage(reason) {
  if (!Object.values(ExportUsageReason).includes(reason)) {
    throw new TypeError(
      "export usage reason must be an ExportUsageReason code",
    );
  }
  return {
    available: false,
    reason,
    skippedEdges: 0,
    skippedProviders: [],
    unattributedEdges: 0,
    edges: [],
  };
}
