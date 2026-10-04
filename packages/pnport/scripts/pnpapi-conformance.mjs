// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Use a ZIP-backed package's require context. pnpapi is supplied by the Yarn
// loader to every package, without a declared package dependency.
const probe = `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const {createRequire, findPnpApi} = require('node:module');
if (process.env.PNPORT_TEST_PRELOAD) assert.equal(globalThis.__pnportTestPreload, 'preserved');
const issuer = (process.argv[2] ?? process.cwd()) + '/probe.cjs';
const filename = createRequire(issuer).resolve(process.argv[1]);
assert.equal(typeof JSON.parse(fs.readFileSync(filename, 'utf8')).name, 'string');
const zipBacked = filename.replaceAll('\\\\', '/').includes('.zip/');
assert(zipBacked, 'The probe requires a ZIP-backed package manifest');
const api = createRequire(filename)('pnpapi');
assert.equal(String(api.VERSIONS.std), process.versions.pnp);
assert.equal(findPnpApi(filename), api);
assert(api.findPackageLocator(filename));
const resolved = api.resolveRequest(process.argv[1], issuer);
assert.equal(fs.readFileSync(resolved, 'utf8'), fs.readFileSync(filename, 'utf8'));
process.stdout.write(JSON.stringify({code: 'PNP_API_READY', zipBacked,
  pnp: process.versions.pnp, findPnpApi: true}));
`;

const esmProbe = `
import assert from 'node:assert/strict';
import api from 'pnpapi';
import Module from 'node:module';
const {findPnpApi, createRequire} = Module;
const require = createRequire(process.cwd() + '/probe.mjs');
const filename = require.resolve(process.argv[1]);
const manifest = await import(process.argv[1], {with: {type: 'json'}});
assert.equal(typeof manifest.default.name, 'string');
assert(filename.replaceAll('\\\\', '/').includes('.zip/'));
assert.equal(String(api.VERSIONS.std), process.versions.pnp);
assert.deepEqual(findPnpApi(filename).findPackageLocator(filename), api.findPackageLocator(filename));
assert.equal(api.resolveRequest(process.argv[1], process.cwd() + '/probe.mjs'), filename);
process.stdout.write(JSON.stringify({code: 'PNP_API_READY', zipBacked: true,
  pnp: process.versions.pnp, findPnpApi: true}));
`;

function descendant(environment, source = probe, module = false, cwd) {
  return `
const {spawnSync} = require('node:child_process');
${environment}
const child = spawnSync(process.execPath, [${module ? "'--input-type=module', " : ""}'--eval', ${JSON.stringify(source)}, process.argv[1], process.argv[2]],
  {env, stdio: 'inherit'${cwd ? `, cwd: ${JSON.stringify(cwd)}` : ""}});
if (child.error) throw child.error;
process.exit(child.status ?? 1);
`;
}

export function pnpApiConformance({ binary, root, cache, environment = process.env, dependency = "@types/node/package.json" }) {
  const loader = join(resolve(root), ".pnp.cjs");
  assert(existsSync(loader), "An already prepared Yarn PnP fixture is required");
  const env = { ...environment };
  delete env.NODE_OPTIONS;
  const quote = (value) => `"${value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`;
  const nodeOptions = { ...env, NODE_OPTIONS: `--require ${quote(loader)}` };
  const temporary = mkdtempSync(join(tmpdir(), "pnport-preload-options-"));
  const custom = join(temporary, "preload with spaces.cjs");
  writeFileSync(custom, "globalThis.__pnportTestPreload = 'preserved';\n");
  const customOptions = `--no-warnings --require=${quote(custom)}`;
  const inheritedCustom = { ...env, NODE_OPTIONS: customOptions, PNPORT_TEST_PRELOAD: "1" };
  const cases = [
    ["automatic", ["--eval", probe, dependency], env],
    ["explicitRequire", ["--require", loader, "--eval", probe, dependency], env],
    ["automaticDescendant", ["--eval", descendant("const env = {...process.env};"), dependency], env],
    ["nodeOptionsInherited", ["--eval", descendant("const env = {...process.env};"), dependency], nodeOptions],
    ["nodeOptionsRemoved", ["--eval", descendant("const env = {...process.env}; delete env.NODE_OPTIONS;"), dependency], nodeOptions],
    ["environmentReplaced", ["--eval", descendant("const env = {};"), dependency], env],
    ["descendantOutsideProject", ["--eval", descendant("const env = {};", probe, false, temporary), dependency], env],
    ["callerPreloadPreserved", ["--eval", probe, dependency], inheritedCustom],
    ["descendantOptionsReplaced", ["--eval", descendant(`const env = {NODE_OPTIONS: ${JSON.stringify(customOptions)}, PNPORT_TEST_PRELOAD: '1'};`), dependency], env],
  ];
  if (existsSync(join(root, ".pnp.loader.mjs"))) {
    cases.push(["automaticEsm", ["--input-type=module", "--eval", esmProbe, dependency], env]);
    cases.push(["descendantEsmOptionsRemoved", ["--eval", descendant("const env = {};", esmProbe, true), dependency], env]);
    const selectedUrl = pathToFileURL(join(resolve(root), ".pnp.loader.mjs")).href;
    const singleLoaderProbe = `${esmProbe}\nassert.equal((process.env.NODE_OPTIONS.match(/--(?:experimental-)?loader(?:=| )/g) ?? []).length, 1);`;
    for (const [name, url] of [
      ["esmUppercaseScheme", selectedUrl.replace(/^file:/, "FILE:")],
      ["esmUppercaseLocalhost", selectedUrl.replace(/^file:\/\//, "file://LOCALHOST")],
    ]) {
      cases.push([name, ["--input-type=module", "--eval", singleLoaderProbe, dependency],
        { ...nodeOptions, NODE_OPTIONS: `${nodeOptions.NODE_OPTIONS} --loader=${quote(url)}` }]);
    }
  }
  const outcomes = {};
  try {
    for (const [name, args, childEnv] of cases) {
      const result = spawnSync(binary, ["--cache-dir", cache, "--color", "never", "run", "--", process.execPath, ...args, resolve(root)],
        { cwd: root, env: childEnv, encoding: "utf8", timeout: 30_000, maxBuffer: 1024 * 1024 });
      assert.ifError(result.error);
      assert.equal(result.status, 0, `${name}: ${result.stdout}\n${result.stderr}`);
      assert.deepEqual(JSON.parse(result.stdout), { code: "PNP_API_READY", zipBacked: true, pnp: "3", findPnpApi: true }, name);
      outcomes[name] = { apiAvailable: true, exitCode: result.status };
    }
  } finally { rmSync(temporary, { recursive: true, force: true }); }
  assert(!existsSync(join(root, "node_modules")), "Do not generate a physical dependency tree");
  return { node: process.version, outcomes };
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const [directory, executable, dependency] = process.argv.slice(2);
  assert(directory && executable, "Usage: pnpapi-conformance.mjs <prepared-fixture> <pnport-binary> [ZIP-package/package.json]");
  const temporary = mkdtempSync(join(tmpdir(), "pnport-pnpapi-"));
  const cache = join(temporary, "cache");
  const binary = resolve(executable);
  try {
    console.log(JSON.stringify({ event: "pnport_pnpapi_conformance", platform: process.platform, arch: process.arch,
      ...pnpApiConformance({ binary, root: resolve(directory), cache, dependency }) }));
  } finally {
    // The native cache command removes read-only package storage after leases
    // have ended. Recursive removal alone cannot unlink its protected files.
    const cleaned = spawnSync(binary, ["--cache-dir", cache, "cache", "clean"], { encoding: "utf8", timeout: 30_000 });
    assert.ifError(cleaned.error);
    assert.equal(cleaned.status, 0, "Cannot clean the synthetic conformance cache");
    rmSync(temporary, { recursive: true, force: true });
  }
}
