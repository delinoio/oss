// SPDX-License-Identifier: Apache-2.0

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

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
  const result = spawnSync(process.execPath, [adapterPath, requestPath], {
    cwd: projectRoot,
    encoding: "utf8",
  });
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
        module.path === path.join(fs.realpathSync(root), `${name}.js`) &&
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
        module.path ===
        path.join(fs.realpathSync(projectRoot), "project-entry.js"),
    ),
  );
  assert.ok(fs.existsSync(path.join(buildDir, "external-entry.txt")));
  assert.ok(!fs.existsSync(path.join(buildDir, "project-entry.txt")));
  assert.ok(!fs.existsSync(path.join(externalRoot, "dist")));
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
