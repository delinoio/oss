import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { applyCompilerPatch } from "../toolchain/apply-scriptc-patches.mjs";

const patch =
  "diff --git a/input.txt b/input.txt\n--- a/input.txt\n+++ b/input.txt\n@@ -1 +1 @@\n-before\n+after\n";
function fixture(run) {
  const root = mkdtempSync(join(tmpdir(), "typenative-patch-test-"));
  try {
    const git = (...args) =>
      execFileSync("git", ["-C", root, ...args], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      }).trim();
    git("init");
    writeFileSync(join(root, "input.txt"), "before\n");
    git("add", "input.txt");
    git(
      "-c",
      "user.name=TypeNative test",
      "-c",
      "user.email=typenative@example.invalid",
      "-c",
      "commit.gpgsign=false",
      "commit",
      "-m",
      "fixture",
    );
    const manifest = {
      schemaVersion: 1,
      revision: git("rev-parse", "HEAD"),
      patch: "compiler.patch",
      sha256: createHash("sha256").update(patch).digest("hex"),
    };
    const manifestPath = join(root, "manifest.json");
    const save = () => writeFileSync(manifestPath, JSON.stringify(manifest));
    writeFileSync(join(root, manifest.patch), patch);
    save();
    run({ root, manifest, manifestPath, save });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test("checks without mutation, then applies the pinned compiler patch", () =>
  fixture(({ root, manifestPath }) => {
    assert.equal(
      applyCompilerPatch(root, { manifestPath, checkOnly: true }).code,
      "TN_COMPILER_PATCH_CHECKED",
    );
    assert.equal(readFileSync(join(root, "input.txt"), "utf8"), "before\n");
    assert.equal(
      applyCompilerPatch(root, { manifestPath }).code,
      "TN_COMPILER_PATCH_APPLIED",
    );
    assert.equal(readFileSync(join(root, "input.txt"), "utf8"), "after\n");
    assert.throws(
      () => applyCompilerPatch(root, { manifestPath }),
      /TN_COMPILER_SOURCE_DIRTY/,
    );
  }));

test("rejects corrupt patch content before changing source", () =>
  fixture(({ root, manifestPath }) => {
    writeFileSync(join(root, "compiler.patch"), patch + "corruption\n");
    assert.throws(
      () => applyCompilerPatch(root, { manifestPath }),
      /TN_COMPILER_PATCH_INTEGRITY/,
    );
    assert.equal(readFileSync(join(root, "input.txt"), "utf8"), "before\n");
  }));

test("rejects a different source revision", () =>
  fixture(({ root, manifest, manifestPath, save }) => {
    manifest.revision = "0".repeat(40);
    save();
    assert.throws(
      () => applyCompilerPatch(root, { manifestPath }),
      /TN_COMPILER_SOURCE_REVISION_MISMATCH/,
    );
    assert.equal(readFileSync(join(root, "input.txt"), "utf8"), "before\n");
  }));

test("preserves existing source edits", () =>
  fixture(({ root, manifestPath }) => {
    writeFileSync(join(root, "input.txt"), "user edit\n");
    assert.throws(
      () => applyCompilerPatch(root, { manifestPath }),
      /TN_COMPILER_SOURCE_DIRTY/,
    );
    assert.equal(readFileSync(join(root, "input.txt"), "utf8"), "user edit\n");
  }));

test("rejects a manifest path escape", () =>
  fixture(({ root, manifest, manifestPath, save }) => {
    manifest.patch = "../compiler.patch";
    save();
    assert.throws(
      () => applyCompilerPatch(root, { manifestPath }),
      /TN_COMPILER_PATCH_MANIFEST_INVALID/,
    );
  }));
