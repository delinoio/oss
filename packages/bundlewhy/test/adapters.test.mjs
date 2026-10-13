import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function runAdapter(bundler, project, config, extra = {}) {
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
      path.join(root, "adapters", `${bundler}.mjs`),
      path.join(work, "request.json"),
    ],
    {
      cwd: projectRoot,
      encoding: "utf8",
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
  const { status, collection } = runAdapter("rspack", "rspack-app", path.relative(projectRoot, file));
  fs.rmSync(dir, { recursive: true, force: true });
  assert.equal(status, 0);
  assert.equal(collection.libraryBuild, true);
});
