// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Worker } from "node:worker_threads";
import { setImmediate } from "node:timers/promises";

test("concurrent catalog preparation exposes only complete compiler inputs", async () => {
  const directory = mkdtempSync(join(tmpdir(), "delidev-catalog-"));
  const path = join(directory, "resources.ts");
  const values = ["export const catalog = '" + "a".repeat(32768) + "';\n", "export const catalog = '" + "b".repeat(32768) + "';\n"];
  writeFileSync(path, values[0]);
  const workers = [];
  try {
    let pending = 4, reads = 0;
    const completions = Array.from({ length: 4 }, (_, index) => {
      const worker = new Worker(`const { workerData } = require("node:worker_threads");
        import(workerData.module).then(({ writeLocalizationOutput }) => {
          for (let i = 0; i < 100; i++) writeLocalizationOutput(workerData.path, workerData.values[(i + workerData.index) % 2]);
        });`, { eval: true, workerData: { module: new URL("./localization-output.mjs", import.meta.url).href, path, values, index } });
      workers.push(worker);
      return new Promise((resolve, reject) => { worker.once("error", reject); worker.once("exit", code => { pending--; code === 0 ? resolve() : reject(new Error(`Writer exit ${code}`)); }); });
    });
    while (pending) { assert(values.includes(readFileSync(path, "utf8")), "Compiler input was truncated or partial"); reads++; await setImmediate(); }
    await Promise.all(completions);
    assert(reads > 0);
    assert.deepEqual(readdirSync(directory), ["resources.ts"]);
  } finally { await Promise.all(workers.map(worker => worker.terminate())); rmSync(directory, { recursive: true, force: true }); }
});
