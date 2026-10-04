// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import childProcess from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { pnpApiConformance } from "../scripts/pnpapi-conformance.mjs";

const canary = "PRIVATE_CHILD_STREAM_CANARY";
const expected = { code: "PNP_API_READY", zipBacked: true, pnp: "3", findPnpApi: true };

function fixture(t, result) {
  const root = mkdtempSync(join(tmpdir(), "pnport-private-fixture-canary-"));
  writeFileSync(join(root, ".pnp.cjs"), "// Synthetic fixture; no child code executes.\n");
  t.mock.method(childProcess, "spawnSync", () => result);
  syncBuiltinESMExports();
  t.after(() => {
    t.mock.restoreAll();
    syncBuiltinESMExports();
    rmSync(root, { recursive: true, force: true });
  });
  return { root, run: () => pnpApiConformance({ binary: join(root, "binary-canary"), root, cache: join(root, "cache-canary") }) };
}

for (const [failureClass, result] of [
  ["nonzero-exit", { status: 37, stdout: canary, stderr: canary }],
  ["signal", { status: null, signal: "SIGKILL", stdout: canary, stderr: canary }],
  ["spawn-failed", { status: null, error: Object.assign(new Error(canary), { code: "ENOENT" }), stderr: canary }],
  ["timeout", { status: null, error: Object.assign(new Error(canary), { code: "ETIMEDOUT" }), stdout: canary }],
  ["output-limit", { status: null, error: Object.assign(new Error(canary), { code: "ENOBUFS" }), stdout: canary }],
  ["invalid-json", { status: 0, stdout: canary, stderr: canary }],
  ["unexpected-outcome", { status: 0, stdout: JSON.stringify({ ...expected, pnp: canary }) }],
]) test(`conformance ${failureClass} failures retain only typed diagnostic fields`, (t) => {
  const { root, run } = fixture(t, result);
  assert.throws(run, (error) => {
    assert(!error.stack.includes(canary), "Child contents must not enter the diagnostic");
    assert(!error.stack.includes(root), "Fixture paths must not enter the diagnostic");
    assert.equal(error.cause, undefined);
    assert.deepEqual(JSON.parse(error.message), { case: "automatic", failureClass, exitCode: result.status });
    return true;
  });
});

test("conformance rejects extra child output fields without exposing their values", (t) => {
  const { run } = fixture(t, { status: 0, stdout: JSON.stringify({ ...expected, extra: canary }) });
  assert.throws(run, (error) => {
    assert(!error.stack.includes(canary));
    assert.deepEqual(JSON.parse(error.message), { case: "automatic", failureClass: "unexpected-outcome", exitCode: 0 });
    return true;
  });
});

test("valid conformance output still reports each completed CommonJS case", (t) => {
  const { run } = fixture(t, { status: 0, stdout: JSON.stringify(expected) });
  const result = run();
  assert.equal(result.node, process.version);
  assert.equal(Object.keys(result.outcomes).length, 15);
  for (const outcome of Object.values(result.outcomes)) assert.deepEqual(outcome, { apiAvailable: true, exitCode: 0 });
});
