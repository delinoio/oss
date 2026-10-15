// bundlewhy collector adapter for Rspack (SwcJsMinimizerRspackPlugin).
//
// Loads the project's Rspack configuration, runs one production build with the
// project's own @rspack/core into a bundlewhy-owned directory and normalizes
// stats (reasons, used/provided exports, optimization bailouts and chunks).

import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  AdapterError,
  AdapterErrorCode,
  checkEngineVersion,
  decodeExportUsageEdges,
  ExportUsageReason,
  importFile,
  isRepeatProcess,
  moduleFilePath,
  nearestPackage,
  progress,
  readRepeatPayload,
  RepeatOutcome,
  ReportedFailure,
  resolveProjectPackage,
  runAdapter,
  runRepeatProcess,
  stripAnsi,
  summarizeValue,
  unavailableExportUsage,
  writeCollection,
} from "./common.mjs";

export const ADAPTER_ID = "bundlewhy-collector-rspack";
export const ADAPTER_VERSION = "2";

const STATS_OPTIONS = Object.freeze({
  all: false,
  modules: true,
  nestedModules: true,
  orphanModules: true,
  reasons: true,
  optimizationBailout: true,
  usedExports: true,
  providedExports: true,
  chunks: true,
  chunkRelations: true,
  assets: true,
  ids: true,
  depth: true,
  entrypoints: true,
  issuer: true,
  errors: true,
  warnings: true,
  cachedModules: true,
});

async function loadConfig(request, rspack) {
  if (!request.configFile) {
    throw new AdapterError(
      AdapterErrorCode.ConfigLoadFailed,
      "No Rspack configuration file was selected",
      "Pass --config <rspack.config.*> to bundlewhy analyze.",
    );
  }
  let loaded;
  try {
    // .ts configurations rely on Node.js type stripping (erasable syntax only).
    loaded = (await importFile(request.configFile)).default;
  } catch (error) {
    throw new AdapterError(
      AdapterErrorCode.ConfigLoadFailed,
      `Could not load ${path.basename(request.configFile)}: ${error?.message ?? error}`,
      "Confirm the configuration loads with `node` directly. TypeScript configurations must use erasable syntax.",
    );
  }
  // A configuration factory that throws or rejects is a configuration
  // problem, not a build failure: no compiler has started yet.
  try {
    if (typeof loaded === "function") {
      loaded = await loaded(
        { production: true, RSPACK_BUILD: true },
        { mode: "production" },
      );
    }
    loaded = await loaded;
  } catch (error) {
    throw new AdapterError(
      AdapterErrorCode.ConfigLoadFailed,
      `The configuration function in ${path.basename(request.configFile)} failed: ${error?.message ?? error}`,
      "Fix the error thrown by the configuration function, then start a new run.",
    );
  }
  if (Array.isArray(loaded)) {
    const names = loaded.map((entry, index) => entry?.name ?? `#${index}`);
    if (request.configName) {
      const selected = loaded.find(
        (entry, index) => (entry?.name ?? `#${index}`) === request.configName,
      );
      if (!selected) {
        throw new AdapterError(
          AdapterErrorCode.ConfigAmbiguous,
          `No configuration named ${request.configName}; available: ${names.join(", ")}`,
          "Pass one of the listed names with --config-name.",
        );
      }
      return selected;
    }
    if (loaded.length !== 1) {
      throw new AdapterError(
        AdapterErrorCode.ConfigAmbiguous,
        `The configuration exports ${loaded.length} builds: ${names.join(", ")}`,
        "Select one build with --config-name <name>.",
      );
    }
    return loaded[0];
  }
  if (!loaded || typeof loaded !== "object") {
    throw new AdapterError(
      AdapterErrorCode.ConfigLoadFailed,
      "The configuration did not export an object, array or function",
      "Export a Rspack configuration object from the selected file.",
    );
  }
  void rspack;
  return loaded;
}

function describeMinimizer(rspack, config) {
  const optimization = config.optimization ?? {};
  const minimizers = optimization.minimizer;
  // An empty minimizer list runs no minifier even when minimize is true.
  const minifyEnabled =
    optimization.minimize !== false &&
    !(Array.isArray(minimizers) && minimizers.length === 0);
  if (minimizers === undefined) {
    return { minifyEnabled, defaultMinifier: true, summary: "default" };
  }
  const names = minimizers.map((entry) =>
    entry === "..."
      ? "..."
      : (entry?.constructor?.name ?? summarizeValue(entry)),
  );
  // Every effective JavaScript minimizer must be the default SWC one: "..."
  // expands to the defaults, and a CSS-only LightningCSS minimizer does not
  // transform JavaScript. Any other entry also rewrites the generated code.
  // An explicit SWC instance only counts as default when it keeps
  // compression and mangling enabled; detectors assume both run.
  const swcDefault = (entry) => {
    const options = entry?._args?.[0]?.minimizerOptions ?? {};
    const { compress, mangle } = options;
    if (compress === false || mangle === false) return false;
    // Per-pass compression options (for example { dead_code: false }) and
    // keep-name mangling options also disable what the detectors assume.
    if (
      compress &&
      typeof compress === "object" &&
      Object.values(compress).some((value) => value === false)
    ) {
      return false;
    }
    if (
      mangle &&
      typeof mangle === "object" &&
      Object.entries(mangle).some(
        ([key, value]) => key.startsWith("keep_") && value,
      )
    ) {
      return false;
    }
    return true;
  };
  const isDefault = (entry) =>
    entry === "..." ||
    (entry instanceof rspack.SwcJsMinimizerRspackPlugin && swcDefault(entry)) ||
    entry instanceof rspack.LightningCssMinimizerRspackPlugin;
  const defaultMinifier =
    minimizers.every(isDefault) &&
    minimizers.some(
      (entry) =>
        entry === "..." || entry instanceof rspack.SwcJsMinimizerRspackPlugin,
    );
  return { minifyEnabled, defaultMinifier, summary: names.join(", ") };
}

/**
 * Library builds configured globally (`output.library`) or per entry
 * (`entry: { main: { import, library } }`) are outside bundlewhy's scope.
 */
function libraryBuild(config) {
  if (config.output?.library) return true;
  const entry = config.entry;
  if (entry && typeof entry === "object" && !Array.isArray(entry)) {
    return Object.values(entry).some(
      (description) =>
        description &&
        typeof description === "object" &&
        !Array.isArray(description) &&
        Boolean(description.library),
    );
  }
  return false;
}

function normalizeStats(stats) {
  const chunkFiles = new Map();
  const chunks = [];
  for (const chunk of stats.chunks ?? []) {
    const files = (chunk.files ?? []).filter((file) =>
      /\.(c|m)?js$/.test(file),
    );
    chunkFiles.set(chunk.id, files);
    for (const file of files) {
      chunks.push({
        file,
        isEntry: Boolean(chunk.entry),
        isDynamicEntry: !chunk.initial,
        facadeModuleId: null,
        modules: [],
        map: `${file}.map`,
      });
    }
  }
  const chunkByFile = new Map(chunks.map((chunk) => [chunk.file, chunk]));
  const modules = new Map();

  const record = (raw, concatenatedInto, inheritedChunks) => {
    if (!raw.identifier || raw.moduleType === "runtime") return;
    const file = raw.nameForCondition ?? moduleFilePath(raw.identifier);
    const existing = modules.get(raw.identifier);
    if (existing && (existing.concatenatedInto || !concatenatedInto)) return;
    const pkg = file ? nearestPackage(file) : null;
    const chunkIds = raw.chunks?.length ? raw.chunks : inheritedChunks;
    const files = chunkIds.flatMap((id) => chunkFiles.get(id) ?? []);
    const reasons = (raw.reasons ?? []).map((reason) => ({
      type: reason.type ?? null,
      from: reason.resolvedModuleIdentifier ?? reason.moduleIdentifier ?? null,
      loc: reason.loc ?? null,
      explanation: reason.explanation ?? null,
      active: reason.active !== false,
    }));
    const importers = [
      ...new Set(
        reasons
          .filter(
            (reason) =>
              reason.from && reason.from !== raw.identifier && reason.active,
          )
          // Every import() reason (eager, weak, context element, ...) is a
          // dynamic edge and never part of a static importer chain.
          .filter((reason) => !reason.type?.startsWith("import()"))
          .map((reason) => reason.from),
      ),
    ];
    const dynamicImporters = [
      ...new Set(
        reasons
          .filter(
            (reason) => reason.from && reason.type?.startsWith("import()"),
          )
          .map((reason) => reason.from),
      ),
    ];
    modules.set(raw.identifier, {
      id: raw.identifier,
      name: raw.name ?? null,
      path: file && path.isAbsolute(file) ? file : null,
      virtual: !(file && path.isAbsolute(file)),
      isEntry:
        raw.depth === 0 || reasons.some((reason) => reason.type === "entry"),
      importers,
      dynamicImporters,
      importedIds: [],
      dynamicImportedIds: [],
      exports: Array.isArray(raw.providedExports) ? raw.providedExports : null,
      usedExports: Array.isArray(raw.usedExports)
        ? raw.usedExports
        : raw.usedExports === true
          ? ["*"]
          : null,
      moduleSideEffects: null,
      inputFormat:
        raw.moduleType === "javascript/esm"
          ? "es"
          : (raw.moduleType ?? "unknown"),
      moduleType: raw.moduleType ?? null,
      renderedLength: raw.size ?? 0,
      included: files.length > 0,
      chunks: files,
      bailouts: (raw.optimizationBailout ?? []).map((entry) =>
        stripAnsi(String(entry)),
      ),
      reasons,
      depth: raw.depth ?? null,
      concatenatedInto: concatenatedInto ?? null,
      packageName: pkg?.name ?? null,
      packageVersion: pkg?.version ?? null,
      packageDir: pkg?.dir ?? null,
      packageSideEffects:
        pkg && pkg.sideEffects !== null
          ? summarizeValue(pkg.sideEffects)
          : null,
    });
    for (const fileName of files)
      chunkByFile.get(fileName)?.modules.push(raw.identifier);
  };

  for (const raw of stats.modules ?? []) {
    if (Array.isArray(raw.modules) && raw.modules.length > 0) {
      for (const nested of raw.modules)
        record(nested, raw.identifier, raw.chunks ?? []);
    } else {
      record(raw, null, []);
    }
  }
  // Keep chunk module lists unique when a module was revisited.
  for (const chunk of chunks) chunk.modules = [...new Set(chunk.modules)];
  return {
    modules: [...modules.values()],
    chunks,
    assets: (stats.assets ?? []).map((asset) => asset.name),
    warnings: (stats.warnings ?? []).map((entry) =>
      stripAnsi(String(entry.message ?? entry)),
    ),
    errors: (stats.errors ?? []).map((entry) =>
      stripAnsi(String(entry.message ?? entry)),
    ),
  };
}

const EXPORT_USAGE_TAP = "bundlewhy-export-usage";

/**
 * Prepares the observation-only RsdoctorPlugin that reports Rspack's export
 * usage graph. The plugin is appended to the diagnostic build's plugin list;
 * the project's engine and its other plugins stay as configured. Every access
 * to the internal plugin API is guarded: when the API, the hook or the graph
 * is unavailable, the collection records a fixed reason code and the build
 * continues. Error messages from the plugin are never copied anywhere.
 */
function prepareExportUsage(rspackModule, plugins) {
  const state = { graph: null, unavailable: null };
  // Only a list can take an extra plugin. Any other value (null, a single
  // plugin instance, a function, ...) is passed to Rspack exactly as the
  // project wrote it, so the diagnostic build keeps the project's semantics
  // instead of silently replacing the value with the observer.
  if (plugins !== undefined && !Array.isArray(plugins)) {
    state.unavailable = ExportUsageReason.PluginsNotArray;
    return { state, plugins };
  }
  const projectPlugins = plugins ?? [];
  const RsdoctorPlugin = rspackModule.experiments?.RsdoctorPlugin;
  if (
    typeof RsdoctorPlugin !== "function" ||
    typeof RsdoctorPlugin.getCompilationHooks !== "function"
  ) {
    state.unavailable = ExportUsageReason.PluginApiMissing;
    return { state, plugins };
  }
  if (projectPlugins.some((entry) => entry instanceof RsdoctorPlugin)) {
    state.unavailable = ExportUsageReason.PluginAlreadyConfigured;
    return { state, plugins };
  }
  let observer;
  try {
    observer = new RsdoctorPlugin({
      moduleGraphFeatures: ["graph"],
      chunkGraphFeatures: false,
      exportUsageGraph: true,
    });
  } catch {
    state.unavailable = ExportUsageReason.PluginConstructionFailed;
    return { state, plugins };
  }
  // The first failure wins; later ones never replace the recorded reason.
  const fail = (reason) => {
    state.unavailable ??= reason;
  };
  // Rspack applies the plugin while it creates the compiler, so an exception
  // here would otherwise abort the build and lose every diagnostic.
  const guardedObserver = {
    apply(compiler) {
      try {
        observer.apply(compiler);
      } catch {
        fail(ExportUsageReason.PluginApplyFailed);
      }
    },
  };
  const collector = {
    apply(compiler) {
      try {
        // thisCompilation is not fired for child compilations, so the
        // payload always describes the main build.
        compiler.hooks.thisCompilation.tap(EXPORT_USAGE_TAP, (compilation) => {
          try {
            const hooks = RsdoctorPlugin.getCompilationHooks(compilation);
            hooks.moduleGraph.tapPromise(EXPORT_USAGE_TAP, async (graph) => {
              state.graph = graph;
            });
          } catch {
            fail(ExportUsageReason.HookRegistrationFailed);
          }
        });
      } catch {
        fail(ExportUsageReason.HookRegistrationFailed);
      }
    },
  };
  return { state, plugins: [...projectPlugins, guardedObserver, collector] };
}

/** Builds the collection's export usage record from the captured payload. */
function collectExportUsage(state, normalized) {
  if (state.unavailable) return unavailableExportUsage(state.unavailable);
  const graph = state.graph;
  if (!graph || typeof graph !== "object") {
    return unavailableExportUsage(ExportUsageReason.GraphNotDelivered);
  }
  if (!Array.isArray(graph.modules)) {
    return unavailableExportUsage(ExportUsageReason.ModulesPayloadInvalid);
  }
  const identifiers = new Map();
  for (const entry of graph.modules) {
    if (
      entry &&
      typeof entry.ukey === "number" &&
      typeof entry.identifier === "string"
    ) {
      identifiers.set(entry.ukey, entry.identifier);
    }
  }
  const collectedIds = new Set(normalized.modules.map((module) => module.id));
  return decodeExportUsageEdges(
    graph.exportUsageEdges,
    identifiers,
    collectedIds,
  );
}

/**
 * True when a value survives a JSON round trip unchanged: only strings,
 * finite numbers, booleans, null, lists and plain objects.
 */
function plainJson(value, depth = 0) {
  if (depth > 32) return false;
  if (value === null) return true;
  switch (typeof value) {
    case "string":
    case "boolean":
      return true;
    case "number":
      return Number.isFinite(value);
    case "object": {
      if (Array.isArray(value)) {
        return value.every((entry) => plainJson(entry, depth + 1));
      }
      const prototype = Object.getPrototypeOf(value);
      return (
        (prototype === Object.prototype || prototype === null) &&
        Object.values(value).every((entry) => plainJson(entry, depth + 1))
      );
    }
    default:
      return false;
  }
}

/**
 * What the repeat process receives: the result of the entry function when the
 * first attempt called one and the result can be passed on as JSON.
 */
function repeatPayloadFor(resolvedEntry) {
  return resolvedEntry && plainJson(resolvedEntry.value)
    ? { entry: resolvedEntry.value }
    : {};
}

function runCompiler(rspack, config) {
  return new Promise((resolve, reject) => {
    let compiler;
    try {
      compiler = rspack(config);
    } catch (error) {
      reject(error);
      return;
    }
    // The resolved platform reflects defaults and Browserslist queries that
    // the literal `target` value cannot show.
    const platform = { ...(compiler.platform ?? {}) };
    compiler.run((error, stats) => {
      compiler.close(() => {
        if (error) reject(error);
        else resolve({ stats, platform });
      });
    });
  });
}

await runAdapter(async (request) => {
  progress("detecting", "Loading the project's Rspack installation");
  const rspackPackage = resolveProjectPackage(
    request.projectRoot,
    "@rspack/core",
  );
  checkEngineVersion(request, "@rspack/core", rspackPackage.version);
  const rspackModule = rspackPackage.require("@rspack/core");
  const rspack = rspackModule.rspack ?? rspackModule;
  const version = rspackModule.rspackVersion ?? rspackPackage.version;

  // A failed observer build is repeated in a new process (see
  // `runRepeatProcess`). That process runs this same code with
  // `repeatProcess` set: it builds once, without the observer.
  const repeatProcess = isRepeatProcess();
  const repeatPayload = repeatProcess ? await readRepeatPayload() : null;
  const handedEntry =
    repeatPayload !== null &&
    typeof repeatPayload === "object" &&
    Object.hasOwn(repeatPayload, "entry");

  // Everything that turns the loaded configuration into the diagnostic
  // build's configuration lives in one function.
  const prepareBuild = async () => {
    const config = await loadConfig(request, rspackModule);
    const overrides = [
      "mode=production",
      `output.path=${path.basename(request.buildDir)} (bundlewhy run directory)`,
      "output.clean=true",
      "devtool=source-map",
      "watch=false",
    ];
    // Resolve an entry function once and build with its result, so the
    // library check sees exactly what the compiler builds. The repeat
    // process receives the first result instead of calling the function
    // again; when that result cannot be passed on (see `plainJson`), the
    // repeat process calls the function itself.
    let resolvedEntry = null;
    if (handedEntry) {
      config.entry = repeatPayload.entry;
      overrides.push("entry function resolved once by bundlewhy");
      resolvedEntry = { value: config.entry };
    } else if (typeof config.entry === "function") {
      try {
        config.entry = await config.entry();
      } catch (error) {
        throw new AdapterError(
          AdapterErrorCode.ConfigLoadFailed,
          `The entry function failed: ${error?.message ?? error}`,
          "Fix the error thrown by the entry function, then start a new run.",
        );
      }
      overrides.push(
        repeatProcess
          ? "entry function called again by the repeat build (its first result could not be passed on)"
          : "entry function resolved once by bundlewhy",
      );
      resolvedEntry = { value: config.entry };
    }
    const diagnosticConfig = {
      ...config,
      mode: "production",
      context: config.context ?? request.projectRoot,
      devtool: "source-map",
      watch: false,
      output: { ...(config.output ?? {}), path: request.buildDir, clean: true },
    };
    return { config, overrides, diagnosticConfig, resolvedEntry };
  };

  // Everything that describes the configuration the collected build ran with
  // is derived from that configuration, so a repeat build reports its own
  // values and never the first attempt's.
  const describeConfig = (config) => {
    const rawTarget = config.target ?? "web";
    // Inspect every raw target entry; the summary below is for display only.
    const targets = (Array.isArray(rawTarget) ? rawTarget : [rawTarget]).map(
      (entry) => (typeof entry === "string" ? entry : ""),
    );
    return {
      minimizer: describeMinimizer(rspackModule, config),
      nodeTarget: targets.some((entry) =>
        /(^|[^a-z])(node|async-node|electron-main|electron-preload|nwjs|node-webkit)/.test(
          entry,
        ),
      ),
      target: Array.isArray(rawTarget)
        ? rawTarget.map((entry) => summarizeValue(entry)).join(", ")
        : summarizeValue(rawTarget),
    };
  };

  const prepared = await prepareBuild();
  const { config, overrides, diagnosticConfig } = prepared;
  const { minimizer, nodeTarget, target } = describeConfig(config);
  // The value that the build receives for `plugins`, read once so that a
  // getter cannot answer differently later.
  const projectPlugins = diagnosticConfig.plugins;
  const exportUsage = repeatProcess
    ? {
        state: {
          graph: null,
          unavailable: ExportUsageReason.BuildFailedWithObserver,
        },
        plugins: projectPlugins,
      }
    : prepareExportUsage(rspackModule, projectPlugins);
  const observerAttached = exportUsage.state.unavailable === null;
  if (observerAttached) {
    diagnosticConfig.plugins = exportUsage.plugins;
  }

  progress("building", `Running the Rspack ${version} production build`);
  let stats;
  let platform = {};
  if (observerAttached) {
    // The observer is an internal API that can drift between Rspack
    // releases. Any failure of this build might come from it, so the build
    // is repeated once without it; export usage is then unavailable and
    // the project's own build failure, if any, is reported by the repeat.
    let observed = false;
    try {
      ({ stats, platform } = await runCompiler(rspack, diagnosticConfig));
      observed = !stats.hasErrors();
    } catch {
      observed = false;
    }
    if (!observed) {
      progress(
        "building",
        "Repeating the production build without the export usage observer",
      );
      // The failed build may have used up single-use project state (a plugin
      // instance that rejects a second compiler, for example), and modules
      // that the configuration imports stay cached in this process. The
      // repeat therefore runs in a new process that loads the configuration
      // from scratch; its result, including a configuration that cannot be
      // loaded again or a build that fails, is the project's result.
      const outcome = await runRepeatProcess(
        fileURLToPath(import.meta.url),
        repeatPayloadFor(prepared.resolvedEntry),
      );
      if (outcome === RepeatOutcome.Completed) return;
      if (outcome === RepeatOutcome.Reported) throw new ReportedFailure();
      throw new AdapterError(
        AdapterErrorCode.BuildFailed,
        "The repeat build process ended without a result",
        "Confirm the Rspack build succeeds in the project, then start a new run.",
      );
    }
    overrides.push(
      "plugins += RsdoctorPlugin (observation-only export usage graph; the project's plugins are unchanged)",
    );
  } else {
    try {
      ({ stats, platform } = await runCompiler(rspack, diagnosticConfig));
    } catch (error) {
      throw new AdapterError(
        AdapterErrorCode.BuildFailed,
        `rspack build failed: ${error?.message ?? error}`,
        "Fix the build error, confirm the Rspack build succeeds in the project, then start a new run.",
      );
    }
  }

  progress("collecting", "Normalizing Rspack stats");
  const json = stats.toJson(STATS_OPTIONS);
  const normalized = normalizeStats(json);
  const exportUsageRecord = collectExportUsage(exportUsage.state, normalized);
  progress(
    "collecting",
    exportUsageRecord.available
      ? `Collected ${exportUsageRecord.edges.length} export usage edge(s)`
      : "Export usage edges are unavailable for this build",
  );
  if (stats.hasErrors()) {
    throw new AdapterError(
      AdapterErrorCode.BuildFailed,
      `rspack build reported errors: ${normalized.errors.slice(0, 3).join("\n")}`,
      "Fix the build errors, then start a new run.",
    );
  }

  writeCollection(request, {
    bundler: "rspack",
    bundlerPackage: "@rspack/core",
    bundlerVersion: version,
    minifier: minimizer.defaultMinifier
      ? "SwcJsMinimizerRspackPlugin"
      : minimizer.summary,
    minifierVersion: minimizer.defaultMinifier ? version : "unknown",
    defaultMinifier: minimizer.defaultMinifier,
    minifyEnabled: minimizer.minifyEnabled,
    adapterId: ADAPTER_ID,
    adapterVersion: ADAPTER_VERSION,
    engineDetails: {},
    overrides,
    configFile: request.configFile,
    context: diagnosticConfig.context,
    configSummary: {
      "configuration name": config.name ?? "(unnamed)",
      target,
      "optimization.minimize": summarizeValue(config.optimization?.minimize),
      "optimization.minimizer": minimizer.summary,
      "optimization.concatenateModules": summarizeValue(
        config.optimization?.concatenateModules,
      ),
      "optimization.sideEffects": summarizeValue(
        config.optimization?.sideEffects,
      ),
      "optimization.usedExports": summarizeValue(
        config.optimization?.usedExports,
      ),
      "optimization.providedExports": summarizeValue(
        config.optimization?.providedExports,
      ),
      "optimization.splitChunks": summarizeValue(
        config.optimization?.splitChunks,
      ),
      "original mode": summarizeValue(config.mode),
      "original devtool": summarizeValue(config.devtool),
    },
    libraryBuild: libraryBuild(config),
    ssrBuild: nodeTarget || platform.node === true || platform.nwjs === true,
    buildDir: request.buildDir,
    logs: [],
    exportUsage: exportUsageRecord,
    ...normalized,
  });
});
