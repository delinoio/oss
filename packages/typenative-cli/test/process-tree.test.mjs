import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { runProbeTool } from "../dist/core.js";

test(
  "aborting a native tool terminates its descendant process",
  { timeout: 15000 },
  async () => {
    const root = await mkdtemp(join(tmpdir(), "typenative-tree-"));
    const marker = join(root, "ready");
    const controller = new AbortController();
    const pending = runProbeTool(
      process.execPath,
      [
        "-e",
        `
    const {spawn}=require('node:child_process');
    const child=spawn(process.execPath,['-e', 'setInterval(()=>{},1000)'],{stdio:'inherit'});
    require('node:fs').writeFileSync(process.argv[1], String(child.pid));
    child.on('exit',()=>process.exit(0));
    setInterval(()=>{},1000);
  `,
        marker,
      ],
      root,
      controller.signal,
    );
    try {
      let pid;
      for (let i = 0; i < 200; i++) {
        try {
          pid = Number(await readFile(marker, "utf8"));
          if (pid) break;
        } catch {}
        await delay(25);
      }
      assert.ok(pid);
      controller.abort();
      assert.equal((await pending).failed, true);
      let running = true;
      for (let i = 0; i < 100; i++) {
        try {
          process.kill(pid, 0);
        } catch (error) {
          if (error.code === "ESRCH") {
            running = false;
            break;
          }
          throw error;
        }
        await delay(25);
      }
      assert.equal(running, false, "descendant must exit after cancellation");
    } finally {
      controller.abort();
      await pending;
      await rm(root, { recursive: true, force: true });
    }
  },
);
