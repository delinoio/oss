// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Use a ZIP-backed package's require context. pnpapi is supplied by the Yarn
// loader to every package, without a declared package dependency.
const probe = `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const {createRequire, findPnpApi} = require('node:module');
const filename = require.resolve(process.argv[1]);
assert.equal(typeof JSON.parse(fs.readFileSync(filename, 'utf8')).name, 'string');
const zipBacked = filename.replaceAll('\\\\', '/').includes('.zip/');
assert(zipBacked, 'The probe requires a ZIP-backed package manifest');
let api;
try { api = createRequire(filename)('pnpapi'); }
catch (error) {
  if (error.code !== 'MODULE_NOT_FOUND') throw error;
  assert.match(error.message, /^Cannot find module 'pnpapi'/);
  process.stdout.write(JSON.stringify({code: error.code, zipBacked,
    pnp: process.versions.pnp ?? null, findPnpApi: typeof findPnpApi === 'function'}));
  process.exit(1);
}
assert.equal(String(api.VERSIONS.std), process.versions.pnp);
assert.equal(findPnpApi(filename), api);
assert(api.findPackageLocator(filename));
const resolved = api.resolveRequest(process.argv[1], process.cwd() + '/probe.cjs');
assert.equal(fs.readFileSync(resolved, 'utf8'), fs.readFileSync(filename, 'utf8'));
process.stdout.write(JSON.stringify({code: 'PNP_API_READY', zipBacked,
  pnp: process.versions.pnp, findPnpApi: true}));
`;

function descendant(removeOptions) {
  return `
const {spawnSync} = require('node:child_process');
const env = {...process.env};
${removeOptions ? "delete env.NODE_OPTIONS;" : ""}
const child = spawnSync(process.execPath, ['--eval', ${JSON.stringify(probe)}, process.argv[1]],
  {env, stdio: 'inherit'});
if (child.error) throw child.error;
process.exit(child.status ?? 1);
`;
}

export function pnpApiConformance({ binary, root, cache, environment = process.env, dependency = "@types/node/package.json" }) {
  const loader = join(resolve(root), ".pnp.cjs");
  assert(existsSync(loader), "An already prepared Yarn PnP fixture is required");
  const env = { ...environment };
  delete env.NODE_OPTIONS;
  const nodeOptions = { ...env, NODE_OPTIONS: `--require ${JSON.stringify(loader)}` };
  const cases = [
    ["filesystemOnly", ["--eval", probe, dependency], env, false],
    ["explicitRequire", ["--require", loader, "--eval", probe, dependency], env, true],
    ["requireNotInherited", ["--require", loader, "--eval", descendant(false), dependency], env, false],
    ["nodeOptionsInherited", ["--eval", descendant(false), dependency], nodeOptions, true],
    ["nodeOptionsRemoved", ["--eval", descendant(true), dependency], nodeOptions, false],
  ];
  const outcomes = {};
  for (const [name, args, childEnv, available] of cases) {
    const result = spawnSync(binary, ["--cache-dir", cache, "--color", "never", "run", "--", process.execPath, ...args],
      { cwd: root, env: childEnv, encoding: "utf8", timeout: 30_000, maxBuffer: 1024 * 1024 });
    assert.ifError(result.error);
    assert.equal(result.status, available ? 0 : 1, `${name}: ${result.stdout}\n${result.stderr}`);
    assert.deepEqual(JSON.parse(result.stdout), available
      ? { code: "PNP_API_READY", zipBacked: true, pnp: "3", findPnpApi: true }
      : { code: "MODULE_NOT_FOUND", zipBacked: true, pnp: null, findPnpApi: false }, name);
    outcomes[name] = { apiAvailable: available, exitCode: result.status };
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
    assert.ifError(cleaned.error);
    assert.equal(cleaned.status, 0, "Cannot clean the synthetic conformance cache");
    rmSync(temporary, { recursive: true, force: true });
  }
}
