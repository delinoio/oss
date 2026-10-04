// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

test("offline cache conformance runs every Vitest iteration through the supplied CLI", { skip: process.platform === "win32" }, () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "pnport-cache-gate-")));
  try {
    const fixture = join(root, "fixture");
    for (const format of ["inline", "split"]) mkdirSync(join(fixture, format), { recursive: true });
    const calls = join(root, "calls.jsonl");
    const binary = join(root, "pnport-fixture");
    writeFileSync(binary, `#!/usr/bin/env node
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
assert.equal(process.env.YARN_ENABLE_NETWORK, "0");
assert.equal(process.env.npm_config_offline, "true");
fs.appendFileSync(process.env.PNPORT_CONFORMANCE_CALLS, JSON.stringify({ args, cwd: process.cwd() }) + "\\n");
assert.equal(args[0], "--cache-dir");
if (args[2] === "doctor") {
  assert.deepEqual(args.slice(2), ["doctor", "--json"]);
  console.log(JSON.stringify({ ready: true }));
} else {
  assert.equal(args[2], "run");
  assert.equal(args[3], "--");
  const vitest = args[4] === "vitest";
  if (vitest) assert.deepEqual(args.slice(4), ["vitest", "run"]);
  else assert.equal(args[4], path.join(process.cwd(), "probe"));
  const cache = path.join(process.cwd(), "node_modules", vitest ? ".vite/vitest" : ".native-cache");
  fs.mkdirSync(cache, { recursive: true });
  fs.writeFileSync(path.join(cache, "results.json"), vitest ? "vitest fixture" : "cache");
}
`, { mode: 0o700 });
    execFileSync(process.execPath, [fileURLToPath(new URL("../scripts/cache-conformance.mjs", import.meta.url)),
      "run", fixture, binary], { env: { ...process.env, PNPORT_CONFORMANCE_CALLS: calls }, timeout: 30_000 });
    const recorded = readFileSync(calls, "utf8").trim().split("\n").map((line) => JSON.parse(line));
    for (const format of ["inline", "split"]) {
      const runs = recorded.filter(({ cwd, args }) => cwd === join(fixture, format) && args[2] === "run");
      assert.equal(runs.length, 4);
      assert.equal(runs.filter(({ args }) => args[4] === "vitest").length, 2);
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});
