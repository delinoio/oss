// SPDX-License-Identifier: Apache-2.0

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { normalizeCollectionRoot } from "../../../crates/bundlewhy/adapters/vite.mjs";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const adapterPath = path.resolve(
  packageRoot,
  "../../crates/bundlewhy/adapters/vite.mjs",
);
const vitePackagePath = createRequire(
  path.join(packageRoot, "package.json"),
).resolve("vite/package.json");
const viteCli = path.join(path.dirname(vitePackagePath), "bin/vite.js");

test("collection roots use Vite's separator format in every resolution mode", () => {
  const lexicalRoot = "C:\\project\\web";
  assert.equal(normalizeCollectionRoot(lexicalRoot, true), "C:/project/web");
  assert.equal(normalizeCollectionRoot(lexicalRoot), "C:/project/web");
  assert.equal(
    normalizeCollectionRoot(process.cwd()),
    fs.realpathSync(process.cwd()).replaceAll("\\", "/"),
  );
});

function makeProject(t) {
  const work = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-vite-root-"));
  t.after(() => fs.rmSync(work, { recursive: true, force: true }));
  const projectRoot = path.join(work, "project");
  fs.mkdirSync(projectRoot);
  fs.writeFileSync(
    path.join(projectRoot, "package.json"),
    JSON.stringify({ type: "module", dependencies: { vite: "8.3.4" } }),
  );
  fs.symlinkSync(
    path.join(packageRoot, "node_modules"),
    path.join(projectRoot, "node_modules"),
    process.platform === "win32" ? "junction" : "dir",
  );
  return { work, projectRoot };
}

function writeEntry(root, name) {
  fs.mkdirSync(root, { recursive: true });
  fs.writeFileSync(
    path.join(root, "index.html"),
    `<script type="module" src="/${name}.js"></script>\n`,
  );
  fs.writeFileSync(path.join(root, `${name}.js`), `console.log("${name}");\n`);
  fs.mkdirSync(path.join(root, "public"));
  fs.writeFileSync(path.join(root, "public", `${name}.txt`), name);
}

function runNormalBuild(projectRoot, configFile) {
  const result = spawnSync(
    process.execPath,
    [viteCli, "build", "--config", configFile],
    { cwd: projectRoot, encoding: "utf8" },
  );
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
}

function runDiagnostic(work, projectRoot, configFile, expectedStatus = 0) {
  const buildDir = path.join(work, "diagnostic", "build");
  const collectPath = path.join(work, "diagnostic", "collect.json");
  fs.mkdirSync(path.dirname(buildDir), { recursive: true });
  const requestPath = path.join(work, "request.json");
  fs.writeFileSync(
    requestPath,
    JSON.stringify({
      projectRoot,
      configFile,
      configName: null,
      buildDir,
      collectPath,
      supportedVersion: "8.3.4",
      allowUnsupportedEngine: false,
      protocolToken: "vite-root-test",
    }),
  );
  const result = spawnSync(
    process.execPath,
    [
      path.join(packageRoot, "test/adapter-supervisor.mjs"),
      adapterPath,
      requestPath,
    ],
    {
      cwd: projectRoot,
      encoding: "utf8",
    },
  );
  assert.equal(
    result.status,
    expectedStatus,
    `${result.stdout}\n${result.stderr}`,
  );
  const prefix = "BUNDLEWHY-FRAME vite-root-test ";
  const events = result.stdout
    .split("\n")
    .filter((line) => line.startsWith(prefix))
    .map((line) => JSON.parse(line.slice(prefix.length)));
  if (expectedStatus !== 0) {
    assert.ok(!fs.existsSync(collectPath));
    return { buildDir, events };
  }
  const collection = JSON.parse(fs.readFileSync(collectPath, "utf8"));
  assert.ok(
    collection.overrides.every((override) => !override.startsWith("root=")),
  );
  assert.ok(collection.chunks.length > 0);
  assert.ok(
    collection.chunks.every((chunk) =>
      fs.existsSync(path.join(buildDir, `${chunk.file}.map`)),
    ),
  );
  return { buildDir, collection, events };
}

function assertSelectedEntry(collection, root, name) {
  assert.ok(
    collection.modules.some(
      (module) =>
        typeof module.path === "string" &&
        path.normalize(module.path) ===
          path.normalize(path.join(fs.realpathSync(root), `${name}.js`)) &&
        module.included,
    ),
    `${name} must come from the selected Vite root`,
  );
}

test("Vite diagnostic keeps a relative configured root and its public files", (t) => {
  const { work, projectRoot } = makeProject(t);
  const webRoot = path.join(projectRoot, "web");
  writeEntry(webRoot, "web-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(configFile, 'export default { root: "web" };\n');

  runNormalBuild(projectRoot, configFile);
  assert.ok(fs.existsSync(path.join(webRoot, "dist", "web-entry.txt")));
  fs.rmSync(path.join(webRoot, "dist"), { recursive: true });
  const { buildDir, collection } = runDiagnostic(work, projectRoot, configFile);
  assertSelectedEntry(collection, webRoot, "web-entry");
  assert.ok(fs.existsSync(path.join(buildDir, "web-entry.txt")));
  assert.ok(!fs.existsSync(path.join(webRoot, "dist")));
});

test("Vite diagnostic keeps an absolute root outside the selected project", (t) => {
  const { work, projectRoot } = makeProject(t);
  const externalRoot = path.join(work, "external");
  writeEntry(projectRoot, "project-entry");
  writeEntry(externalRoot, "external-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(
    configFile,
    `export default { root: ${JSON.stringify(externalRoot)} };\n`,
  );

  runNormalBuild(projectRoot, configFile);
  assert.ok(
    fs.existsSync(path.join(externalRoot, "dist", "external-entry.txt")),
  );
  fs.rmSync(path.join(externalRoot, "dist"), { recursive: true });
  const { buildDir, collection } = runDiagnostic(work, projectRoot, configFile);
  assertSelectedEntry(collection, externalRoot, "external-entry");
  assert.ok(
    !collection.modules.some(
      (module) =>
        typeof module.path === "string" &&
        path.normalize(module.path) ===
          path.normalize(
            path.join(fs.realpathSync(projectRoot), "project-entry.js"),
          ),
    ),
  );
  assert.ok(fs.existsSync(path.join(buildDir, "external-entry.txt")));
  assert.ok(!fs.existsSync(path.join(buildDir, "project-entry.txt")));
  assert.ok(!fs.existsSync(path.join(externalRoot, "dist")));
});

test("Vite protection is announced before configResolved hooks run", (t) => {
  const { work, projectRoot } = makeProject(t);
  const externalRoot = path.join(work, "external");
  writeEntry(externalRoot, "external-entry");
  fs.writeFileSync(
    path.join(externalRoot, "package.json"),
    JSON.stringify({ name: "before-hook" }),
  );
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(
    configFile,
    `import fs from "node:fs";
export default {
  root: ${JSON.stringify(externalRoot)},
  plugins: [{
    name: "mutate-protected-root",
    configResolved() {
      fs.writeFileSync(${JSON.stringify(path.join(externalRoot, "package.json"))}, JSON.stringify({ name: "after-hook" }));
    },
  }],
};
`,
  );

  const { events } = runDiagnostic(work, projectRoot, configFile);
  const protectionIndex = events.findIndex(
    (event) => event.event === "protection",
  );
  const buildingIndex = events.findIndex(
    (event) => event.event === "progress" && event.stage === "building",
  );
  assert.ok(protectionIndex >= 0);
  assert.ok(buildingIndex >= 0);
  assert.ok(protectionIndex < buildingIndex);
  assert.deepEqual(
    JSON.parse(
      fs.readFileSync(path.join(externalRoot, "package.json"), "utf8"),
    ),
    { name: "after-hook" },
  );
});

test("Vite diagnostic rejects a root selected during a mutating config hook", (t) => {
  const { work, projectRoot } = makeProject(t);
  const externalRoot = path.join(work, "external");
  writeEntry(externalRoot, "external-entry");
  const manifest = path.join(externalRoot, "package.json");
  fs.writeFileSync(manifest, JSON.stringify({ name: "before-hook" }));
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(
    configFile,
    `import fs from "node:fs";
export default {
  plugins: [{
    name: "select-and-mutate-root",
    config() {
      fs.writeFileSync(${JSON.stringify(manifest)}, JSON.stringify({ name: "after-hook" }));
      return { root: ${JSON.stringify(externalRoot)} };
    },
  }],
};
`,
  );

  const { events } = runDiagnostic(work, projectRoot, configFile, 1);
  assert.equal(
    events.find((event) => event.event === "error")?.code,
    "BW_UNSUPPORTED_BUILD",
  );
  assert.deepEqual(JSON.parse(fs.readFileSync(manifest, "utf8")), {
    name: "after-hook",
  });
});

test("Vite diagnostic falls back to the selected project root", (t) => {
  const { work, projectRoot } = makeProject(t);
  writeEntry(projectRoot, "project-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(configFile, "export default {};\n");

  const { buildDir, collection } = runDiagnostic(work, projectRoot, configFile);
  assertSelectedEntry(collection, projectRoot, "project-entry");
  assert.ok(fs.existsSync(path.join(buildDir, "project-entry.txt")));
  assert.ok(!fs.existsSync(path.join(projectRoot, "dist")));
});

test("Vite diagnostic keeps build-failure reporting for an empty configured root", (t) => {
  const { work, projectRoot } = makeProject(t);
  const emptyRoot = path.join(projectRoot, "empty");
  fs.mkdirSync(emptyRoot);
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(configFile, 'export default { root: "empty" };\n');

  const { events } = runDiagnostic(work, projectRoot, configFile, 1);
  assert.equal(
    events.find((event) => event.event === "error")?.code,
    "BW_BUILD_FAILED",
  );
});

test("Vite diagnostic gives plugins the real config file and applies the config once", (t) => {
  const { work, projectRoot } = makeProject(t);
  writeEntry(projectRoot, "project-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  const probe = path.join(work, "plugin-probe.json");
  fs.writeFileSync(
    configFile,
    `import fs from "node:fs";
export default {
  plugins: [{
    name: "require-config-file",
    configResolved(config) {
      fs.writeFileSync(${JSON.stringify(probe)}, JSON.stringify({
        configFile: config.configFile,
        plugins: config.plugins.map((plugin) => plugin.name),
        mode: config.mode,
        outDir: config.build.outDir,
        emptyOutDir: config.build.emptyOutDir,
        sourcemap: config.build.sourcemap,
        write: config.build.write,
        watch: config.build.watch,
      }));
    },
  }],
};
`,
  );

  const { buildDir, collection } = runDiagnostic(work, projectRoot, configFile);
  const seen = JSON.parse(fs.readFileSync(probe, "utf8"));
  assert.equal(fs.realpathSync(seen.configFile), fs.realpathSync(configFile));
  assert.equal(
    seen.plugins.filter((name) => name === "require-config-file").length,
    1,
  );
  assert.equal(
    seen.plugins.filter((name) => name === "bundlewhy:collector").length,
    1,
  );
  assert.equal(seen.mode, "production");
  assert.equal(path.resolve(projectRoot, seen.outDir), buildDir);
  assert.equal(seen.emptyOutDir, true);
  assert.equal(seen.sourcemap, true);
  assert.equal(seen.write, true);
  assert.equal(seen.watch, null);
  assert.ok(collection.configSummary.plugins.includes("require-config-file"));
  assert.ok(!collection.configSummary.plugins.includes("bundlewhy:collector"));
  assert.ok(
    !collection.configSummary.plugins.includes("bundlewhy:config-file"),
  );
});

test("Vite diagnostic keeps configFile disabled without a configuration file", (t) => {
  const { work, projectRoot } = makeProject(t);
  writeEntry(projectRoot, "project-entry");
  // A discoverable config file must not be loaded when none was selected.
  fs.writeFileSync(
    path.join(projectRoot, "vite.config.js"),
    'throw new Error("config file must not be loaded");\n',
  );

  const { collection } = runDiagnostic(work, projectRoot, null);
  assert.equal(collection.configFile, null);
  assert.ok(collection.chunks.length > 0);
});

test("Vite diagnostic reports a throwing config factory as a configuration error", (t) => {
  const { work, projectRoot } = makeProject(t);
  writeEntry(projectRoot, "project-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(
    configFile,
    'export default () => { throw new Error("factory broke"); };\n',
  );

  const { events } = runDiagnostic(work, projectRoot, configFile, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error?.code, "BW_CONFIG_LOAD_FAILED");
  assert.match(error.message, /factory broke/);
});

test("Vite diagnostic does not blame the native build for a plugin hook error", (t) => {
  const { work, projectRoot } = makeProject(t);
  writeEntry(projectRoot, "project-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  fs.writeFileSync(
    configFile,
    `export default {
  plugins: [{
    name: "throwing-hook",
    buildStart() {
      throw new Error("plugin hook broke");
    },
  }],
};
`,
  );

  const { events } = runDiagnostic(work, projectRoot, configFile, 1);
  const error = events.find((event) => event.event === "error");
  assert.equal(error?.code, "BW_BUILD_FAILED");
  assert.match(error.message, /plugin hook broke/);
  assert.match(error.recovery, /diagnostic build/);
  assert.doesNotMatch(error.recovery, /^Fix the build error/);
});

test("Vite diagnostic evaluates a stateful config factory exactly once", (t) => {
  const { work, projectRoot } = makeProject(t);
  writeEntry(projectRoot, "project-entry");
  const configFile = path.join(projectRoot, "vite.config.js");
  const counter = path.join(work, "factory-calls.log");
  const probe = path.join(work, "factory-probe.json");
  // The factory records every call outside the project and rejects a second
  // call, like a configuration that claims a one-shot process-level resource.
  fs.writeFileSync(
    configFile,
    `import fs from "node:fs";
export default () => {
  fs.appendFileSync(${JSON.stringify(counter)}, "call\\n");
  const calls = fs.readFileSync(${JSON.stringify(counter)}, "utf8").trim().split("\\n").length;
  if (calls > 1) throw new Error("factory evaluated " + calls + " times");
  return {
    plugins: [{
      name: "read-config-file",
      configResolved(config) {
        fs.writeFileSync(${JSON.stringify(probe)}, JSON.stringify({
          configFile: config.configFile,
          dependencies: config.configFileDependencies,
        }));
      },
    }],
  };
};
`,
  );

  const { collection } = runDiagnostic(work, projectRoot, configFile);
  assert.equal(
    fs.readFileSync(counter, "utf8").trim().split("\n").length,
    1,
    "the config factory must run once for the whole diagnostic run",
  );
  const seen = JSON.parse(fs.readFileSync(probe, "utf8"));
  assert.equal(fs.realpathSync(seen.configFile), fs.realpathSync(configFile));
  assert.ok(Array.isArray(seen.dependencies));
  assert.ok(collection.chunks.length > 0);
});
