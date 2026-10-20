// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { runBuildTool } from "../examples/native-todo/build-tool.mjs";

async function ready(path) {
  for (let i = 0; i < 200; i++) {
    try {
      return Number(await readFile(path, "utf8"));
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    await delay(10);
  }
  throw new Error("Tool descendant did not start");
}

async function stopped(pid) {
  for (let i = 0; i < 200; i++) {
    try {
      process.kill(pid, 0);
      if (
        process.platform === "linux" &&
        /\) Z /.test(await readFile(`/proc/${pid}/stat`, "utf8"))
      )
        return;
    } catch (error) {
      if (["ESRCH", "ENOENT"].includes(error.code)) return;
      throw error;
    }
    await delay(10);
  }
  assert.fail("Tool descendant is still running");
}

for (const mode of ["abort", "timeout", "parent-exit"]) {
  test(
    `todo build ${mode} stops descendants that retain pipes and ignore SIGTERM`,
    { skip: process.platform === "win32", timeout: 10_000 },
    async () => {
      const root = await mkdtemp(join(tmpdir(), "typenative-todo-tree-"));
      const marker = join(root, "descendant");
      const controller = new AbortController();
      const descendant = `process.on('SIGTERM',()=>{});require('node:fs').writeFileSync(process.argv[1],String(process.pid));setInterval(()=>{},1000)`;
      const parent = `const {spawn}=require('node:child_process');const child=spawn(process.execPath,['-e',${JSON.stringify(descendant)},process.argv[1]],{stdio:'inherit'});process.on('SIGTERM',()=>{});child.on('exit',()=>process.exit(0));${mode === "parent-exit" ? "setTimeout(()=>process.exit(0),100)" : "setInterval(()=>{},1000)"}`;
      const pending = runBuildTool(process.execPath, ["-e", parent, marker], {
        cwd: root,
        signal: controller.signal,
        timeoutMs: mode === "abort" ? 8000 : 1000,
        killDelayMs: 50,
      });
      try {
        const pid = await ready(marker);
        if (mode === "abort") controller.abort();
        const result = await pending;
        assert.equal(result.aborted, mode === "abort");
        assert.equal(result.timedOut, mode !== "abort");
        await stopped(pid);
      } finally {
        controller.abort();
        await pending;
        await rm(root, { recursive: true, force: true });
      }
    },
  );
}
