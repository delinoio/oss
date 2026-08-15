import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { waitForChildClose } from "./platform-smoke-child.mjs";

const processTreeChildPath = fileURLToPath(new URL("./process-tree-child.mjs", import.meta.url));

async function waitForStatus(statusPath) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      return JSON.parse(readFileSync(statusPath, "utf8"));
    } catch (error) {
      if (error.code !== "ENOENT") {
        throw error;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("timed out waiting for the process-tree child status");
}

function listenOnPort(port) {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(port, "127.0.0.1", () => {
      server.close(resolve);
    });
  });
}

test("returns the exit code after a child closes", async () => {
  const child = spawn(process.execPath, ["-e", "process.exit(7)"], {
    stdio: "ignore",
    windowsHide: true,
  });

  assert.equal(await waitForChildClose(child, "normal", 5_000), 7);
});

test("waits for a timed-out child to close before rejecting", async () => {
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1_000)"], {
    stdio: "ignore",
    windowsHide: true,
  });
  await once(child, "spawn");
  let closed = false;
  child.once("close", () => {
    closed = true;
  });

  await assert.rejects(waitForChildClose(child, "hung", 50), /hung smoke timed out/u);
  assert.equal(closed, true);
});

test(
  "terminates and awaits a timed-out Windows process tree",
  { skip: process.platform !== "win32", timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-smoke-process-tree-"));
    const statusPath = join(temporaryDirectory, "status.json");
    t.after(() => rmSync(temporaryDirectory, { force: true, recursive: true }));

    const child = spawn(process.execPath, [processTreeChildPath, "manager", statusPath], {
      shell: false,
      stdio: "ignore",
      windowsHide: true,
    });
    const { pid, port } = await waitForStatus(statusPath);

    await assert.rejects(waitForChildClose(child, "hung-tree", 50), /hung-tree smoke timed out/u);
    assert.throws(() => process.kill(pid, 0));
    await listenOnPort(port);
  },
);
