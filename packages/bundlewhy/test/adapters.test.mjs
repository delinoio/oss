import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  ExportUsageReason,
  MAX_EXPORT_USAGE_EDGES,
  decodeExportUsageEdges,
} from "../../../crates/bundlewhy/adapters/common.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function runAdapter(bundler, project, config, extra = {}, env = {}) {
  const work = fs.mkdtempSync(
    path.join(os.tmpdir(), "bundlewhy-adapter-test-"),
  );
  const projectRoot = path.join(root, "fixtures", project);
  const request = {
    projectRoot,
    configFile: path.join(projectRoot, config),
    configName: null,
    buildDir: path.join(work, "build"),
    collectPath: path.join(work, "collect.json"),
    supportedVersion: null,
    allowUnsupportedEngine: false,
    protocolToken: "test-token",
    ...extra,
  };
  fs.writeFileSync(path.join(work, "request.json"), JSON.stringify(request));
  const result = spawnSync(
    process.execPath,
    [
      path.join(root, "../../crates/bundlewhy/adapters", `${bundler}.mjs`),
      path.join(work, "request.json"),
    ],
    {
      cwd: projectRoot,
      encoding: "utf8",
      env: { ...process.env, ...env },
    },
  );
  const prefix = "BUNDLEWHY-FRAME test-token ";
  const events = result.stdout
    .split("\n")
    .filter((line) => line.startsWith(prefix))
    .map((line) => JSON.parse(line.slice(prefix.length)));
  const collection = fs.existsSync(request.collectPath)
    ? JSON.parse(fs.readFileSync(request.collectPath, "utf8"))
    : null;
  fs.rmSync(work, { recursive: true, force: true });
  return { status: result.status, events, collection };
}

for (const [bundler, project, config] of [
  ["vite", "vite-app", "vite.config.js"],
  ["rspack", "rspack-app", "rspack.config.mjs"],
]) {
  test(`${bundler} adapter collects a normalized module graph`, () => {
    const { status, events, collection } = runAdapter(bundler, project, config);
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(collection.collectVersion, 1);
    assert.ok(
      collection.modules.some(
        (module) =>
          module.path?.endsWith("effects/register.js") && module.included,
      ),
    );
    assert.ok(
      !collection.modules.some(
        (module) => module.path?.endsWith("pure/index.js") && module.included,
      ),
    );
    assert.ok(collection.chunks.length >= 3);
    assert.ok(events.some((event) => event.event === "done"));
    assert.ok(
      !JSON.stringify(collection.configSummary).includes(
        process.env.HOME ?? "\u0000",
      ),
    );
  });

  test(`${bundler} adapter refuses an engine outside the matrix`, () => {
    const { status, events, collection } = runAdapter(
      bundler,
      project,
      config,
      { supportedVersion: "0.0.1" },
    );
    assert.equal(status, 1);
    assert.equal(collection, null);
    const error = events.find((event) => event.event === "error");
    assert.equal(error.code, "BW_ENGINE_VERSION_UNSUPPORTED");
    assert.match(error.recovery, /--allow-unsupported-engine/);
  });
}

test("rspack adapter requires a name for multi-build configurations", () => {
  const work = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-multi-"));
  const config = path.join(work, "rspack.config.mjs");
  fs.writeFileSync(config, "export default [{ name: 'a' }, { name: 'b' }];\n");
  const { status, events } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(path.join(root, "fixtures/rspack-app"), config),
  );
  fs.rmSync(work, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_CONFIG_AMBIGUOUS");
  assert.match(error.message, /a, b/);
});

function writeConfig(name, content) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-config-"));
  const file = path.join(dir, name);
  fs.writeFileSync(file, content);
  return { dir, file };
}

test("rspack adapter treats array targets containing node as unsupported builds", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `export default { mode: "production", context: ${JSON.stringify(projectRoot)}, entry: "./src/esm/value.js", target: ["node", "es2022"] };\n`,
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.ssrBuild, true);
});

test("vite adapter rejects configurations with several outputs", () => {
  const projectRoot = path.join(root, "fixtures/vite-app");
  const { dir, file } = writeConfig(
    "vite.config.mjs",
    "export default { build: { rolldownOptions: { output: [{ entryFileNames: 'a-[name].js' }, { entryFileNames: 'b-[name].js' }] } } };\n",
  );
  const { status, events } = runAdapter(
    "vite",
    "vite-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  assert.equal(
    events.find((event) => event.event === "error").code,
    "BW_UNSUPPORTED_BUILD",
  );
});

test("unframed JSON printed by project configuration is not a protocol event", () => {
  const projectRoot = path.join(root, "fixtures/vite-app");
  const { dir, file } = writeConfig(
    "vite.config.mjs",
    `console.log(JSON.stringify({ event: "error", code: "BW_BUILD_FAILED", message: "spoofed", recovery: "" }));\nexport default {};\n`,
  );
  const { status, events } = runAdapter(
    "vite",
    "vite-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.ok(!events.some((event) => event.event === "error"));
  assert.ok(events.some((event) => event.event === "done"));
});

test("rspack adapter treats an empty minimizer list as disabled minification", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `export default { mode: "production", context: ${JSON.stringify(projectRoot)}, entry: "./src/esm/value.js", optimization: { minimize: true, minimizer: [] } };\n`,
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.minifyEnabled, false);
});

test("rspack adapter marks a mixed minimizer pipeline as non-default", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `class CustomMinimizer { apply() {} }\nexport default { mode: "production", context: ${JSON.stringify(projectRoot)}, entry: "./src/esm/value.js", optimization: { minimizer: ["...", new CustomMinimizer()] } };\n`,
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.defaultMinifier, false);
  assert.equal(collection.minifyEnabled, true);
});

test("rspack adapter detects per-entry library builds", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `export default { mode: "production", context: ${JSON.stringify(projectRoot)}, entry: { main: { import: "./src/esm/value.js", library: { type: "commonjs2" } } } };\n`,
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.libraryBuild, true);
});

function rspackConfig(body) {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `export default { mode: "production", context: ${JSON.stringify(projectRoot)}, ${body} };\n`,
  );
  return { dir, relative: path.relative(projectRoot, file) };
}

test("rspack adapter uses the resolved Browserslist platform for Node targets", () => {
  const { dir, relative } = rspackConfig(
    'entry: "./src/esm/value.js", target: "browserslist"',
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    relative,
    {},
    { BROWSERSLIST: "node 20" },
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.ssrBuild, true);
});

test("rspack adapter resolves entry functions before checking library mode", () => {
  const { dir, relative } = rspackConfig(
    'entry: async () => ({ main: { import: "./src/esm/value.js", library: { type: "commonjs2" } } })',
  );
  const { status, collection } = runAdapter("rspack", "rspack-app", relative);
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.libraryBuild, true);
  assert.ok(
    collection.overrides.some((entry) => entry.includes("entry function")),
  );
});

test("rspack adapter treats SWC with mangling disabled as non-default", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `import { createRequire } from "node:module";\nconst { SwcJsMinimizerRspackPlugin } = createRequire(${JSON.stringify(path.join(projectRoot, "package.json"))})("@rspack/core");\nexport default { mode: "production", context: ${JSON.stringify(projectRoot)}, entry: "./src/esm/value.js", optimization: { minimizer: [new SwcJsMinimizerRspackPlugin({ minimizerOptions: { mangle: false } })] } };\n`,
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.defaultMinifier, false);
});

test("rspack configuration functions that throw are configuration load failures", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    'export default () => { throw new Error("factory broke"); };\n',
  );
  const { status, events } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_CONFIG_LOAD_FAILED");
  assert.match(error.message, /factory broke/);
});

test("vite configuration errors are configuration load failures", () => {
  const projectRoot = path.join(root, "fixtures/vite-app");
  const { dir, file } = writeConfig(
    "vite.config.mjs",
    'export default async () => { throw new Error("vite factory broke"); };\n',
  );
  const { status, events } = runAdapter(
    "vite",
    "vite-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_CONFIG_LOAD_FAILED");
  assert.match(error.message, /vite factory broke/);
});

test("rspack adapter records providedExports in the configuration summary", () => {
  const { dir, relative } = rspackConfig(
    'entry: "./src/esm/value.js", optimization: { providedExports: false }',
  );
  const { status, collection } = runAdapter("rspack", "rspack-app", relative);
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(
    collection.configSummary["optimization.providedExports"],
    "false",
  );
});

test("rspack adapter treats SWC with a disabled compression pass as non-default", () => {
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const { dir, file } = writeConfig(
    "rspack.config.mjs",
    `import { createRequire } from "node:module";\nconst { SwcJsMinimizerRspackPlugin } = createRequire(${JSON.stringify(path.join(projectRoot, "package.json"))})("@rspack/core");\nexport default { mode: "production", context: ${JSON.stringify(projectRoot)}, entry: "./src/esm/value.js", optimization: { minimizer: [new SwcJsMinimizerRspackPlugin({ minimizerOptions: { compress: { dead_code: false } } })] } };\n`,
  );
  const { status, collection } = runAdapter(
    "rspack",
    "rspack-app",
    path.relative(projectRoot, file),
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.defaultMinifier, false);
});

// Export usage collection (Rspack RsdoctorPlugin export usage graph).

function edgeOf(collection, consumerSuffix, providerSuffix) {
  return collection.exportUsage.edges.find(
    (edge) =>
      edge.consumer.module.endsWith(consumerSuffix) &&
      edge.provider.module.endsWith(providerSuffix),
  );
}

test("rspack adapter collects export usage edges for every import shape", () => {
  const { status, events, collection } = runAdapter(
    "rspack",
    "rspack-app",
    "rspack.config.mjs",
  );
  assert.equal(status, 0, JSON.stringify(events));
  assert.equal(collection.adapterVersion, "2");
  assert.equal(collection.bundlerPackage, "@rspack/core");
  const usage = collection.exportUsage;
  assert.equal(usage.available, true);
  assert.equal(usage.reason, null);
  assert.ok(usage.edges.length > 0);
  // Chain: named export consumed from the entry's module scope (null).
  const chain = edgeOf(collection, "src/main.js", "exports/chain-a.js");
  assert.equal(chain.consumer.export, null);
  assert.deepEqual(chain.provider.export, ["chainA"]);
  assert.equal(typeof chain.dependencyId, "string");
  assert.match(chain.location, /^\d+:\d+/);
  const inner = edgeOf(collection, "exports/chain-a.js", "exports/leaf.js");
  assert.deepEqual(inner.consumer.export, ["chainA"]);
  assert.deepEqual(inner.provider.export, ["leaf"]);
  // Branch: a second consumer of the same provider export.
  assert.ok(edgeOf(collection, "exports/chain-b.js", "exports/leaf.js"));
  // Namespace: the provider side has no named export.
  const namespace = edgeOf(
    collection,
    "exports/namespace.js",
    "exports/leaf.js",
  );
  assert.equal(namespace.provider.export, null);
  // Cycle: both directions are reported.
  assert.ok(edgeOf(collection, "exports/leaf.js", "exports/cycle-back.js"));
  assert.ok(edgeOf(collection, "exports/cycle-back.js", "exports/leaf.js"));
  // Re-export: Rspack reports the entry's use of leaf directly.
  assert.ok(edgeOf(collection, "src/main.js", "exports/leaf.js"));
  assert.ok(
    !usage.edges.some(
      (edge) =>
        edge.consumer.module.endsWith("reexport.js") ||
        edge.provider.module.endsWith("reexport.js"),
    ),
  );
  // Every module reference is a collected module.
  const ids = new Set(collection.modules.map((module) => module.id));
  for (const edge of usage.edges) {
    assert.ok(ids.has(edge.consumer.module));
    assert.ok(ids.has(edge.provider.module));
  }
});

test("rspack adapter records the added plugin as a collection override", () => {
  const { collection } = runAdapter(
    "rspack",
    "rspack-app",
    "rspack.config.mjs",
  );
  assert.ok(
    collection.overrides.some(
      (entry) =>
        entry.includes("RsdoctorPlugin") &&
        entry.includes("project's plugins are unchanged"),
    ),
  );
  // The engine stays the project's own installed Rspack.
  assert.equal(collection.bundlerPackage, "@rspack/core");
  assert.equal(collection.bundlerVersion, "2.2.9");
});

test("export usage collection leaves no source text in the output", () => {
  const work = fs.mkdtempSync(
    path.join(os.tmpdir(), "bundlewhy-adapter-test-"),
  );
  const projectRoot = path.join(root, "fixtures/rspack-app");
  const request = {
    projectRoot,
    configFile: path.join(projectRoot, "rspack.config.mjs"),
    configName: null,
    buildDir: path.join(work, "build"),
    collectPath: path.join(work, "collect.json"),
    supportedVersion: null,
    allowUnsupportedEngine: false,
    protocolToken: "test-token",
  };
  fs.writeFileSync(path.join(work, "request.json"), JSON.stringify(request));
  const result = spawnSync(
    process.execPath,
    [
      path.join(root, "../../crates/bundlewhy/adapters/rspack.mjs"),
      path.join(work, "request.json"),
    ],
    { cwd: projectRoot, encoding: "utf8" },
  );
  const collection = JSON.parse(fs.readFileSync(request.collectPath, "utf8"));
  fs.rmSync(work, { recursive: true, force: true });
  assert.equal(result.status, 0);
  // The fixture source `eval(code) ?? back()` must not be quoted anywhere an
  // export usage record or a protocol frame could carry it.
  assert.ok(!JSON.stringify(collection.exportUsage).includes("eval("));
  assert.ok(!result.stdout.includes("eval("));
  assert.ok(!result.stderr.includes("eval("));
});

const decodeIdentifiers = new Map([
  [1, "/p/a.js"],
  [2, "/p/b.js"],
  [3, "/p/container.js|abc"],
]);
const decodeCollected = new Set(["/p/a.js", "/p/b.js"]);

test("decodeExportUsageEdges handles every payload shape", () => {
  const decode = (raw) =>
    decodeExportUsageEdges(raw, decodeIdentifiers, decodeCollected);
  const good = [1, ["a"], 2, null, "10", "3:1-4"];
  const cases = [
    {
      name: "valid edge keeps null names and positional fields",
      raw: [good],
      expect: (result) => {
        assert.equal(result.available, true);
        assert.equal(result.skippedEdges, 0);
        assert.deepEqual(result.edges, [
          {
            consumer: { module: "/p/a.js", export: ["a"] },
            provider: { module: "/p/b.js", export: null },
            dependencyId: "10",
            location: "3:1-4",
          },
        ]);
      },
    },
    {
      name: "missing dependency id and location become null",
      raw: [[1, null, 2, ["b"], null, null]],
      expect: (result) => {
        assert.equal(result.edges[0].dependencyId, null);
        assert.equal(result.edges[0].location, null);
      },
    },
    {
      name: "payload that is not a list is unavailable",
      raw: { not: "a list" },
      expect: (result) => {
        assert.equal(result.available, false);
        assert.equal(result.reason, ExportUsageReason.EdgesPayloadInvalid);
        assert.deepEqual(result.edges, []);
      },
    },
    {
      name: "missing payload is unavailable",
      raw: undefined,
      expect: (result) => assert.equal(result.available, false),
    },
    {
      name: "tuple that is too short is skipped and counted",
      raw: [[1, ["a"], 2], good],
      expect: (result) => {
        assert.equal(result.skippedEdges, 1);
        assert.equal(result.edges.length, 1);
      },
    },
    {
      name: "unknown module key is skipped and counted",
      raw: [[1, ["a"], 99, null, "1", null]],
      expect: (result) => {
        assert.equal(result.skippedEdges, 1);
        assert.equal(result.edges.length, 0);
        assert.equal(result.available, true);
      },
    },
    {
      name: "concatenated container is skipped and counted",
      raw: [[3, ["a"], 2, null, "1", null]],
      expect: (result) => {
        assert.equal(result.skippedEdges, 1);
        assert.equal(result.edges.length, 0);
      },
    },
    {
      name: "empty export path is skipped and counted",
      raw: [[1, [], 2, null, "1", null]],
      expect: (result) => assert.equal(result.skippedEdges, 1),
    },
    {
      name: "export path with only an empty segment is skipped and counted",
      raw: [[1, [""], 2, null, "1", null]],
      expect: (result) => {
        assert.equal(result.skippedEdges, 1);
        assert.deepEqual(result.edges, []);
      },
    },
    {
      name: "provider export path with an empty segment is skipped and counted",
      raw: [[1, ["a"], 2, ["b", ""], "1", null], good],
      expect: (result) => {
        assert.equal(result.skippedEdges, 1);
        assert.equal(result.edges.length, 1);
      },
    },
    {
      name: "self edge is stored once",
      raw: [[1, ["a"], 1, ["a"], "1", null]],
      expect: (result) => {
        assert.equal(result.skippedEdges, 0);
        assert.equal(result.edges.length, 1);
      },
    },
    {
      name: "non-string export path segment is skipped and counted",
      raw: [[1, ["a", 7], 2, null, "1", null]],
      expect: (result) => assert.equal(result.skippedEdges, 1),
    },
    {
      name: "duplicate edges are stored once",
      raw: [good, [...good]],
      expect: (result) => {
        assert.equal(result.edges.length, 1);
        assert.equal(result.skippedEdges, 0);
      },
    },
    {
      name: "edges are sorted regardless of input order",
      raw: [
        [2, ["b"], 1, ["a"], "2", null],
        [1, ["a"], 2, ["b"], "1", null],
      ],
      expect: (result) => {
        assert.deepEqual(
          result.edges.map((edge) => edge.dependencyId),
          ["1", "2"],
        );
      },
    },
  ];
  for (const { name, raw, expect } of cases) {
    expect(decode(raw), name);
  }
});

test("decodeExportUsageEdges refuses graphs above the edge limit", () => {
  const raw = Array.from({ length: MAX_EXPORT_USAGE_EDGES + 1 }, (_, index) => [
    1,
    [`e${index}`],
    2,
    null,
    String(index),
    null,
  ]);
  const result = decodeExportUsageEdges(
    raw,
    decodeIdentifiers,
    decodeCollected,
  );
  assert.equal(result.available, false);
  assert.equal(result.reason, ExportUsageReason.EdgeLimitExceeded);
  assert.deepEqual(result.edges, []);
});

// Projects that use a wrapper around the real @rspack/core to simulate
// engines whose export usage API is missing or has a different shape. The
// build itself is always the real Rspack.

const realRspackEntry = createRequire(path.join(root, "package.json")).resolve(
  "@rspack/core",
);

function wrappedProject({ config = null } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-wrapped-"));
  fs.mkdirSync(path.join(dir, "src"));
  fs.writeFileSync(
    path.join(dir, "package.json"),
    JSON.stringify({ name: "wrapped", private: true, type: "module" }),
  );
  fs.writeFileSync(
    path.join(dir, "src/main.js"),
    'import { value } from "./value.js";\nconsole.log(value());\n',
  );
  fs.writeFileSync(
    path.join(dir, "src/value.js"),
    "export const value = () => 1;\n",
  );
  fs.writeFileSync(
    path.join(dir, "rspack.config.mjs"),
    config ??
      `export default { mode: "production", context: ${JSON.stringify(dir)}, entry: "./src/main.js" };\n`,
  );
  const core = path.join(dir, "node_modules/@rspack/core");
  fs.mkdirSync(core, { recursive: true });
  fs.writeFileSync(
    path.join(core, "package.json"),
    JSON.stringify({
      name: "@rspack/core",
      version: "2.2.9",
      main: "index.cjs",
    }),
  );
  fs.writeFileSync(
    path.join(core, "index.cjs"),
    `const real = require(${JSON.stringify(realRspackEntry)});
const mode = process.env.BUNDLEWHY_TEST_EXPORT_USAGE_MODE;
const Real = real.experiments.RsdoctorPlugin;
class Patched extends Real {
  constructor(...args) {
    if (mode === "construct-throws") throw new Error("boom construct\\nsecond line quotes source");
    super(...args);
  }
  apply(compiler) {
    if (mode === "apply-throws") throw new Error("boom apply\\nsecond line quotes source");
    if (mode === "compilation-hook-throws") {
      compiler.hooks.thisCompilation.tap("boom", () => {
        throw new Error("boom compilation\\nsecond line quotes source");
      });
    }
    return super.apply(compiler);
  }
}
Patched.getCompilationHooks = (compilation) => {
  if (mode === "hook-throws") throw new Error("boom\\nsecond line quotes source");
  if (mode === "hook-missing") return {};
  if (mode === "never-called") return { moduleGraph: { tapPromise() {} } };
  const hooks = Real.getCompilationHooks(compilation);
  if (mode === "no-edges-field") {
    return {
      moduleGraph: {
        tapPromise(name, fn) {
          hooks.moduleGraph.tapPromise(name, async (graph) =>
            fn({ modules: graph.modules }),
          );
        },
      },
    };
  }
  if (mode === "no-modules-field") {
    return {
      moduleGraph: {
        tapPromise(name, fn) {
          hooks.moduleGraph.tapPromise(name, async (graph) =>
            fn({ exportUsageEdges: graph.exportUsageEdges }),
          );
        },
      },
    };
  }
  return hooks;
};
const dumpPath = process.env.BUNDLEWHY_TEST_CONFIG_DUMP;
const describeEntry = (entry) =>
  entry === null || (typeof entry !== "object" && typeof entry !== "function")
    ? String(entry)
    : (entry.name ?? entry.constructor?.name ?? "anonymous");
const describePlugins = (value) => {
  if (value === undefined) return "undefined";
  if (value === null) return "null";
  if (Array.isArray(value)) return value.map(describeEntry);
  return { type: typeof value };
};
module.exports = new Proxy(real, {
  get(target, key) {
    if (key === "rspack" && dumpPath) {
      // Records the plugins value that the engine receives on every build.
      return Object.assign((config, ...rest) => {
        require("node:fs").appendFileSync(
          dumpPath,
          JSON.stringify({ plugins: describePlugins(config.plugins) }) + "\\n",
        );
        return target.rspack(config, ...rest);
      }, target.rspack);
    }
    if (key === "experiments") {
      return {
        ...target.experiments,
        RsdoctorPlugin: mode === "no-plugin" ? undefined : Patched,
      };
    }
    return target[key];
  },
});
`,
  );
  return dir;
}

function runWrapped(dir, mode, extra = {}) {
  const dumpFile = path.join(dir, "config-dump.jsonl");
  const result = runAdapter(
    "rspack",
    "rspack-app",
    "rspack.config.mjs",
    {
      projectRoot: dir,
      configFile: path.join(dir, "rspack.config.mjs"),
      ...extra,
    },
    {
      BUNDLEWHY_TEST_EXPORT_USAGE_MODE: mode,
      BUNDLEWHY_TEST_CONFIG_DUMP: dumpFile,
    },
  );
  // One entry per engine build: the plugins value that Rspack received.
  const calls = fs.existsSync(dumpFile)
    ? fs
        .readFileSync(dumpFile, "utf8")
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line).plugins)
    : [];
  return { ...result, calls };
}

test("rspack adapter works through the wrapped engine when the API is intact", () => {
  const dir = wrappedProject();
  const { status, collection } = runWrapped(dir, "intact");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.exportUsage.available, true);
  assert.ok(
    collection.exportUsage.edges.some((edge) =>
      edge.provider.module.endsWith("src/value.js"),
    ),
  );
});

// Every adapter failure or drift path: the export usage reason code and
// whether the final build used the plugin. The build always succeeds.
const adapterOutcomes = [
  ["no-plugin", ExportUsageReason.PluginApiMissing, false],
  ["construct-throws", ExportUsageReason.PluginConstructionFailed, false],
  ["apply-throws", ExportUsageReason.PluginApplyFailed, true],
  ["hook-missing", ExportUsageReason.HookRegistrationFailed, true],
  ["hook-throws", ExportUsageReason.HookRegistrationFailed, true],
  ["compilation-hook-throws", ExportUsageReason.BuildFailedWithObserver, false],
  ["never-called", ExportUsageReason.GraphNotDelivered, true],
  ["no-edges-field", ExportUsageReason.EdgesPayloadInvalid, true],
  ["no-modules-field", ExportUsageReason.ModulesPayloadInvalid, true],
];

for (const [mode, reason, addsPlugin] of adapterOutcomes) {
  test(`rspack adapter reports unavailable export usage when ${mode}`, () => {
    const dir = wrappedProject();
    const { status, events, collection } = runWrapped(dir, mode);
    fs.rmSync(dir, { recursive: true, force: true });
    // The build and the rest of the collection are unaffected.
    assert.equal(status, 0, JSON.stringify(events));
    assert.ok(
      collection.modules.some((module) => module.path?.endsWith("value.js")),
    );
    assert.equal(collection.exportUsage.available, false);
    assert.equal(collection.exportUsage.reason, reason);
    assert.deepEqual(collection.exportUsage.edges, []);
    assert.equal(
      collection.overrides.some((entry) => entry.includes("RsdoctorPlugin")),
      addsPlugin,
    );
    // The reason is a code from the fixed set and no error text is copied
    // into the collection or the protocol frames.
    assert.ok(Object.values(ExportUsageReason).includes(reason));
    const everything = JSON.stringify({ collection, events });
    assert.ok(!everything.includes("boom"), mode);
    assert.ok(!everything.includes("second line"), mode);
  });
}

test("every export usage reason code has an adapter outcome test or a payload test", () => {
  const covered = new Set(adapterOutcomes.map(([, reason]) => reason));
  covered.add(ExportUsageReason.PluginAlreadyConfigured);
  covered.add(ExportUsageReason.PluginsNotArray);
  covered.add(ExportUsageReason.EdgeLimitExceeded);
  assert.deepEqual(
    [...covered].sort(),
    Object.values(ExportUsageReason).sort(),
  );
});

test("a failing project build is still reported after the observer repeat", () => {
  const dir = wrappedProject();
  fs.writeFileSync(path.join(dir, "src/main.js"), "import { from ;\n");
  const { status, events, collection } = runWrapped(dir, "intact");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.notEqual(status, 0);
  assert.equal(collection, null);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_BUILD_FAILED");
});

test("rspack adapter does not add a second RsdoctorPlugin to the project", () => {
  const dir = wrappedProject({
    config: `import { createRequire } from "node:module";
const { experiments } = createRequire(import.meta.url)("@rspack/core");
export default {
  mode: "production",
  context: ${JSON.stringify("PLACEHOLDER")},
  entry: "./src/main.js",
  plugins: [new experiments.RsdoctorPlugin({ moduleGraphFeatures: false, chunkGraphFeatures: false })],
};
`.replace('"PLACEHOLDER"', "import.meta.dirname"),
  });
  const { status, events, collection } = runWrapped(dir, "intact");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0, JSON.stringify(events));
  assert.equal(collection.exportUsage.available, false);
  assert.equal(
    collection.exportUsage.reason,
    ExportUsageReason.PluginAlreadyConfigured,
  );
  assert.ok(
    !collection.overrides.some((entry) => entry.includes("RsdoctorPlugin")),
  );
});

// Every shape of `plugins` and what the engine receives. A list gets the
// observer appended after the project's own entries (a new list; the project's
// list is never changed). Any other value is passed through unchanged and
// export usage is unavailable with a fixed reason. Mirrors the "Plugin
// configuration" table in the contract.
const probePreamble = 'const probe = { name: "project-probe", apply() {} };\n';
const OBSERVER = ["Object", "Object"];
const pluginShapes = [
  {
    name: "no plugins key",
    config: "",
    received: OBSERVER,
    available: true,
  },
  {
    name: "undefined",
    config: "plugins: undefined,",
    received: OBSERVER,
    available: true,
  },
  {
    name: "empty list",
    config: "plugins: [],",
    received: OBSERVER,
    available: true,
  },
  {
    name: "list of plugins",
    config: "plugins: [probe],",
    received: ["project-probe", ...OBSERVER],
    available: true,
  },
  {
    name: "list with falsy entries",
    config: "plugins: [false, null, probe],",
    received: ["false", "null", "project-probe", ...OBSERVER],
    available: true,
  },
  {
    name: "frozen list",
    config: "plugins: Object.freeze([probe]),",
    received: ["project-probe", ...OBSERVER],
    available: true,
  },
  {
    name: "null",
    config: "plugins: null,",
    received: "null",
    reason: ExportUsageReason.PluginsNotArray,
  },
  {
    name: "a single plugin instance",
    config: "plugins: probe,",
    received: { type: "object" },
    reason: ExportUsageReason.PluginsNotArray,
  },
  {
    name: "a function",
    config: "plugins: function plugin() {},",
    received: { type: "function" },
    reason: ExportUsageReason.PluginsNotArray,
  },
  {
    name: "a string",
    config: 'plugins: "plugin",',
    received: { type: "string" },
    reason: ExportUsageReason.PluginsNotArray,
  },
];

function pluginConfig(body) {
  return `${probePreamble}export default {
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
  ${body}
};
`;
}

for (const shape of pluginShapes) {
  test(`rspack adapter passes plugins (${shape.name}) to the engine as documented`, () => {
    const dir = wrappedProject({ config: pluginConfig(shape.config) });
    const { status, events, collection, calls } = runWrapped(dir, "intact");
    fs.rmSync(dir, { recursive: true, force: true });
    assert.equal(status, 0, JSON.stringify(events));
    assert.deepEqual(calls, [shape.received]);
    const attached = shape.available === true;
    assert.equal(collection.exportUsage.available, attached);
    assert.equal(collection.exportUsage.reason, shape.reason ?? null);
    assert.equal(
      collection.overrides.some((entry) => entry.includes("RsdoctorPlugin")),
      attached,
    );
    // The build itself is unaffected either way.
    assert.ok(
      collection.modules.some((module) => module.path?.endsWith("value.js")),
    );
  });
}

test("a plugins list that Rspack rejects is rejected for the project's value, not the observer's", () => {
  const dir = wrappedProject({ config: pluginConfig("plugins: [[probe]],") });
  const { status, events, collection, calls } = runWrapped(dir, "intact");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.notEqual(status, 0);
  assert.equal(collection, null);
  assert.equal(
    events.find((event) => event.event === "error").code,
    "BW_BUILD_FAILED",
  );
  // First attempt with the observer, then the repeat with the project's own
  // value, which is what decides the result.
  assert.deepEqual(calls, [["Array", ...OBSERVER], ["Array"]]);
});

test("only the selected build of a multi-build configuration is changed", () => {
  const config = `${probePreamble}const base = {
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
};
export default [
  { ...base, name: "first", plugins: null },
  { ...base, name: "second", plugins: [probe] },
  { ...base, name: "third", plugins: Object.freeze([probe]) },
];
`;
  const expected = {
    first: { received: "null", available: false },
    second: { received: ["project-probe", ...OBSERVER], available: true },
    third: { received: ["project-probe", ...OBSERVER], available: true },
  };
  for (const [name, { received, available }] of Object.entries(expected)) {
    const dir = wrappedProject({ config });
    const result = runWrapped(dir, "intact", { configName: name });
    fs.rmSync(dir, { recursive: true, force: true });
    assert.equal(result.status, 0, name);
    // Exactly one build: the unselected builds are never started.
    assert.deepEqual(result.calls, [received], name);
    assert.equal(result.collection.exportUsage.available, available, name);
  }
});
