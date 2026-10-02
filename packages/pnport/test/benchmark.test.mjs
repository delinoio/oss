// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { linkSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { diskUsage, processTreeRss, summarize } from "../scripts/benchmark.mjs";

test("sample summaries include slow outliers and require repeated finite measurements", () => {
  assert.deepEqual(summarize([10, 2, 3, 1, 100]), { median: 3, min: 1, max: 100 });
  assert.deepEqual(summarize([10, 2, 3, 1, 100, 5]), { median: 4, min: 1, max: 100 });
  assert.throws(() => summarize([1]));
  assert.throws(() => summarize([1, 2, 3, 4, NaN]));
});

test("RSS samples include nested descendants regardless of row order and exclude other jobs", () => {
  assert.equal(processTreeRss(" 30 20 7\n40 1 900\n20 10 5\n10 1 3\n50 40 600", 10), 15 * 1024);
  assert.equal(processTreeRss("10 1 3\n20 1 5", 99), 0);
  assert.throws(() => processTreeRss("10 1 invalid", 10));
});

test("disk measurements count hard-linked bytes once and do not traverse external symlinks", { skip: process.platform === "win32" }, () => {
  const root = mkdtempSync(join(tmpdir(), "pnport-benchmark-test-"));
  try {
    const cache = join(root, "cache");
    mkdirSync(cache);
    writeFileSync(join(cache, "a"), "abc");
    linkSync(join(cache, "a"), join(cache, "b"));
    writeFileSync(join(root, "external"), "not cache data".repeat(100));
    symlinkSync("../external", join(cache, "link"));
    const measured = diskUsage(cache);
    assert.equal(measured.files, 2);
    assert.equal(measured.logicalBytes, 3 + "../external".length);
    assert(measured.allocatedBytes >= 0);
    assert.deepEqual(diskUsage(join(root, "missing")), { logicalBytes: 0, allocatedBytes: 0, files: 0 });
  } finally { rmSync(root, { recursive: true, force: true }); }
});
