import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  MangleSetting,
  normalizeCollectionRoot,
  normalizeViteModule,
  resolveMangleSetting,
  resolveNormalizedMangleSetting,
} from "../../../crates/bundlewhy/adapters/vite.mjs";
import {
  ExportUsageReason,
  MAX_EXPORT_USAGE_EDGES,
  SkippedProviderScope,
  decodeExportUsageEdges,
} from "../../../crates/bundlewhy/adapters/common.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function runAdapter(bundler, project, config, extra = {}, env = {}) {
  const work = fs.mkdtempSync(
    path.join(os.tmpdir(), "bundlewhy-adapter-test-"),
  );
  const projectRoot = path.resolve(root, "fixtures", project);
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
      path.join(root, "test/adapter-supervisor.mjs"),
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

test("vite collection root follows resolver symlink policy", () => {
  if (process.platform === "win32") return;
  const work = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-root-"));
  const realRoot = path.join(work, "real");
  const lexicalRoot = path.join(work, "linked");
  fs.mkdirSync(realRoot);
  fs.symlinkSync(realRoot, lexicalRoot, "dir");

  assert.equal(normalizeCollectionRoot(lexicalRoot), fs.realpathSync(realRoot));
  assert.equal(normalizeCollectionRoot(lexicalRoot, true), lexicalRoot);
  const missingRoot = path.join(work, "missing");
  assert.equal(normalizeCollectionRoot(missingRoot), missingRoot);

  fs.rmSync(work, { recursive: true, force: true });
});

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

// Mirrors the "Name mangling" rows of the Vite collection contract: Vite
// lets an explicit `output.minify` replace the value it derives from
// `build.minify`, and Rolldown mangles unless the effective option disables it.
const mangleShapes = [
  {
    name: "absent output with oxc",
    minify: "oxc",
    output: undefined,
    expected: MangleSetting.Enabled,
  },
  {
    name: "absent output with minify false",
    minify: false,
    output: undefined,
    expected: MangleSetting.Disabled,
  },
  {
    name: "output without minify",
    minify: "oxc",
    output: {},
    expected: MangleSetting.Enabled,
  },
  {
    name: "output.minify false",
    minify: "oxc",
    output: { minify: false },
    expected: MangleSetting.Disabled,
  },
  {
    name: "output.minify dce-only",
    minify: "oxc",
    output: { minify: "dce-only" },
    expected: MangleSetting.Disabled,
  },
  {
    name: "output.minify true",
    minify: "oxc",
    output: { minify: true },
    expected: MangleSetting.Enabled,
  },
  {
    name: "mangle false",
    minify: "oxc",
    output: { minify: { compress: true, mangle: false, codegen: true } },
    expected: MangleSetting.Disabled,
  },
  {
    name: "mangle true",
    minify: "oxc",
    output: { minify: { mangle: true } },
    expected: MangleSetting.Enabled,
  },
  {
    name: "mangle options object",
    minify: "oxc",
    output: { minify: { mangle: { toplevel: true } } },
    expected: MangleSetting.Enabled,
  },
  {
    name: "options object without mangle",
    minify: "oxc",
    output: { minify: { compress: true } },
    expected: MangleSetting.Enabled,
  },
  {
    name: "single-entry array with mangle false",
    minify: "oxc",
    output: [{ minify: { mangle: false } }],
    expected: MangleSetting.Disabled,
  },
  {
    name: "single-entry array with mangle true",
    minify: "oxc",
    output: [{ minify: { mangle: true } }],
    expected: MangleSetting.Enabled,
  },
  {
    name: "array with one mangling entry",
    minify: "oxc",
    output: [{ minify: false }, { minify: true }],
    expected: MangleSetting.Enabled,
  },
  {
    name: "unrecognized value",
    minify: "oxc",
    output: { minify: "full" },
    expected: MangleSetting.Enabled,
  },
];

for (const shape of mangleShapes) {
  test(`vite mangle setting (${shape.name})`, () => {
    assert.equal(
      resolveMangleSetting(shape.minify, shape.output),
      shape.expected,
    );
  });
}

for (const [name, output, expected] of [
  [
    "mangle false",
    "{ minify: { compress: true, mangle: false, codegen: true } }",
    MangleSetting.Disabled,
  ],
  ["dce-only", "{ minify: 'dce-only' }", MangleSetting.Disabled],
  ["mangle true", "{ minify: { mangle: true } }", MangleSetting.Enabled],
  ["absent", "{}", MangleSetting.Enabled],
]) {
  test(`vite adapter collects the effective mangle setting (${name})`, () => {
    const projectRoot = path.join(root, "fixtures/vite-app");
    const { dir, file } = writeConfig(
      "vite.config.mjs",
      `export default { build: { minify: 'oxc', rolldownOptions: { output: ${output} } } };\n`,
    );
    const { status, events, collection } = runAdapter(
      "vite",
      "vite-app",
      path.relative(projectRoot, file),
    );
    fs.rmSync(dir, { recursive: true, force: true });
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(collection.mangle, expected);
    assert.equal(
      collection.configSummary["build.rolldownOptions.output.minify.mangle"],
      expected,
    );
    assert.equal(collection.defaultMinifier, true);
    assert.equal(collection.minifyEnabled, true);
  });
}

// The normalized `minify` option that `generateBundle` receives is what
// Rolldown applies after every plugin's `outputOptions` hook ran: `mangle` is
// only present when mangling runs.
for (const [name, normalized, expected] of [
  ["false", false, MangleSetting.Disabled],
  ["dce-only", "dce-only", MangleSetting.Disabled],
  ["object without mangle", { compress: {} }, MangleSetting.Disabled],
  ["mangle false", { mangle: false }, MangleSetting.Disabled],
  ["mangle options", { mangle: { toplevel: true } }, MangleSetting.Enabled],
  ["undefined", undefined, null],
  ["true", true, null],
  ["unrecognized string", "full", null],
]) {
  test(`vite normalized mangle setting (${name})`, () => {
    assert.equal(resolveNormalizedMangleSetting(normalized), expected);
  });
}

// A plugin's `outputOptions` hook runs after `configResolved`, so the
// collected mangle setting must come from the options the build applies.
for (const [name, buildOutput, hookMinify, expected] of [
  [
    "hook disables mangle over an enabled config",
    "{}",
    "{ mangle: false }",
    MangleSetting.Disabled,
  ],
  [
    "hook sets minify false",
    "{}",
    "false",
    MangleSetting.Disabled,
  ],
  [
    "hook enables mangle over a disabled config",
    "{ minify: { mangle: false } }",
    "{ mangle: true }",
    MangleSetting.Enabled,
  ],
]) {
  test(`vite adapter reads the mangle setting after outputOptions hooks (${name})`, () => {
    const projectRoot = path.join(root, "fixtures/vite-app");
    const { dir, file } = writeConfig(
      "vite.config.mjs",
      `export default {
  build: { minify: 'oxc', rolldownOptions: { output: ${buildOutput} } },
  plugins: [{
    name: 'test:set-minify',
    outputOptions(options) {
      options.minify = ${hookMinify};
      return options;
    },
  }],
};\n`,
    );
    const { status, events, collection } = runAdapter(
      "vite",
      "vite-app",
      path.relative(projectRoot, file),
    );
    fs.rmSync(dir, { recursive: true, force: true });
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(collection.mangle, expected);
    assert.equal(
      collection.configSummary["build.rolldownOptions.output.minify.mangle"],
      expected,
    );
  });
}

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

// Builds a project whose `vite` package has Vite 5's dual entry shape: the
// `import` condition points to an ESM entry with named exports and the
// `require` condition (the one `require.resolve` selects) points to a CommonJS
// entry that exposes the API only through its default export.
function writeFakeViteProject({ cjsApi, version = "5.0.12" }) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-fake-vite-"));
  const vite = path.join(dir, "node_modules", "vite");
  fs.mkdirSync(path.join(vite, "dist"), { recursive: true });
  fs.writeFileSync(
    path.join(dir, "package.json"),
    JSON.stringify({ type: "module" }),
  );
  fs.writeFileSync(
    path.join(vite, "package.json"),
    JSON.stringify({
      name: "vite",
      version,
      exports: {
        ".": { import: "./dist/index.js", require: "./index.cjs" },
        "./package.json": "./package.json",
      },
    }),
  );
  fs.writeFileSync(
    path.join(vite, "dist/index.js"),
    'export const version = "esm";\n',
  );
  fs.writeFileSync(
    path.join(vite, "index.cjs"),
    `const api = ${cjsApi};\nmodule.exports = api;\n`,
  );
  fs.writeFileSync(path.join(dir, "vite.config.mjs"), "export default {};\n");
  return dir;
}

const FAKE_VITE_API_BODY = {
  loadConfigFromFile:
    'async () => { if (process.env.FAKE_VITE_CONFIG_FAILS) throw new Error("factory broke"); return { config: {} }; }',
  build: 'async () => { throw new Error("fake vite build reached"); }',
  mergeConfig: "(a, b) => ({ ...a, ...b })",
  createLogger: "() => ({})",
};

function fakeViteApi(overrides = {}) {
  const entries = { ...FAKE_VITE_API_BODY, ...overrides };
  return `{ version: "5.0.12", ${Object.entries(entries)
    .filter(([, value]) => value !== null)
    .map(([name, value]) => `${name}: ${value}`)
    .join(", ")} }`;
}

function runFakeVite(dir, extra = {}, env = {}) {
  return runAdapter(
    "vite",
    dir,
    "vite.config.mjs",
    {
      supportedVersion: "8.3.4",
      allowUnsupportedEngine: true,
      ...extra,
    },
    env,
  );
}

test("normalizeViteModule prefers named exports and falls back to default", () => {
  const named = () => "named";
  const fallback = () => "default";
  const complete = (fn) => ({
    loadConfigFromFile: fn,
    build: fn,
    mergeConfig: fn,
    createLogger: fn,
    version: "1.0.0",
  });
  const mixed = normalizeViteModule({
    ...complete(named),
    default: complete(fallback),
  });
  assert.equal(mixed.missing.length, 0);
  assert.equal(mixed.api.build, named);

  const defaultOnly = normalizeViteModule({ default: complete(fallback) });
  assert.equal(defaultOnly.missing.length, 0);
  assert.equal(defaultOnly.api.build, fallback);
  assert.equal(defaultOnly.api.version, "1.0.0");

  const incomplete = normalizeViteModule({
    default: { loadConfigFromFile: fallback, version: "5.0.0" },
  });
  assert.deepEqual(incomplete.missing, [
    "build",
    "mergeConfig",
    "createLogger",
  ]);
  assert.deepEqual(normalizeViteModule(undefined).missing.length, 4);
});

test("vite adapter normalizes a CommonJS entry that exposes the API under default", () => {
  const dir = writeFakeViteProject({ cjsApi: fakeViteApi() });
  const { status, events } = runFakeVite(dir);
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  // The adapter got past loadConfigFromFile and reached vite.build.
  assert.equal(error.code, "BW_BUILD_FAILED");
  assert.match(error.message, /fake vite build reached/);
});

test("vite adapter reports a missing Vite API as a collection failure", () => {
  const dir = writeFakeViteProject({
    cjsApi: fakeViteApi({ loadConfigFromFile: null }),
  });
  const { status, events } = runFakeVite(dir);
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_COLLECTION_FAILED");
  assert.match(error.message, /Vite 5\.0\.12/);
  assert.match(error.message, /loadConfigFromFile/);
  assert.match(error.recovery, /vite@8\.3\.4/);
  assert.match(error.recovery, /compatible/);
  assert.doesNotMatch(
    error.recovery,
    /Fix the error in the Vite configuration/,
  );
});

test("vite adapter does not report a missing build API as a build failure", () => {
  const dir = writeFakeViteProject({
    cjsApi: fakeViteApi({ build: null }),
  });
  const { status, events } = runFakeVite(dir, { configFile: null });
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_COLLECTION_FAILED");
  assert.match(error.message, /missing: build/);
});

test("vite configuration factories that throw stay configuration load failures", () => {
  const dir = writeFakeViteProject({ cjsApi: fakeViteApi() });
  const { status, events } = runFakeVite(
    dir,
    {},
    { FAKE_VITE_CONFIG_FAILS: "1" },
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error.code, "BW_CONFIG_LOAD_FAILED");
  assert.match(error.message, /factory broke/);
  assert.match(error.recovery, /Fix the error in the Vite configuration/);
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
        assert.deepEqual(result.skippedProviders, []);
        assert.equal(result.unattributedEdges, 0);
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

// Attribution of skipped edges: which provider lost an earlier edge. Mirrors
// the "Edge classification" table in the contract.
test("decodeExportUsageEdges attributes every skipped edge to the provider that lost it", () => {
  const decode = (raw) =>
    decodeExportUsageEdges(raw, decodeIdentifiers, decodeCollected);
  const good = [1, ["a"], 2, null, "10", "3:1-4"];
  const moduleScope = (module, edges) => ({
    module,
    scope: SkippedProviderScope.Module,
    export: [],
    edges,
  });
  const named = (module, exportPath, edges) => ({
    module,
    scope: SkippedProviderScope.Named,
    export: exportPath,
    edges,
  });
  const unreadable = (module, edges) => ({
    module,
    scope: SkippedProviderScope.Unknown,
    export: [],
    edges,
  });
  const cases = [
    {
      name: "stored edges are not skipped",
      raw: [good],
      skipped: 0,
      providers: [],
      unattributed: 0,
    },
    {
      name: "consumer module key is unknown: the collected provider lost an edge",
      raw: [[99, ["a"], 2, null, "1", null]],
      skipped: 1,
      providers: [moduleScope("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "consumer is a concatenated container: the collected provider lost an edge",
      raw: [[3, ["a"], 2, null, "1", null]],
      skipped: 1,
      providers: [moduleScope("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "consumer export path is invalid: the collected provider lost an edge",
      raw: [[1, [""], 2, null, "1", null]],
      skipped: 1,
      providers: [moduleScope("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "provider export path is invalid: the collected provider lost an edge into an unreadable export",
      raw: [[1, ["a"], 2, ["b", ""], "1", null]],
      skipped: 1,
      providers: [unreadable("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "short tuple with a collected provider key names no export",
      raw: [[1, ["a"], 2]],
      skipped: 1,
      providers: [unreadable("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "provider is a concatenated container: no chain through a collected module can end at it",
      raw: [[1, ["a"], 3, null, "1", null]],
      skipped: 1,
      providers: [],
      unattributed: 0,
    },
    {
      name: "provider module key is unknown: it may hide an earlier edge anywhere",
      raw: [[1, ["a"], 99, null, "1", null]],
      skipped: 1,
      providers: [],
      unattributed: 1,
    },
    {
      name: "tuple that is not a list may hide an earlier edge anywhere",
      raw: ["not a tuple", null, 7],
      skipped: 3,
      providers: [],
      unattributed: 3,
    },
    {
      name: "providers are counted per module and sorted",
      raw: [
        [99, ["a"], 2, null, "1", null],
        [99, ["a"], 1, null, "2", null],
        [98, ["a"], 2, null, "3", null],
        good,
      ],
      skipped: 3,
      providers: [moduleScope("/p/a.js", 1), moduleScope("/p/b.js", 2)],
      unattributed: 0,
    },
    {
      name: "a readable provider export path is kept",
      raw: [[99, ["a"], 2, ["x", "y"], "1", null]],
      skipped: 1,
      providers: [named("/p/b.js", ["x", "y"], 1)],
      unattributed: 0,
    },
    {
      name: "a non-string provider export segment is unreadable",
      raw: [[99, ["a"], 2, ["x", 7], "1", null]],
      skipped: 1,
      providers: [unreadable("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "a provider export that is not a list or null is unreadable",
      raw: [[99, ["a"], 2, "x", "1", null]],
      skipped: 1,
      providers: [unreadable("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "an empty provider export list is unreadable",
      raw: [[99, ["a"], 2, [], "1", null]],
      skipped: 1,
      providers: [unreadable("/p/b.js", 1)],
      unattributed: 0,
    },
    {
      name: "different exports of one module are counted separately",
      raw: [
        [99, ["a"], 2, ["x"], "1", null],
        [98, ["a"], 2, ["x"], "2", null],
        [97, ["a"], 2, ["y"], "3", null],
        [96, ["a"], 2, null, "4", null],
        [95, ["a"], 2, [""], "5", null],
      ],
      skipped: 5,
      providers: [
        moduleScope("/p/b.js", 1),
        named("/p/b.js", ["x"], 2),
        named("/p/b.js", ["y"], 1),
        unreadable("/p/b.js", 1),
      ],
      unattributed: 0,
    },
  ];
  for (const { name, raw, skipped, providers, unattributed } of cases) {
    const result = decode(raw);
    assert.equal(result.available, true, name);
    assert.equal(result.skippedEdges, skipped, name);
    assert.deepEqual(result.skippedProviders, providers, name);
    assert.equal(result.unattributedEdges, unattributed, name);
    // Every skipped edge is accounted for at most once.
    assert.ok(
      result.skippedProviders.reduce((sum, entry) => sum + entry.edges, 0) +
        result.unattributedEdges <=
        result.skippedEdges,
      name,
    );
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
  assert.deepEqual(result.skippedProviders, []);
  assert.equal(result.unattributedEdges, 0);
});

// Mirrors the edge limit table in the contract: the decoder returns the
// unavailable result at the first distinct edge beyond the limit and never
// reads the rest of the payload.
test("decodeExportUsageEdges stops reading at the first edge beyond the limit", () => {
  let tailReads = 0;
  const tuple = (index) => [1, [`e${index}`], 2, null, String(index), null];
  const tail = () =>
    new Proxy([1, ["tail"], 2, null, "tail", null], {
      get(target, key, receiver) {
        tailReads += 1;
        return Reflect.get(target, key, receiver);
      },
    });
  const raw = [
    ...Array.from({ length: MAX_EXPORT_USAGE_EDGES + 1 }, (_, index) =>
      tuple(index),
    ),
    ...Array.from({ length: 1000 }, tail),
  ];
  const result = decodeExportUsageEdges(
    raw,
    decodeIdentifiers,
    decodeCollected,
  );
  assert.equal(result.available, false);
  assert.equal(result.reason, ExportUsageReason.EdgeLimitExceeded);
  assert.equal(tailReads, 0);
});

test("decodeExportUsageEdges keeps a graph at the edge limit and ignores repeats beyond it", () => {
  const tuple = (index) => [1, [`e${index}`], 2, null, String(index), null];
  const distinct = Array.from({ length: MAX_EXPORT_USAGE_EDGES }, (_, index) =>
    tuple(index),
  );
  // Repeats of stored edges and dropped edges add no distinct edge.
  const raw = [
    ...distinct,
    tuple(0),
    tuple(1),
    [999, null, 2, null, "x", null],
  ];
  const result = decodeExportUsageEdges(
    raw,
    decodeIdentifiers,
    decodeCollected,
  );
  assert.equal(result.available, true);
  assert.equal(result.edges.length, MAX_EXPORT_USAGE_EDGES);
  assert.equal(result.skippedEdges, 1);
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

// The repeat build starts from a freshly loaded configuration, so project
// state that a compiler uses up (a plugin instance that rejects a second
// compiler) is new. The plugin below fails when it is applied a second time.
// Mirrors the "Repeat build" table in the contract.
const singleUseBody = `class SingleUsePlugin {
  apply() {
    if (this.applied) throw new Error("single-use plugin applied twice");
    this.applied = true;
  }
}
`;

function loadCount(dir, name) {
  const file = path.join(dir, name);
  return fs.existsSync(file) ? fs.readFileSync(file, "utf8").length : 0;
}

const repeatShapes = [
  {
    name: "ES module object",
    file: "rspack.config.mjs",
    source: (dir) => `import fs from "node:fs";
fs.appendFileSync(${JSON.stringify(path.join(dir, "loads.txt"))}, "x");
${singleUseBody}export default {
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
  plugins: [new SingleUsePlugin()],
};
`,
  },
  {
    name: "CommonJS object",
    file: "rspack.config.cjs",
    source: (
      dir,
    ) => `require("node:fs").appendFileSync(${JSON.stringify(path.join(dir, "loads.txt"))}, "x");
${singleUseBody}module.exports = {
  mode: "production",
  context: __dirname,
  entry: "./src/main.js",
  plugins: [new SingleUsePlugin()],
};
`,
  },
  {
    name: "configuration function",
    file: "rspack.config.mjs",
    source: (dir) => `import fs from "node:fs";
fs.appendFileSync(${JSON.stringify(path.join(dir, "loads.txt"))}, "x");
${singleUseBody}export default () => ({
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
  plugins: [new SingleUsePlugin()],
});
`,
  },
];

for (const shape of repeatShapes) {
  test(`the repeat build gets fresh project plugins (${shape.name})`, () => {
    const dir = wrappedProject();
    fs.writeFileSync(path.join(dir, shape.file), shape.source(dir));
    const { status, events, collection, calls } = runWrapped(
      dir,
      "compilation-hook-throws",
      { configFile: path.join(dir, shape.file) },
    );
    const loads = loadCount(dir, "loads.txt");
    fs.rmSync(dir, { recursive: true, force: true });
    // The observer build fails, the repeat succeeds with a new plugin
    // instance and the project's build is not reported as failed.
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(collection.exportUsage.available, false);
    assert.equal(
      collection.exportUsage.reason,
      ExportUsageReason.BuildFailedWithObserver,
    );
    assert.deepEqual(calls, [
      ["SingleUsePlugin", ...OBSERVER],
      ["SingleUsePlugin"],
    ]);
    assert.equal(loads, 2);
    assert.ok(
      !collection.overrides.some((entry) => entry.includes("RsdoctorPlugin")),
    );
  });
}

// A configuration may answer differently on every load (a function reading
// the environment, a file that changed). Every value that describes the
// collected build comes from the configuration that produced the build.
// Mirrors the "Repeat build" derived-values table in the contract.
const repeatDerivedShapes = [
  {
    name: "first node and unminified, repeat web and default minifier",
    first: `{ target: "node", optimization: { minimize: false } }`,
    second: `{ target: "web" }`,
    expected: {
      ssrBuild: false,
      minifyEnabled: true,
      defaultMinifier: true,
      target: "web",
      minimizer: "default",
    },
  },
  {
    name: "first web and default minifier, repeat node and unminified",
    first: `{ target: "web" }`,
    second: `{ target: "node", optimization: { minimize: false } }`,
    expected: {
      ssrBuild: true,
      minifyEnabled: false,
      defaultMinifier: true,
      target: "node",
      minimizer: "default",
    },
  },
  {
    name: "first and repeat agree",
    first: `{ target: "node" }`,
    second: `{ target: "node" }`,
    expected: {
      ssrBuild: true,
      minifyEnabled: true,
      defaultMinifier: true,
      target: "node",
      minimizer: "default",
    },
  },
];

for (const shape of repeatDerivedShapes) {
  test(`the repeat build reports its own configuration (${shape.name})`, () => {
    const dir = wrappedProject();
    fs.writeFileSync(
      path.join(dir, "rspack.config.mjs"),
      `import fs from "node:fs";
const marker = ${JSON.stringify(path.join(dir, "loaded.txt"))};
const second = fs.existsSync(marker);
fs.writeFileSync(marker, "x");
const first = ${shape.first};
const repeat = ${shape.second};
export default {
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
  ...(second ? repeat : first),
};
`,
    );
    const { status, events, collection } = runWrapped(
      dir,
      "compilation-hook-throws",
    );
    fs.rmSync(dir, { recursive: true, force: true });
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(
      collection.exportUsage.reason,
      ExportUsageReason.BuildFailedWithObserver,
    );
    assert.equal(collection.ssrBuild, shape.expected.ssrBuild);
    assert.equal(collection.minifyEnabled, shape.expected.minifyEnabled);
    assert.equal(collection.defaultMinifier, shape.expected.defaultMinifier);
    assert.equal(collection.configSummary.target, shape.expected.target);
    assert.equal(
      collection.configSummary["optimization.minimizer"],
      shape.expected.minimizer,
    );
  });
}

test("a build without the observer loads the configuration once", () => {
  const dir = wrappedProject();
  const source = repeatShapes[0].source(dir);
  fs.writeFileSync(path.join(dir, "rspack.config.mjs"), source);
  const { status, events, calls } = runWrapped(dir, "intact");
  const loads = loadCount(dir, "loads.txt");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0, JSON.stringify(events));
  assert.equal(calls.length, 1);
  assert.equal(loads, 1);
});

test("an entry function is called once even when the build is repeated", () => {
  const dir = wrappedProject();
  fs.writeFileSync(
    path.join(dir, "rspack.config.mjs"),
    `import fs from "node:fs";
${singleUseBody}export default {
  mode: "production",
  context: import.meta.dirname,
  entry: () => {
    fs.appendFileSync(${JSON.stringify(path.join(dir, "entry-calls.txt"))}, "x");
    return "./src/main.js";
  },
  plugins: [new SingleUsePlugin()],
};
`,
  );
  const { status, events, collection } = runWrapped(
    dir,
    "compilation-hook-throws",
  );
  const calls = loadCount(dir, "entry-calls.txt");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0, JSON.stringify(events));
  assert.equal(calls, 1);
  assert.equal(
    collection.exportUsage.reason,
    ExportUsageReason.BuildFailedWithObserver,
  );
  assert.ok(
    collection.overrides.includes("entry function resolved once by bundlewhy"),
  );
});

test("a configuration that cannot be loaded again is a configuration failure", () => {
  const dir = wrappedProject();
  fs.writeFileSync(
    path.join(dir, "rspack.config.mjs"),
    `import fs from "node:fs";
const marker = ${JSON.stringify(path.join(dir, "loaded.txt"))};
if (fs.existsSync(marker)) throw new Error("second load");
fs.writeFileSync(marker, "x");
export default { mode: "production", context: import.meta.dirname, entry: "./src/main.js" };
`,
  );
  const { status, events, collection } = runWrapped(
    dir,
    "compilation-hook-throws",
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.notEqual(status, 0);
  assert.equal(collection, null);
  assert.equal(
    events.find((event) => event.event === "error").code,
    "BW_CONFIG_LOAD_FAILED",
  );
});

// The repeat build runs in a new process, so nothing the first attempt left in
// memory can reach it: not the configuration file, not the modules it imports
// and not the objects they create. Mirrors the "Repeat build" table in the
// contract.
const transitiveShapes = [
  {
    name: "ES module imported by the configuration",
    config: "rspack.config.mjs",
    files: {
      "rspack.config.mjs": `import { plugin } from "./shared.mjs";
export default {
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
  plugins: [plugin],
};
`,
      "shared.mjs": `import fs from "node:fs";
fs.appendFileSync(import.meta.dirname + "/shared-loads.txt", "x");
${singleUseBody}export const plugin = new SingleUsePlugin();
`,
    },
  },
  {
    name: "configuration re-exported from another ES module",
    config: "rspack.config.mjs",
    files: {
      "rspack.config.mjs": `export { default } from "./real.config.mjs";\n`,
      "real.config.mjs": `import fs from "node:fs";
fs.appendFileSync(import.meta.dirname + "/shared-loads.txt", "x");
${singleUseBody}export default {
  mode: "production",
  context: import.meta.dirname,
  entry: "./src/main.js",
  plugins: [new SingleUsePlugin()],
};
`,
    },
  },
  {
    name: "CommonJS module required by the configuration",
    config: "rspack.config.cjs",
    files: {
      "rspack.config.cjs": `const { plugin } = require("./shared.cjs");
module.exports = {
  mode: "production",
  context: __dirname,
  entry: "./src/main.js",
  plugins: [plugin],
};
`,
      "shared.cjs": `require("node:fs").appendFileSync(__dirname + "/shared-loads.txt", "x");
${singleUseBody}exports.plugin = new SingleUsePlugin();
`,
    },
  },
  {
    name: "ES module imported inside a configuration function",
    config: "rspack.config.mjs",
    files: {
      "rspack.config.mjs": `export default async () => {
  const { plugin } = await import("./shared.mjs");
  return {
    mode: "production",
    context: import.meta.dirname,
    entry: "./src/main.js",
    plugins: [plugin],
  };
};
`,
      "shared.mjs": `import fs from "node:fs";
fs.appendFileSync(import.meta.dirname + "/shared-loads.txt", "x");
${singleUseBody}export const plugin = new SingleUsePlugin();
`,
    },
  },
];

for (const shape of transitiveShapes) {
  test(`the repeat build reloads imported modules (${shape.name})`, () => {
    const dir = wrappedProject();
    for (const [name, source] of Object.entries(shape.files)) {
      fs.writeFileSync(path.join(dir, name), source);
    }
    const { status, events, collection, calls } = runWrapped(
      dir,
      "compilation-hook-throws",
      { configFile: path.join(dir, shape.config) },
    );
    const loads = loadCount(dir, "shared-loads.txt");
    fs.rmSync(dir, { recursive: true, force: true });
    // The imported plugin instance rejects a second compiler: the repeat
    // succeeds only because it got a new instance from a new module load.
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(
      collection.exportUsage.reason,
      ExportUsageReason.BuildFailedWithObserver,
    );
    assert.deepEqual(calls, [
      ["SingleUsePlugin", ...OBSERVER],
      ["SingleUsePlugin"],
    ]);
    assert.equal(loads, 2);
  });
}

// The protocol a reader sees is the same with or without a repeat: one engine
// event, stages that never go back, one end.
const stageOrder = ["detecting", "building", "collecting"];

test("a repeat build adds no engine, stage or end event of its own", () => {
  const dir = wrappedProject();
  const { status, events } = runWrapped(dir, "compilation-hook-throws");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0, JSON.stringify(events));
  assert.equal(events.filter((event) => event.event === "engine").length, 1);
  assert.equal(events.filter((event) => event.event === "done").length, 1);
  assert.equal(events.filter((event) => event.event === "error").length, 0);
  assert.equal(events.at(-1).event, "done");
  const stages = events
    .filter((event) => event.event === "progress")
    .map((event) => stageOrder.indexOf(event.stage));
  assert.ok(!stages.includes(-1), JSON.stringify(events));
  assert.deepEqual(
    stages,
    [...stages].sort((left, right) => left - right),
  );
});

test("a failure of the repeat build is framed once", () => {
  const dir = wrappedProject();
  fs.writeFileSync(path.join(dir, "src/main.js"), "import { from ;\n");
  const { status, events } = runWrapped(dir, "intact");
  fs.rmSync(dir, { recursive: true, force: true });
  assert.notEqual(status, 0);
  assert.equal(events.filter((event) => event.event === "error").length, 1);
  assert.equal(events.filter((event) => event.event === "done").length, 0);
});

test("a repeat process that ends without a result is a build failure", () => {
  const dir = wrappedProject();
  fs.writeFileSync(
    path.join(dir, "rspack.config.mjs"),
    `import fs from "node:fs";
const marker = ${JSON.stringify(path.join(dir, "loaded.txt"))};
if (fs.existsSync(marker)) process.exit(3);
fs.writeFileSync(marker, "x");
export default { mode: "production", context: import.meta.dirname, entry: "./src/main.js" };
`,
  );
  const { status, events, collection } = runWrapped(
    dir,
    "compilation-hook-throws",
  );
  fs.rmSync(dir, { recursive: true, force: true });
  assert.notEqual(status, 0);
  assert.equal(collection, null);
  const errors = events.filter((event) => event.event === "error");
  assert.equal(errors.length, 1);
  assert.equal(errors[0].code, "BW_BUILD_FAILED");
  assert.match(
    errors[0].message,
    /repeat build process ended without a result/,
  );
});

// An entry function is called once in the first attempt. Its result reaches
// the repeat process as JSON when it survives a JSON round trip; otherwise the
// repeat process calls the function itself.
const entryShapes = [
  { name: "a string", body: `"./src/main.js"`, calls: 1, handed: true },
  { name: "a list", body: `["./src/main.js"]`, calls: 1, handed: true },
  {
    name: "an object of lists",
    body: `{ main: { import: ["./src/main.js"] } }`,
    calls: 1,
    handed: true,
  },
  {
    name: "an object with a function member",
    body: `{ main: { import: "./src/main.js", filename: () => "main.js" } }`,
    calls: 2,
    handed: false,
  },
  {
    name: "an object with a non-plain prototype",
    body: `Object.assign(new (class Entries {})(), { main: "./src/main.js" })`,
    calls: 2,
    handed: false,
  },
];

for (const shape of entryShapes) {
  test(`an entry function that returns ${shape.name} reaches the repeat build as documented`, () => {
    const dir = wrappedProject();
    fs.writeFileSync(
      path.join(dir, "rspack.config.mjs"),
      `import fs from "node:fs";
export default {
  mode: "production",
  context: import.meta.dirname,
  entry: () => {
    fs.appendFileSync(${JSON.stringify(path.join(dir, "entry-calls.txt"))}, "x");
    return ${shape.body};
  },
};
`,
    );
    const { status, events, collection } = runWrapped(
      dir,
      "compilation-hook-throws",
    );
    const calls = loadCount(dir, "entry-calls.txt");
    fs.rmSync(dir, { recursive: true, force: true });
    assert.equal(status, 0, JSON.stringify(events));
    assert.equal(calls, shape.calls);
    assert.equal(
      collection.overrides.includes(
        "entry function resolved once by bundlewhy",
      ),
      shape.handed,
    );
    assert.equal(
      collection.overrides.some((entry) =>
        entry.startsWith("entry function called again"),
      ),
      !shape.handed,
    );
  });
}

// The repeat process must not outlive a parent that is gone (cancellation
// kills only the parent on some platforms).
test(
  "a repeat process ends when its parent is killed",
  { skip: process.platform === "win32" },
  async () => {
    const dir = wrappedProject();
    const pidFile = path.join(dir, "repeat.pid");
    fs.writeFileSync(
      path.join(dir, "rspack.config.mjs"),
      `import fs from "node:fs";
const marker = ${JSON.stringify(path.join(dir, "loaded.txt"))};
if (fs.existsSync(marker)) {
  fs.writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));
  await new Promise(() => setInterval(() => {}, 1000));
}
fs.writeFileSync(marker, "x");
export default { mode: "production", context: import.meta.dirname, entry: "./src/main.js" };
`,
    );
    const work = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-orphan-"));
    const requestPath = path.join(work, "request.json");
    fs.writeFileSync(
      requestPath,
      JSON.stringify({
        projectRoot: dir,
        configFile: path.join(dir, "rspack.config.mjs"),
        configName: null,
        buildDir: path.join(work, "build"),
        collectPath: path.join(work, "collect.json"),
        supportedVersion: null,
        allowUnsupportedEngine: false,
        protocolToken: "test-token",
      }),
    );
    const parent = spawn(
      process.execPath,
      [
        path.join(root, "../../crates/bundlewhy/adapters/rspack.mjs"),
        requestPath,
      ],
      {
        cwd: dir,
        stdio: "ignore",
        env: {
          ...process.env,
          BUNDLEWHY_TEST_EXPORT_USAGE_MODE: "compilation-hook-throws",
        },
      },
    );
    const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
    const alive = (pid) => {
      try {
        process.kill(pid, 0);
        return true;
      } catch {
        return false;
      }
    };
    let repeatPid = 0;
    try {
      for (let waited = 0; waited < 30_000 && !repeatPid; waited += 50) {
        if (fs.existsSync(pidFile)) {
          repeatPid = Number(fs.readFileSync(pidFile, "utf8"));
        } else {
          await sleep(50);
        }
      }
      assert.ok(repeatPid > 0, "the repeat process did not start");
      assert.ok(alive(repeatPid));
      parent.kill("SIGKILL");
      let ended = false;
      for (let waited = 0; waited < 10_000 && !ended; waited += 50) {
        ended = !alive(repeatPid);
        if (!ended) await sleep(50);
      }
      assert.ok(ended, "the repeat process outlived its parent");
    } finally {
      parent.kill("SIGKILL");
      if (repeatPid && alive(repeatPid)) process.kill(repeatPid, "SIGKILL");
      fs.rmSync(work, { recursive: true, force: true });
      fs.rmSync(dir, { recursive: true, force: true });
    }
  },
);

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
