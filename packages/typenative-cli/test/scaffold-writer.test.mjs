import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import {
  mkdtemp,
  mkdir,
  lstat,
  rename,
  symlink,
  readFile,
  readdir,
  rm,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

test(
  "scaffold writer stays in its reserved directory after a symlink replacement",
  { skip: process.platform === "win32", timeout: 10000 },
  async () => {
    const root = await mkdtemp(join(tmpdir(), "typenative-writer-"));
    let child;
    try {
      const directory = join(root, "project"),
        moved = join(root, "reserved"),
        outside = join(root, "outside");
      await mkdir(directory);
      await mkdir(outside);
      const identity = await lstat(directory, { bigint: true });
      child = spawn(
        process.execPath,
        [
          fileURLToPath(new URL("../dist/scaffold-writer.js", import.meta.url)),
          String(identity.dev),
          String(identity.ino),
        ],
        { cwd: directory, stdio: ["pipe", "pipe", "pipe"] },
      );
      const closed = once(child, "close");
      await once(child.stdout, "data");
      await rename(directory, moved);
      await symlink(outside, directory);
      child.stdin.end(JSON.stringify({ "App.tsx": "reserved content" }));
      assert.equal((await closed)[0], 0);
      assert.equal(
        await readFile(join(moved, "App.tsx"), "utf8"),
        "reserved content",
      );
      assert.deepEqual(await readdir(outside), []);
    } finally {
      if (child && child.exitCode === null) child.kill("SIGKILL");
      await rm(root, { recursive: true, force: true });
    }
  },
);

test("scaffold writer rejects a replaced directory before receiving files", async () => {
  const root = await mkdtemp(join(tmpdir(), "typenative-writer-"));
  try {
    const child = spawn(
      process.execPath,
      [
        fileURLToPath(new URL("../dist/scaffold-writer.js", import.meta.url)),
        "-1",
        "-1",
      ],
      { cwd: root, stdio: ["ignore", "pipe", "pipe"] },
    );
    assert.equal((await once(child, "close"))[0], 1);
    assert.deepEqual(await readdir(root), []);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
