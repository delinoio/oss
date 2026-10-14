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
  writeCollection,
} from "./common.mjs";

export const ADAPTER_ID = "bundlewhy-collector-vite";
export const ADAPTER_VERSION = "1";

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

function collectorPlugin(state) {
  return {
    name: "bundlewhy:collector",
    enforce: "post",
    apply: "build",
    configResolved(config) {
      state.root = config.root;
      const treeshakeOption =
        config.build.rolldownOptions?.treeshake ??
        config.build.rollupOptions?.treeshake;
      state.moduleSideEffectsOption =
        treeshakeOption !== null && typeof treeshakeOption === "object"
          ? treeshakeOption.moduleSideEffects
          : undefined;
      state.resolved = {
        minify: config.build.minify,
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
    generateBundle(_options, bundle) {
      // generateBundle runs once per output. bundlewhy analyzes one output;
      // several outputs are rejected after the build instead of silently
      // keeping only the last one.
      state.outputs += 1;
      if (state.outputs > 1) return;
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

      for (const id of this.getModuleIds()) recordModule(id);

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
        });
      }
      state.collection = { modules: [...modules.values()], chunks, assets };
    },
  };
}

await runAdapter(async (request) => {
  progress("detecting", "Loading the project's Vite installation");
  const vitePackage = resolveProjectPackage(request.projectRoot, "vite");
  checkEngineVersion(request, "vite", vitePackage.version);
  const vite = await importFile(vitePackage.require.resolve("vite"));

  const state = {
    root: null,
    resolved: null,
    moduleSideEffectsOption: undefined,
    collection: null,
    outputs: 0,
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

  // Load the configuration once on its own so that evaluation errors and
  // rejecting configuration factories are reported as configuration
  // problems, not build failures. The loaded config is then built inline.
  let userConfig = {};
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
    } catch (error) {
      throw new AdapterError(
        AdapterErrorCode.ConfigLoadFailed,
        `Could not load ${path.basename(request.configFile)}: ${error?.message ?? error}`,
        "Fix the error in the Vite configuration (or its configuration function), then start a new run.",
      );
    }
  }

  progress("building", `Running vite build with Vite ${vite.version}`);
  try {
    await vite.build(
      vite.mergeConfig(userConfig, {
        root: userConfig.root ?? request.projectRoot,
        configFile: false,
        mode: "production",
        logLevel: "warn",
        clearScreen: false,
        customLogger: {
          ...vite.createLogger("warn"),
          warn(message) {
            state.warnings.push(stripAnsi(String(message)).slice(0, 2000));
          },
          warnOnce(message) {
            state.warnings.push(stripAnsi(String(message)).slice(0, 2000));
          },
        },
        build: {
          outDir,
          emptyOutDir: true,
          sourcemap: true,
          write: true,
          watch: null,
        },
        plugins: [collectorPlugin(state)],
      }),
    );
  } catch (error) {
    throw new AdapterError(
      AdapterErrorCode.BuildFailed,
      `vite build failed: ${error?.message ?? error}`,
      "Fix the build error, confirm `vite build` succeeds in the project, then start a new run.",
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

  const resolved = state.resolved ?? {};
  const minify = resolved.minify;
  const minifyEnabled = minify !== false;
  // Vite documents `build.minify` default as 'oxc' for client builds; `true`
  // selects the same default minifier.
  const minifierName =
    minify === true || minify === undefined ? "oxc" : String(minify);
  const configSummary = {
    "build.minify": summarizeValue(minify),
    "build.target": resolved.target ?? "unknown",
    "build.rolldownOptions.treeshake": resolved.treeshake ?? "undefined",
    "build.lib": String(Boolean(resolved.lib)),
    "build.ssr": String(Boolean(resolved.ssr)),
    "original build.sourcemap": summarizeValue(resolved.sourcemap),
    mode: resolved.mode ?? "production",
    "define keys": (resolved.defineKeys ?? []).join(", ") || "(none)",
    plugins: (resolved.plugins ?? [])
      .filter((name) => name !== "bundlewhy:collector")
      .join(", "),
  };

  writeCollection(request, {
    bundler: "vite",
    bundlerPackage: "vite",
    bundlerVersion: vite.version,
    minifier: minifierName === "oxc" ? "Oxc minifier (Rolldown)" : minifierName,
    minifierVersion: minifierName === "oxc" ? vite.rolldownVersion : "unknown",
    defaultMinifier: minifierName === "oxc",
    minifyEnabled,
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
