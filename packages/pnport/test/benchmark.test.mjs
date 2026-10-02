// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { linkSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { diskUsage, processTreeRss, requireCleanSource, summarize } from "../scripts/benchmark.mjs";

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

test("revision records reject tracked, staged and untracked source changes but allow ignored output", () => {
  const root = mkdtempSync(join(tmpdir(), "pnport-benchmark-source-"));
  const git = (...args) => execFileSync("git", args, { cwd: root, encoding: "utf8", env: { ...process.env,
    GIT_AUTHOR_NAME: "Fixture", GIT_AUTHOR_EMAIL: "fixture@example.invalid", GIT_COMMITTER_NAME: "Fixture", GIT_COMMITTER_EMAIL: "fixture@example.invalid" } }).trim();
  try {
    git("init", "-q");
    writeFileSync(join(root, ".gitignore"), "generated\n");
    writeFileSync(join(root, "source"), "committed bytes\n");
    git("add", ".");
    git("update-ref", "HEAD", git("commit-tree", git("write-tree"), "-m", "Synthetic fixture"));
    requireCleanSource(root);
    writeFileSync(join(root, "generated"), "ignored measurement\n");
    requireCleanSource(root);
    writeFileSync(join(root, "source"), "uncommitted bytes\n");
    assert.throws(() => requireCleanSource(root));
    git("add", "source");
    assert.throws(() => requireCleanSource(root));
    git("reset", "--hard", "-q", "HEAD");
    writeFileSync(join(root, "untracked"), "new source\n");
    assert.throws(() => requireCleanSource(root));
  } finally { rmSync(root, { recursive: true, force: true }); }
});
