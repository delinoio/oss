// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const FailureClass = Object.freeze({
  Spawn: "spawn-failed",
  Timeout: "timeout",
  OutputLimit: "output-limit",
  Signal: "signal",
  Exit: "nonzero-exit",
  InvalidOutput: "invalid-json",
  UnexpectedOutput: "unexpected-outcome",
});

function failProbe(name, failureClass, result) {
  // Child streams, parsed values and spawn errors can contain input paths or
  // private content. Never attach them as a message, assertion value or cause.
  throw new Error(JSON.stringify({ case: name, failureClass,
    exitCode: Number.isInteger(result.status) ? result.status : null }));
}

function requireSuccessfulExit(name, result) {
  if (result.error) {
    const failureClass = result.error.code === "ETIMEDOUT" ? FailureClass.Timeout
      : result.error.code === "ENOBUFS" ? FailureClass.OutputLimit : FailureClass.Spawn;
    failProbe(name, failureClass, result);
  }
  if (result.signal) failProbe(name, FailureClass.Signal, result);
  if (result.status !== 0) failProbe(name, FailureClass.Exit, result);
}

function requireExpectedOutcome(name, result) {
  requireSuccessfulExit(name, result);
  let value;
  try { value = JSON.parse(result.stdout); }
  catch { failProbe(name, FailureClass.InvalidOutput, result); }
  if (!value || typeof value !== "object" || Array.isArray(value)
    || Object.keys(value).length !== 4 || value.code !== "PNP_API_READY"
    || value.zipBacked !== true || value.pnp !== "3" || value.findPnpApi !== true) {
    failProbe(name, FailureClass.UnexpectedOutput, result);
  }
}

// Use a ZIP-backed package's require context. pnpapi is supplied by the Yarn
// loader to every package, without a declared package dependency.
const probe = `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const {createRequire, findPnpApi} = require('node:module');
if (process.env.PNPORT_TEST_PRELOAD) {
  assert.equal(globalThis.__pnportTestPreload, 'preserved');
  assert.deepEqual(globalThis.__pnportTestPreloadOrder, ['first', 'second']);
}
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
  // Graph admission canonicalizes the project. Build selected-loader URL
  // controls from that same root so macOS /tmp aliases do not add a distinct
  // filesystem-symlink case to the normalized URL identity checks.
  try { root = realpathSync(root); }
  catch { throw new Error("Cannot canonicalize the prepared Yarn PnP fixture."); }
  const loader = join(resolve(root), ".pnp.cjs");
  assert(existsSync(loader), "An already prepared Yarn PnP fixture is required");
  const env = { ...environment };
  delete env.NODE_OPTIONS;
  const quote = (value) => `"${value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`;
  const nodeOptions = { ...env, NODE_OPTIONS: `--require ${quote(loader)}` };
  const temporary = mkdtempSync(join(tmpdir(), "pnport-preload-options-"));
  const childDirectory = mkdtempSync(join(resolve(root), "pnport-esm-cwd-"));
  const custom = join(temporary, "preload with spaces.cjs");
  const second = join(temporary, "second preload.cjs");
  writeFileSync(custom, `
const assert = require('node:assert/strict');
const projectRequire = require('node:module').createRequire(${JSON.stringify(join(resolve(root), "caller.cjs"))});
assert.equal(projectRequire('pnpapi').VERSIONS.std, 3);
assert.equal(typeof projectRequire(${JSON.stringify(dependency)}).name, 'string');
globalThis.__pnportTestPreload = 'preserved';
globalThis.__pnportTestPreloadOrder = ['first'];
`);
  writeFileSync(second, "require('node:assert/strict').deepEqual(globalThis.__pnportTestPreloadOrder, ['first']);\nglobalThis.__pnportTestPreloadOrder.push('second');\n");
  const customOptions = `--no-warnings --require=${quote(custom)} --require ${quote(second)}`;
  const inheritedCustom = { ...env, NODE_OPTIONS: customOptions, PNPORT_TEST_PRELOAD: "1" };
  const requireCountProbe = (count) => `${probe}\nassert.equal((process.env.NODE_OPTIONS.match(/(?:--require|-r)(?:=| )/g) ?? []).length, ${count});`;
  const relativeRequire = '--require "./.pnp.cjs"';
  const outsideDirectory = join(temporary, "outside");
  mkdirSync(outsideDirectory);
  writeFileSync(join(temporary, "other preload.cjs"), "globalThis.__pnportOtherRelativePreload = true;\n");
  const cases = [
    ["automatic", ["--eval", probe, dependency], env],
    ["explicitRequire", ["--require", loader, "--eval", probe, dependency], env],
    ["automaticDescendant", ["--eval", descendant("const env = {...process.env};"), dependency], env],
    ["nodeOptionsInherited", ["--eval", descendant("const env = {...process.env};"), dependency], nodeOptions],
    ["nodeOptionsRemoved", ["--eval", descendant("const env = {...process.env}; delete env.NODE_OPTIONS;"), dependency], nodeOptions],
    ["environmentReplaced", ["--eval", descendant("const env = {};"), dependency], env],
    ["descendantOutsideProject", ["--eval", descendant("const env = {};", probe, false, temporary), dependency], env],
    ["callerPreloadPreserved", ["--eval", probe, dependency], inheritedCustom],
    ["callerPreloadBeforeExplicitLoader", ["--eval", probe, dependency],
      { ...inheritedCustom, NODE_OPTIONS: `${customOptions} -r ${quote(loader)}` }],
    ["descendantOptionsReplaced", ["--eval", descendant(`const env = {NODE_OPTIONS: ${JSON.stringify(customOptions)}, PNPORT_TEST_PRELOAD: '1'};`), dependency], env],
    ["relativeRequire", ["--eval", requireCountProbe(1), dependency], { ...env, NODE_OPTIONS: relativeRequire }],
    ["relativeRequireAlias", ["--eval", requireCountProbe(1), dependency], { ...env, NODE_OPTIONS: '-r="./.pnp.cjs"' }],
    ["relativeRequireBoundAcrossCwdChange", ["--eval",
      descendant("const env = {...process.env};", requireCountProbe(1), false, outsideDirectory), dependency],
      { ...env, NODE_OPTIONS: relativeRequire }],
    ["relativeRequireReplacedWithChildCwd", ["--eval",
      descendant(`const env = {NODE_OPTIONS: '--require "../.pnp.cjs"'};`, requireCountProbe(1), false, childDirectory), dependency], env],
    ["otherRelativeRequirePreserved", ["--eval",
      descendant(`const env = {NODE_OPTIONS: '--require "./other preload.cjs"'};`, `${requireCountProbe(2)}\nassert.equal(globalThis.__pnportOtherRelativePreload, true);`, false, temporary), dependency], env],
  ];
  if (existsSync(join(root, ".pnp.loader.mjs"))) {
    cases.push(["automaticEsm", ["--input-type=module", "--eval", esmProbe, dependency], env]);
    cases.push(["descendantEsmOptionsRemoved", ["--eval", descendant("const env = {};", esmProbe, true), dependency], env]);
    const firstEsm = join(childDirectory, "first loader.mjs");
    const secondEsm = join(childDirectory, "second loader.mjs");
    writeFileSync(firstEsm, `
import assert from 'node:assert/strict';
import api from 'pnpapi';
import manifest from ${JSON.stringify(dependency)} with {type: 'json'};
assert.equal(api.VERSIONS.std, 3);
assert.equal(typeof manifest.name, 'string');
globalThis.__pnportTestLoaderOrder = ['first'];
`);
    writeFileSync(secondEsm, `
import assert from 'node:assert/strict';
assert.deepEqual(globalThis.__pnportTestLoaderOrder, ['first']);
globalThis.__pnportTestLoaderOrder.push('second');
export function resolve(specifier, context, nextResolve) {
  if (specifier === 'pnport:loader-order') {
    assert.deepEqual(globalThis.__pnportTestLoaderOrder, ['first', 'second']);
    return {url: 'data:text/javascript,export default true', shortCircuit: true};
  }
  return nextResolve(specifier, context);
}
`);
    const callerEsmOptions = `--no-warnings --loader ${quote(pathToFileURL(firstEsm).href)} --experimental-loader=${quote(pathToFileURL(secondEsm).href)}`;
    const callerEsmProbe = `import 'pnport:loader-order';\n${esmProbe}`;
    cases.push(["esmCallerLoaders", ["--input-type=module", "--eval", callerEsmProbe, dependency],
      { ...env, NODE_OPTIONS: callerEsmOptions }]);
    cases.push(["esmCallerBeforeExplicitLoader", ["--input-type=module", "--eval", callerEsmProbe, dependency],
      { ...env, NODE_OPTIONS: `${callerEsmOptions} --loader ${quote(pathToFileURL(join(resolve(root), ".pnp.loader.mjs")).href)}` }]);
    cases.push(["esmDescendantCallerOptionsReplaced", ["--eval",
      descendant(`const env = {NODE_OPTIONS: ${JSON.stringify(callerEsmOptions)}};`, callerEsmProbe, true), dependency], env]);
    const selectedUrl = pathToFileURL(join(resolve(root), ".pnp.loader.mjs")).href;
    const singleLoaderProbe = `${esmProbe}\nassert.equal((process.env.NODE_OPTIONS.match(/--(?:experimental-)?loader(?:=| )/g) ?? []).length, 1);`;
    const loaderCountProbe = (count) => `${probe}\nassert.equal((process.env.NODE_OPTIONS.match(/--(?:experimental-)?loader(?:=| )/g) ?? []).length, ${count});`;
    for (const [name, url] of [
      ["esmUppercaseScheme", selectedUrl.replace(/^file:/, "FILE:")],
      ["esmUppercaseLocalhost", selectedUrl.replace(/^file:\/\//, "file://LOCALHOST")],
    ]) {
      cases.push([name, ["--input-type=module", "--eval", singleLoaderProbe, dependency],
        { ...nodeOptions, NODE_OPTIONS: `${nodeOptions.NODE_OPTIONS} --loader=${quote(url)}` }]);
    }
    const relativeOptions = '--loader "./.pnp.loader.mjs"';
    cases.push(["esmRelativeLoader", ["--input-type=module", "--eval", singleLoaderProbe, dependency],
      { ...env, NODE_OPTIONS: relativeOptions }]);
    cases.push(["esmRelativeLoaderAlias", ["--input-type=module", "--eval", singleLoaderProbe, dependency],
      { ...env, NODE_OPTIONS: '--experimental-loader="./.pnp.loader.mjs"' }]);
    cases.push(["esmRelativeLoaderBoundAcrossCwdChange", ["--eval",
      descendant("const env = {...process.env};", loaderCountProbe(1), false, temporary), dependency],
      { ...env, NODE_OPTIONS: relativeOptions }]);
    cases.push(["esmRelativeLoaderReplacedWithChildCwd", ["--eval",
      descendant(`const env = {NODE_OPTIONS: '--loader "../.pnp.loader.mjs"'};`, loaderCountProbe(1), false, childDirectory), dependency], env]);
    writeFileSync(join(temporary, ".pnp.loader.mjs"), "export {};\n");
    cases.push(["esmOtherRelativeLoaderPreserved", ["--eval",
      descendant(`const env = {NODE_OPTIONS: ${JSON.stringify(relativeOptions)}};`, loaderCountProbe(2), false, temporary), dependency], env]);
  }
  const outcomes = {};
  try {
    for (const [name, args, childEnv] of cases) {
      const result = spawnSync(binary, ["--cache-dir", cache, "--color", "never", "run", "--", process.execPath, ...args, resolve(root)],
        { cwd: root, env: childEnv, encoding: "utf8", timeout: 30_000, maxBuffer: 1024 * 1024 });
      requireExpectedOutcome(name, result);
      outcomes[name] = { apiAvailable: true, exitCode: result.status };
    }
  } finally {
    rmSync(temporary, { recursive: true, force: true });
    rmSync(childDirectory, { recursive: true, force: true });
  }
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
    requireSuccessfulExit("cacheClean", cleaned);
    rmSync(temporary, { recursive: true, force: true });
  }
}
