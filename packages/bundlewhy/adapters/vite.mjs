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

function collectorPlugin(state) {
  return {
    name: "bundlewhy:collector",
    enforce: "post",
    apply: "build",
    configResolved(config) {
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
          inputFormat: info?.inputFormat ?? "unknown",
          renderedLength: 0,
          included: false,
          chunks: [],
          bailouts: [],
          reasons: [],
          packageName: pkg?.name ?? null,
          packageVersion: pkg?.version ?? null,
          packageDir: pkg?.dir ?? null,
          packageSideEffects:
            pkg && pkg.sideEffects !== null
              ? summarizeValue(pkg.sideEffects)
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

  const state = { resolved: null, collection: null, outputs: 0, warnings: [], logs: [] };
  const outDir = request.buildDir;
  const overrides = [
    `build.outDir=${path.basename(outDir)} (bundlewhy run directory)`,
    "build.emptyOutDir=true",
    "build.sourcemap=true",
    "build.write=true",
    "build.watch=null",
  ];

  progress("building", `Running vite build with Vite ${vite.version}`);
  try {
    await vite.build({
      root: request.projectRoot,
      configFile: request.configFile ?? false,
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
    });
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
    context: request.projectRoot,
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
