// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { lstat, mkdir, mkdtemp, realpath, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { externalBuildPath } from "../examples/native-todo/build-paths.mjs";

test(
  "todo build rejects symlinked dependencies, outputs and caches inside its repository",
  { skip: process.platform === "win32" },
  async () => {
    const root = await mkdtemp(join(tmpdir(), "typenative-todo-paths-"));
    try {
      const repo = join(root, "repository");
      const outside = join(root, "outside");
      await mkdir(join(repo, "tools"), { recursive: true });
      await mkdir(outside);
      const alias = join(outside, "repository-link");
      await symlink(repo, alias);
      for (const path of [
        join(alias, "tools"),
        join(alias, "new-output"),
        join(alias, "missing", "target"),
        alias,
      ]) {
        await assert.rejects(
          externalBuildPath(repo, path),
          /outside the repository/,
        );
      }
      const dangling = join(outside, "future-repository-directory");
      await symlink(join(repo, "not-created"), dangling);
      await assert.rejects(
        externalBuildPath(repo, join(dangling, "target")),
        /outside the repository/,
      );
      await assert.rejects(lstat(join(repo, "not-created")), {
        code: "ENOENT",
      });
      await assert.rejects(lstat(join(repo, "new-output")), { code: "ENOENT" });
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  },
);

test(
  "todo build preserves genuine external symlink paths and resolves missing descendants",
  { skip: process.platform === "win32" },
  async () => {
    const root = await mkdtemp(join(tmpdir(), "typenative-todo-paths-"));
    try {
      const repo = join(root, "repository");
      const external = join(root, "external");
      await mkdir(repo);
      await mkdir(external);
      const alias = join(root, "external-link");
      await symlink(external, alias);
      const canonical = await realpath(external);
      assert.equal(await externalBuildPath(repo, alias), canonical);
      assert.equal(
        await externalBuildPath(repo, join(alias, "new", "target")),
        join(canonical, "new", "target"),
      );
      await assert.rejects(lstat(join(external, "new")), { code: "ENOENT" });
      const loop = join(root, "loop");
      await symlink(loop, loop);
      await assert.rejects(externalBuildPath(repo, loop), { code: "ELOOP" });
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  },
);
