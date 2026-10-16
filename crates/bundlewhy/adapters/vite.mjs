// bundlewhy collector adapter for Vite (Rolldown + Oxc minifier).
//
// Runs one client production build with the project's own Vite installation
// and records Rolldown module/chunk facts through a collector plugin.

import fs from "node:fs";
import path from "node:path";

import {
  AdapterError,
  AdapterErrorCode,
  checkEngineVersion,
  emit,
  importFile,
  moduleFilePath,
  nearestPackage,
  progress,
  resolveProjectPackage,
  runAdapter,
  stripAnsi,
  summarizeValue,
  waitForProtectionAck,
  writeCollection,
} from "./common.mjs";

export const ADAPTER_ID = "bundlewhy-collector-vite";
export const ADAPTER_VERSION = "4";

/** Plugins that the adapter adds; they are not part of the project's config. */
const BUNDLEWHY_PLUGIN_NAMES = new Set([
  "bundlewhy:collector",
  "bundlewhy:config-file",
]);

/** Vite API functions that the adapter calls on the project's installation. */
export const REQUIRED_VITE_API = Object.freeze([
  "loadConfigFromFile",
  "build",
  "mergeConfig",
  "createLogger",
]);

/**
 * Normalizes an imported Vite module namespace into the API the adapter uses.
 *
 * The adapter imports the file that the project's `require.resolve` selects.
 * For packages whose export map sends `require` to a CommonJS entry (Vite 5),
 * Node exposes the API only on the namespace's `default` export. Named exports
 * win when present; otherwise the `default` export (or its own `default`) is
 * used. Returns the normalized API and the names of required functions that are
 * still missing.
 */
export function normalizeViteModule(namespace) {
  const sources = [];
  const addSource = (candidate) => {
    if (
      candidate !== null &&
      (typeof candidate === "object" || typeof candidate === "function") &&
      !sources.includes(candidate)
    ) {
      sources.push(candidate);
    }
  };
  addSource(namespace);
  addSource(namespace?.default);
  addSource(namespace?.default?.default);
  const pick = (name, type) => {
    for (const source of sources) {
      if (typeof source[name] === type) return source[name];
    }
    return undefined;
  };
  const api = {
    version: pick("version", "string"),
    rolldownVersion: pick("rolldownVersion", "string"),
  };
  const missing = [];
  for (const name of REQUIRED_VITE_API) {
    api[name] = pick(name, "function");
    if (api[name] === undefined) missing.push(name);
  }
  return { api, missing };
}

/**
 * Match Vite's resolver identity for the root recorded in a collection.
 * Vite resolves module IDs through symlinks unless preserveSymlinks is set;
 * retaining the lexical root in that mode would make ownership and freshness
 * checks disagree with the IDs emitted by the build.
 */
export function normalizeCollectionRoot(root, preserveSymlinks = false) {
  // Vite normalizes root separators before configResolved, including when it
  // preserves symlinks or cannot resolve a lexical path yet.
  if (preserveSymlinks) return root.replaceAll("\\", "/");
  // Vite keeps the resolved lexical root when realpath cannot access it yet.
  try {
    return fs.realpathSync(root).replaceAll("\\", "/");
  } catch {
    return root.replaceAll("\\", "/");
  }
}

/**
 * Evaluates `build.rolldownOptions.treeshake.moduleSideEffects` for one bundled
 * module the way Rolldown does when no plugin hook set a value for it.
 *
 * Returns "true" or "false" when the option decides the module, "null" when it
 * gives no verdict (the default `true` and an unmatched rule list, so Rolldown
 * continues with the package `sideEffects` field and its own code analysis),
 * or a value summary for options that bundlewhy does not evaluate (functions
 * and unknown shapes).
 */
export function configuredModuleSideEffects(option, id) {
  if (option === undefined || option === true) return "null";
  if (option === false) return "false";
  // Rolldown normalizes this to "external modules are side-effect free, local
  // modules have side effects"; the collected modules are bundled ones.
  if (option === "no-external") return "true";
  if (!Array.isArray(option)) return summarizeValue(option);
  // A list of module IDs: listed modules have side effects, all others do not.
  // Rolldown reads an empty list the same way, so no module has side effects.
  if (option.every((entry) => typeof entry === "string")) {
    return String(option.includes(id));
  }
  // A list of rules: the first matching rule decides.
  for (const rule of option) {
    const valid =
      rule !== null &&
      typeof rule === "object" &&
      typeof rule.sideEffects === "boolean" &&
      (rule.test === undefined || rule.test instanceof RegExp) &&
      (rule.external === undefined || typeof rule.external === "boolean") &&
      (rule.test !== undefined || rule.external !== undefined);
    if (!valid) return summarizeValue(option);
  }
  for (const rule of option) {
    // Bundled modules are never external.
    if (rule.external === true) continue;
    // Rolldown tests the pattern without regard to `lastIndex` state.
    const test =
      rule.test === undefined ||
      new RegExp(rule.test.source, rule.test.flags.replace(/[gy]/g, "")).test(
        id,
      );
    if (test) return String(rule.sideEffects);
  }
  return "null";
}

/**
 * Whether one minifier stage runs for the analyzed output. The values are the
 * wire values of the Rust `Stage` enum in the collection (`mangle`,
 * `deadCodeElimination` and `compress`) and of the `...output.minify.*`
 * configuration-summary entries.
 */
export const StageSetting = Object.freeze({
  Enabled: "enabled",
  Disabled: "disabled",
});

/** Name-mangling stage setting; the same values as `StageSetting`. */
export const MangleSetting = StageSetting;

/**
 * Value of the `build.rolldownOptions.output.minify` configuration-summary
 * entry: whether Rolldown's own minifier (Oxc) runs for the analyzed output.
 * `DceOnly` marks Rolldown's `'dce-only'` mode, where dead-code elimination
 * runs while compression, name mangling and whitespace removal do not.
 */
export const OutputMinifySetting = Object.freeze({
  Enabled: "enabled",
  DceOnly: "dce-only",
  Disabled: "disabled",
});

const ENABLED_STAGES = Object.freeze({
  dce: StageSetting.Enabled,
  compress: StageSetting.Enabled,
  mangle: StageSetting.Enabled,
});
const DISABLED_STAGES = Object.freeze({
  dce: StageSetting.Disabled,
  compress: StageSetting.Disabled,
  mangle: StageSetting.Disabled,
});
const DCE_ONLY_STAGES = Object.freeze({
  dce: StageSetting.Enabled,
  compress: StageSetting.Disabled,
  mangle: StageSetting.Disabled,
});

function stageSetting(runs) {
  return runs ? StageSetting.Enabled : StageSetting.Disabled;
}

/**
 * Resolves which Rolldown minifier stages run, from the configured
 * `rolldownOptions.output.minify` option and Vite's `build.minify`. Plugins'
 * `outputOptions` hooks can still change the option later; the collector
 * replaces the result with `resolveNormalizedMinifyStages` in `generateBundle`.
 *
 * The result holds three independent facts:
 *
 * - `dce`: dead-code elimination runs. It is the Oxc compressor's dead-code
 *   pass, so it runs for `true`, for `'dce-only'` and for an options object
 *   unless `compress` is `false`.
 * - `compress`: code compression runs (`true`, or an object unless `compress`
 *   is `false`). `'dce-only'` does not compress.
 * - `mangle`: name mangling runs (`true`, or an object unless `mangle` is
 *   `false`). `false` and `'dce-only'` never mangle.
 *
 * Vite builds the output `minify` option from `build.minify` (`'oxc'` or
 * `true` gives `true`, `false` gives `'dce-only'`, and any other minifier
 * name such as `'terser'` gives `false` because that minifier runs in a Vite
 * plugin instead) and lets an explicit `output.minify` replace it. A
 * single-entry output array is read as its entry; with several entries a stage
 * counts as disabled only when every entry disables it. Anything unrecognized
 * keeps every stage enabled, so a finding is only exempted by an explicit
 * setting and never by a guess.
 */
export function resolveMinifyStages(buildMinify, output) {
  const entries = Array.isArray(output) ? output : [output];
  if (entries.length === 0) return resolveMinifyStagesOption(buildMinify);
  const settings = entries.map((entry) =>
    resolveMinifyStagesOption(buildMinify, entry?.minify),
  );
  const anyEnabled = (key) =>
    settings.some((setting) => setting[key] === StageSetting.Enabled);
  return {
    dce: stageSetting(anyEnabled("dce")),
    compress: stageSetting(anyEnabled("compress")),
    mangle: stageSetting(anyEnabled("mangle")),
  };
}

/**
 * Reads the stages from the normalized `minify` option that a `generateBundle`
 * hook receives. Plugins' `outputOptions` hooks run after `configResolved`, so
 * this is the only place that sees what Rolldown applies. The normalized
 * option is `false`, `'dce-only'`, `true` or an options object that only
 * carries the `compress` and `mangle` stages that run. Returns `null` for
 * anything else, so the caller keeps the stages resolved from the
 * configuration.
 */
export function resolveNormalizedMinifyStages(normalizedMinify) {
  if (normalizedMinify === false) return DISABLED_STAGES;
  if (normalizedMinify === "dce-only") return DCE_ONLY_STAGES;
  if (normalizedMinify === true) return ENABLED_STAGES;
  if (normalizedMinify !== null && typeof normalizedMinify === "object") {
    const compress = isMinifyStageEnabled(normalizedMinify.compress);
    return {
      dce: stageSetting(compress),
      compress: stageSetting(compress),
      mangle: stageSetting(isMinifyStageEnabled(normalizedMinify.mangle)),
    };
  }
  return null;
}

function isMinifyStageEnabled(stage) {
  return stage !== undefined && stage !== null && stage !== false;
}

function resolveMinifyStagesOption(buildMinify, outputMinify) {
  // Vite's own value for an absent `output.minify`: only the Oxc minifier
  // (`'oxc'`, or `true`/absent which select it) runs inside Rolldown, and
  // `build.minify: false` still leaves dead-code elimination on.
  if (outputMinify === undefined) {
    if (buildMinify === false) return DCE_ONLY_STAGES;
    return isExternalMinifierName(buildMinify)
      ? DISABLED_STAGES
      : ENABLED_STAGES;
  }
  if (outputMinify === false) return DISABLED_STAGES;
  if (outputMinify === "dce-only") return DCE_ONLY_STAGES;
  if (
    outputMinify !== null &&
    typeof outputMinify === "object" &&
    !Array.isArray(outputMinify)
  ) {
    const compress = outputMinify.compress !== false;
    return {
      dce: stageSetting(compress),
      compress: stageSetting(compress),
      mangle: stageSetting(outputMinify.mangle !== false),
    };
  }
  return ENABLED_STAGES;
}

/** Name-mangling stage of the configured output. */
export function resolveMangleSetting(buildMinify, output) {
  return resolveMinifyStages(buildMinify, output).mangle;
}

/** Name-mangling stage of the normalized output option, or `null`. */
export function resolveNormalizedMangleSetting(normalizedMinify) {
  return resolveNormalizedMinifyStages(normalizedMinify)?.mangle ?? null;
}

/**
 * Summarizes the stages as Rolldown's minifier setting: `enabled` when
 * compression or name mangling runs, `dce-only` when only dead-code
 * elimination runs and `disabled` when no stage runs.
 */
export function summarizeOutputMinify(stages) {
  if (
    stages.compress === StageSetting.Enabled ||
    stages.mangle === StageSetting.Enabled
  ) {
    return OutputMinifySetting.Enabled;
  }
  return stages.dce === StageSetting.Enabled
    ? OutputMinifySetting.DceOnly
    : OutputMinifySetting.Disabled;
}

/** Rolldown minifier setting of the configured output. */
export function resolveOutputMinifySetting(buildMinify, output) {
  return summarizeOutputMinify(resolveMinifyStages(buildMinify, output));
}

/** Rolldown minifier setting of the normalized output option, or `null`. */
export function resolveNormalizedOutputMinifySetting(normalizedMinify) {
  const stages = resolveNormalizedMinifyStages(normalizedMinify);
  return stages === null ? null : summarizeOutputMinify(stages);
}

/** A `build.minify` value that names a minifier other than Oxc. */
function isExternalMinifierName(buildMinify) {
  return typeof buildMinify === "string" && buildMinify !== "oxc";
}

/**
 * Derives the minifier gates of the collection from `build.minify` and the
 * effective Rolldown stages. The returned `stages` are the effective facts of
 * the whole build: a named minifier such as `'terser'` compresses, mangles and
 * removes dead code outside Rolldown, so every stage counts as running.
 *
 * - `minifyEnabled`: compression or name mangling runs (including when only an
 *   `output.minify` option or an `outputOptions` hook enables it over
 *   `build.minify: false`), or `build.minify` names another minifier.
 *   Rolldown's `'dce-only'` mode is not minification.
 * - `defaultMinifier`: Rolldown's Oxc minifier is the one that runs, either
 *   fully or as dead-code elimination only. A named minifier is never the
 *   default one, whatever the output option says.
 * - `minifierName`: with no minifier running it is `"false"`, so the report
 *   never claims a minifier that did not run.
 */
export function resolveMinifierGate(buildMinify, rolldownStages) {
  const external = isExternalMinifierName(buildMinify);
  const rolldownMinifies =
    rolldownStages.compress === StageSetting.Enabled ||
    rolldownStages.mangle === StageSetting.Enabled;
  const rolldownRuns =
    rolldownMinifies || rolldownStages.dce === StageSetting.Enabled;
  const effective = (key) =>
    stageSetting(external || rolldownStages[key] === StageSetting.Enabled);
  return {
    minifyEnabled: rolldownMinifies || external,
    minifierName: external ? buildMinify : rolldownRuns ? "oxc" : "false",
    defaultMinifier: rolldownRuns && !external,
    stages: {
      dce: effective("dce"),
      compress: effective("compress"),
      mangle: effective("mangle"),
    },
  };
}

/**
 * Gives plugins the real `resolvedConfig.configFile` without evaluating the
 * configuration file a second time.
 *
 * The adapter loads the configuration file once itself and builds with that
 * loaded object and `configFile: false`, so Vite never evaluates a stateful
 * configuration factory (process-level side effects, one-shot resources) twice.
 * With `configFile: false` Vite leaves `resolvedConfig.configFile` undefined,
 * yet plugins such as React Router's Vite plugin require it. This plugin runs
 * first among the `pre` plugins, so its synchronous `configResolved` handler
 * sets the value before any project plugin reads it.
 */
export function configFileIdentityPlugin(configFile, dependencies = []) {
  const normalize = (file) => path.resolve(file).replaceAll("\\", "/");
  return {
    name: "bundlewhy:config-file",
    enforce: "pre",
    apply: "build",
    configResolved(config) {
      if (config.configFile !== undefined) return;
      config.configFile = normalize(configFile);
      config.configFileDependencies = dependencies.map(normalize);
    },
  };
}

/**
 * Names under which an emitted chunk can list an external module in its
 * `imports`: the module id and, when `output.paths` is configured, the string
 * that replaces the id in the generated code. `paths` is a record or a
 * function in Rolldown. Both names are returned so that a rewritten external
 * is recognised whichever form the chunk list carries; a name shared with an
 * emitted chunk is then treated as ambiguous by the Rust side.
 */
export function externalImportNames(externalIds, paths) {
  const names = new Set(externalIds);
  for (const id of externalIds) {
    let rendered;
    try {
      rendered =
        typeof paths === "function"
          ? paths(id)
          : paths !== null && typeof paths === "object"
            ? paths[id]
            : undefined;
    } catch {
      // A throwing user function cannot be evaluated again here; the id alone
      // is still recorded.
      rendered = undefined;
    }
    if (typeof rendered === "string") names.add(rendered);
  }
  return names;
}

/**
 * The chunk-graph fields of one emitted chunk: static imports, the imports
 * that refer to an external module, and dynamic imports.
 */
export function chunkGraphFields(chunk, externalNames) {
  const imports = [...(chunk.imports ?? [])];
  return {
    imports,
    externalImports: imports.filter((name) => externalNames.has(name)),
    dynamicImports: [...(chunk.dynamicImports ?? [])],
  };
}

/**
 * Replaces the chunk graph of the snapshot with the one of the finished build.
 *
 * A `generateBundle` hook of an output-only plugin (`output.plugins`) can run
 * after the collector whatever its own ordering is, so the snapshot taken in
 * the hook may predate a rewrite of chunk imports. The value `build()` returns
 * is the bundle after every hook. Chunk modules and sizes stay as snapshotted;
 * only the import lists come from the final bundle. When the final output
 * cannot be read or its chunk files differ from the snapshot, the import lists
 * are dropped, which the analysis reads as "no chunk import data" and never as
 * proof.
 */
export function applyFinalChunkGraph(collection, buildResult, externalNames) {
  const results = Array.isArray(buildResult) ? buildResult : [buildResult];
  const output = results.length === 1 ? results[0]?.output : null;
  const finalChunks = new Map();
  if (Array.isArray(output)) {
    for (const item of output) {
      if (item?.type === "chunk") finalChunks.set(item.fileName, item);
    }
  }
  const matches =
    finalChunks.size === collection.chunks.length &&
    collection.chunks.every((chunk) => finalChunks.has(chunk.file));
  for (const chunk of collection.chunks) {
    delete chunk.imports;
    delete chunk.externalImports;
    delete chunk.dynamicImports;
    if (matches) {
      Object.assign(
        chunk,
        chunkGraphFields(finalChunks.get(chunk.file), externalNames),
      );
    }
  }
  return matches;
}

function collectorPlugin(state) {
  return {
    name: "bundlewhy:collector",
    enforce: "post",
    apply: "build",
    async configResolved(config) {
      const resolvedRoot = normalizeCollectionRoot(
        config.root,
        config.resolve?.preserveSymlinks === true,
      );
      // A config hook can choose an arbitrary external root and modify its
      // files before configResolved runs. No earlier protection snapshot can
      // cover an unknown path, so reject that run instead of reporting a
      // clean project from a post-mutation baseline.
      if (resolvedRoot !== state.root) {
        throw new AdapterError(
          AdapterErrorCode.UnsupportedBuild,
          "A Vite config hook changed the project root after the protection snapshot",
          "Select the final root in the Vite configuration before plugin config hooks run, then start a new diagnostic run.",
        );
      }
      state.root = resolvedRoot;
      const treeshakeOption =
        config.build.rolldownOptions?.treeshake ??
        config.build.rollupOptions?.treeshake;
      state.moduleSideEffectsOption =
        treeshakeOption !== null && typeof treeshakeOption === "object"
          ? treeshakeOption.moduleSideEffects
          : undefined;
      const outputOption =
        config.build.rolldownOptions?.output ??
        config.build.rollupOptions?.output;
      state.resolved = {
        minify: config.build.minify,
        stages: resolveMinifyStages(config.build.minify, outputOption),
        sourcemap: config.build.sourcemap,
        lib: Boolean(config.build.lib),
        ssr: Boolean(config.build.ssr),
        target: summarizeValue(config.build.target),
        treeshake: summarizeValue(
          config.build.rolldownOptions?.treeshake ??
            config.build.rollupOptions?.treeshake,
        ),
        mode: config.mode,
        defineKeys: Object.keys(config.define ?? {}).sort(),
        plugins: config.plugins.map((plugin) => plugin.name).filter(Boolean),
      };
    },
    onLog(level, log) {
      // Rolldown reports optimization-relevant warnings (for example EVAL and
      // INEFFECTIVE_DYNAMIC_IMPORT) as structured logs. Record them as
      // engine-reported evidence and let them continue to Vite's logger.
      state.logs.push({
        level,
        code: log.code ?? null,
        message: stripAnsi(String(log.message ?? "")).slice(0, 2000),
        id: log.id ?? null,
        line: log.loc?.line ?? null,
        column: log.loc?.column ?? null,
      });
    },
    // `enforce: "post"` orders the plugin, while hook ordering is separate: a
    // project plugin with `generateBundle: { order: "post" }` can still add,
    // remove or rewrite chunk imports. This hook is post-ordered as well, so the
    // snapshot is taken from the final bundle.
    generateBundle: {
      order: "post",
      handler(options, bundle) {
        // generateBundle runs once per output. bundlewhy analyzes one output;
        // several outputs are rejected after the build instead of silently
        // keeping only the last one.
        state.outputs += 1;
        if (state.outputs > 1) return;
        // Plugins' `outputOptions` hooks can replace `output.minify` after
        // `configResolved`, so the analyzed output's dead-code elimination,
        // compression and mangling stages are read together from the
        // normalized options this hook receives. Every minifier gate of the
        // collection derives from them.
        const effectiveStages = resolveNormalizedMinifyStages(options?.minify);
        if (effectiveStages !== null && state.resolved) {
          state.resolved.stages = effectiveStages;
        }
        const modules = new Map();
        const recordModule = (id) => {
          if (modules.has(id)) return modules.get(id);
          const info = this.getModuleInfo(id);
          const file = moduleFilePath(id);
          const pkg = file ? nearestPackage(file) : null;
          const record = {
            id,
            path: file,
            virtual: !file,
            isEntry: Boolean(info?.isEntry),
            importers: info?.importers ?? [],
            dynamicImporters: info?.dynamicImporters ?? [],
            importedIds: info?.importedIds ?? [],
            dynamicImportedIds: info?.dynamicallyImportedIds ?? [],
            exports: info?.exports ?? null,
            usedExports: null,
            moduleSideEffects: info
              ? summarizeValue(info.moduleSideEffects)
              : null,
            // Hook-level values (getModuleInfo) list only what JavaScript hooks
            // set. They never reflect the configured
            // treeshake.moduleSideEffects option, which Rolldown consults next,
            // or a value that a native plugin (such as Vite's resolver) set.
            treeshakeSideEffects: configuredModuleSideEffects(
              state.moduleSideEffectsOption,
              id,
            ),
            inputFormat: info?.inputFormat ?? "unknown",
            renderedLength: 0,
            included: false,
            chunks: [],
            bailouts: [],
            reasons: [],
            packageName: pkg?.name ?? null,
            packageVersion: pkg?.version ?? null,
            packageDir: pkg?.dir ?? null,
            // A string is a glob, so quote it: the bare words `true` and
            // `false` stay reserved for the boolean values.
            packageSideEffects:
              pkg && pkg.sideEffects !== null
                ? typeof pkg.sideEffects === "string"
                  ? JSON.stringify(pkg.sideEffects)
                  : summarizeValue(pkg.sideEffects)
                : null,
          };
          modules.set(id, record);
          return record;
        };

        const externalIds = new Set();
        for (const id of this.getModuleIds()) {
          recordModule(id);
          if (this.getModuleInfo(id)?.code === null) externalIds.add(id);
        }

        state.externalNames = externalImportNames(externalIds, options?.paths);
        const chunks = [];
        const assets = [];
        for (const output of Object.values(bundle)) {
          if (output.type === "asset") {
            assets.push(output.fileName);
            continue;
          }
          const chunkModules = [];
          for (const [id, rendered] of Object.entries(output.modules)) {
            const record = recordModule(id);
            record.renderedLength += rendered.renderedLength;
            record.usedExports = [
              ...new Set([
                ...(record.usedExports ?? []),
                ...rendered.renderedExports,
              ]),
            ];
            record.included = record.included || rendered.renderedLength > 0;
            record.chunks.push(output.fileName);
            chunkModules.push(id);
          }
          chunks.push({
            file: output.fileName,
            isEntry: output.isEntry,
            isDynamicEntry: output.isDynamicEntry,
            facadeModuleId: output.facadeModuleId ?? null,
            modules: chunkModules,
            map: output.sourcemapFileName ?? `${output.fileName}.map`,
            // Emitted chunk graph: static imports are loaded together with the
            // chunk, dynamic imports are requested later. `imports` mixes
            // emitted chunk files with external specifiers, so
            // `externalImports` names the entries that refer to an external
            // module (by id or by its `output.paths` replacement). The lists
            // are replaced by the finished build's bundle after `build()`.
            ...chunkGraphFields(output, state.externalNames),
          });
        }
        state.collection = { modules: [...modules.values()], chunks, assets };
      },
    },
  };
}

await runAdapter(async (request) => {
  progress("detecting", "Loading the project's Vite installation");
  const vitePackage = resolveProjectPackage(request.projectRoot, "vite");
  checkEngineVersion(request, "vite", vitePackage.version);
  const viteModule = await importFile(vitePackage.require.resolve("vite"));

  // Validate the adapter's own API needs before any project configuration is
  // evaluated, so an engine API mismatch is never reported as a project
  // configuration or build failure.
  const { api: vite, missing: missingViteApi } =
    normalizeViteModule(viteModule);
  if (missingViteApi.length > 0) {
    throw new AdapterError(
      AdapterErrorCode.CollectionFailed,
      `Vite ${vitePackage.version} does not provide the API the bundlewhy Vite adapter requires (missing: ${missingViteApi.join(", ")})`,
      request.supportedVersion
        ? `Install vite@${request.supportedVersion}, or use a bundlewhy adapter that is compatible with Vite ${vitePackage.version}.`
        : `Install a Vite version in the bundlewhy compatibility matrix, or use a bundlewhy adapter that is compatible with Vite ${vitePackage.version}.`,
    );
  }
  if (typeof vite.version !== "string") vite.version = vitePackage.version;

  const state = {
    root: null,
    resolved: null,
    moduleSideEffectsOption: undefined,
    collection: null,
    outputs: 0,
    externalNames: new Set(),
    warnings: [],
    logs: [],
  };
  const outDir = request.buildDir;
  const overrides = [
    `build.outDir=${path.basename(outDir)} (bundlewhy run directory)`,
    "build.emptyOutDir=true",
    "build.sourcemap=true",
    "build.write=true",
    "build.watch=null",
  ];

  // Capture the selected project before loading the user's configuration.
  // Configuration factories and plugins can mutate files while they are
  // evaluated, so the Rust side must acknowledge this baseline first.
  state.root = normalizeCollectionRoot(request.projectRoot);
  emit({ event: "protection", root: state.root });
  await waitForProtectionAck();

  // Load the configuration once on its own so that evaluation errors and
  // rejecting configuration factories are reported as configuration
  // problems, not build failures. The loaded config is then built inline, so
  // the configuration is evaluated only once.
  let userConfig = {};
  let loadedConfigFile = null;
  let loadedDependencies = [];
  if (request.configFile) {
    try {
      const loaded = await vite.loadConfigFromFile(
        {
          command: "build",
          mode: "production",
          isSsrBuild: false,
          isPreview: false,
        },
        request.configFile,
        request.projectRoot,
        "silent",
      );
      userConfig = loaded?.config ?? {};
      loadedConfigFile = loaded?.path ?? request.configFile;
      loadedDependencies = Array.isArray(loaded?.dependencies)
        ? loaded.dependencies
        : [];
    } catch (error) {
      throw new AdapterError(
        AdapterErrorCode.ConfigLoadFailed,
        `Could not load ${path.basename(request.configFile)}: ${error?.message ?? error}`,
        "Fix the error in the Vite configuration (or its configuration function), then start a new run.",
      );
    }
  }

  // Capture the configured root after loading the user's configuration. This
  // adds external roots to the protection set before the build starts.
  const configuredRoot =
    typeof userConfig.root === "string" ? userConfig.root : request.projectRoot;
  const root = path.isAbsolute(configuredRoot)
    ? configuredRoot
    : path.resolve(request.projectRoot, configuredRoot);
  state.root = normalizeCollectionRoot(
    root,
    userConfig.resolve?.preserveSymlinks === true,
  );
  emit({ event: "protection", root: state.root });
  await waitForProtectionAck();

  progress("building", `Running vite build with Vite ${vite.version}`);
  const customLogger = {
    ...vite.createLogger("warn"),
    warn(message) {
      state.warnings.push(stripAnsi(String(message)).slice(0, 2000));
    },
    warnOnce(message) {
      state.warnings.push(stripAnsi(String(message)).slice(0, 2000));
    },
  };
  const collectionOverrides = {
    mode: "production",
    logLevel: "warn",
    clearScreen: false,
    customLogger,
    build: {
      outDir,
      emptyOutDir: true,
      sourcemap: true,
      write: true,
      watch: null,
    },
  };
  // The user's configuration is evaluated exactly once: the preliminary load
  // above produced `userConfig`, and the build receives that object with
  // `configFile: false`. Passing `configFile` to Vite would make it evaluate a
  // stateful configuration factory a second time. When a configuration file
  // was loaded, a leading `pre` plugin restores the real
  // `resolvedConfig.configFile` that plugins such as React Router's Vite plugin
  // require. The collector is merged after the project's plugins and stays last.
  const mergedConfig = vite.mergeConfig(userConfig, {
    ...collectionOverrides,
    root,
    configFile: false,
    plugins: [collectorPlugin(state)],
  });
  const buildConfig =
    loadedConfigFile === null
      ? mergedConfig
      : vite.mergeConfig(
          {
            plugins: [
              configFileIdentityPlugin(loadedConfigFile, loadedDependencies),
            ],
          },
          mergedConfig,
        );
  let buildResult;
  try {
    buildResult = await vite.build(buildConfig);
  } catch (error) {
    if (error instanceof AdapterError) throw error;
    // Configuration evaluation errors were classified before the build. An
    // error here comes from the build itself or from a plugin hook, and the
    // diagnostic build differs from the native one, so do not claim that the
    // project's own build is broken.
    throw new AdapterError(
      AdapterErrorCode.BuildFailed,
      `vite build failed: ${error?.message ?? error}`,
      "Read the error above. If `vite build` also fails in the project, fix that error. If it succeeds, a plugin or option is incompatible with bundlewhy's diagnostic build, which forces the output directory, source maps and a post collector plugin. Then start a new run.",
    );
  }

  if (state.outputs > 1) {
    throw new AdapterError(
      AdapterErrorCode.UnsupportedBuild,
      `The configuration produces ${state.outputs} Rolldown outputs; bundlewhy analyzes one client output per run`,
      "Configure a single build.rolldownOptions.output entry for the analyzed build.",
    );
  }
  progress("collecting", "Writing the collected module graph");
  if (!state.collection) {
    throw new AdapterError(
      AdapterErrorCode.CollectionFailed,
      "The bundlewhy collector plugin did not observe a client bundle",
      "Confirm the configuration builds a client bundle (library and SSR builds are not supported).",
    );
  }

  // Output-only plugins can rewrite chunk imports after the collector's
  // `generateBundle` hook ran; take the chunk graph from the finished build.
  const finalGraph = applyFinalChunkGraph(
    state.collection,
    buildResult,
    state.externalNames,
  );
  if (!finalGraph) {
    emit({
      event: "progress",
      stage: "collecting",
      message:
        "The finished build's chunks differ from the snapshot; chunk import data is omitted",
    });
  }

  const resolved = state.resolved ?? {};
  const minify = resolved.minify;
  // Vite documents `build.minify` default as 'oxc' for client builds; `true`
  // selects the same default minifier. Whether a minifier actually runs also
  // depends on the effective `output.minify` option, so the gates derive from
  // it together with `build.minify`, never from `build.minify` alone.
  const rolldownStages = resolved.stages ?? ENABLED_STAGES;
  const gate = resolveMinifierGate(
    minify === true || minify === undefined ? "oxc" : minify,
    rolldownStages,
  );
  const { minifyEnabled, minifierName, stages } = gate;
  const configSummary = {
    "build.minify": summarizeValue(minify),
    "build.rolldownOptions.output.minify": summarizeOutputMinify(rolldownStages),
    "build.rolldownOptions.output.minify.dce": stages.dce,
    "build.rolldownOptions.output.minify.compress": stages.compress,
    "build.rolldownOptions.output.minify.mangle": stages.mangle,
    "build.target": resolved.target ?? "unknown",
    "build.rolldownOptions.treeshake": resolved.treeshake ?? "undefined",
    "build.lib": String(Boolean(resolved.lib)),
    "build.ssr": String(Boolean(resolved.ssr)),
    "original build.sourcemap": summarizeValue(resolved.sourcemap),
    mode: resolved.mode ?? "production",
    "define keys": (resolved.defineKeys ?? []).join(", ") || "(none)",
    plugins: (resolved.plugins ?? [])
      .filter((name) => !BUNDLEWHY_PLUGIN_NAMES.has(name))
      .join(", "),
  };

  writeCollection(request, {
    bundler: "vite",
    bundlerPackage: "vite",
    bundlerVersion: vite.version,
    minifier: gate.defaultMinifier ? "Oxc minifier (Rolldown)" : minifierName,
    minifierVersion: gate.defaultMinifier ? vite.rolldownVersion : "unknown",
    defaultMinifier: gate.defaultMinifier,
    minifyEnabled,
    deadCodeElimination: stages.dce,
    compress: stages.compress,
    mangle: stages.mangle,
    adapterId: ADAPTER_ID,
    adapterVersion: ADAPTER_VERSION,
    engineDetails: { rolldownVersion: vite.rolldownVersion },
    overrides,
    configFile: request.configFile,
    context: state.root ?? request.projectRoot,
    configSummary,
    libraryBuild: Boolean(resolved.lib),
    ssrBuild: Boolean(resolved.ssr),
    buildDir: outDir,
    warnings: state.warnings,
    logs: state.logs,
    errors: [],
    ...state.collection,
  });

  if (fs.existsSync(outDir)) {
    emit({
      event: "progress",
      stage: "collecting",
      message: "Diagnostic build written",
    });
  }
});
