import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";

const scriptPath = fileURLToPath(new URL("./run-tauri.mjs", import.meta.url));
const processTreeChildPath = fileURLToPath(new URL("./process-tree-child.mjs", import.meta.url));
const configOverrides = [
  ["separated short option", ["build", "--", "-c", "alternate.json"]],
  ["equals-delimited short option", ["build", "--", "-c=alternate.json"]],
  ["attached short option", ["build", "--", "-calternate.json"]],
  ["separated long option", ["build", "--", "--config", "alternate.json"]],
  ["equals-delimited long option", ["build", "--", "--config=alternate.json"]],
];

for (const [name, args] of configOverrides) {
  test(`rejects the ${name}`, () => {
    const result = spawnSync(process.execPath, [scriptPath, ...args], {
      encoding: "utf8",
    });

    assert.equal(result.signal, null);
    assert.equal(result.status, 1);
    assert.match(
      result.stderr,
      /devhud: -c\/--config cannot override the pinned application, CSP, or development origin/,
    );
  });
}

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

test(
  "forwards an escalation signal while the child remains alive",
  { skip: process.platform === "win32", timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-signal-escalation-"));
    const statusPath = join(temporaryDirectory, "status.json");
    let pid;
    t.after(() => {
      if (pid) {
        try {
          process.kill(pid, "SIGKILL");
        } catch (error) {
          if (error.code !== "ESRCH") {
            throw error;
          }
        }
      }
      rmSync(temporaryDirectory, { force: true, recursive: true });
    });

    const resultPromise = spawnDevServer(
      process.execPath,
      [processTreeChildPath, "signal-escalation", statusPath],
      {
        shell: false,
        stdio: "ignore",
        windowsHide: true,
      },
    );
    ({ pid } = await waitForStatus(statusPath));

    process.emit("SIGINT");
    await new Promise((resolve) => setTimeout(resolve, 100));
    process.kill(pid, 0);

    process.emit("SIGTERM");
    assert.deepEqual(await resultPromise, { code: null, signal: "SIGTERM" });
  },
);

test(
  "terminates and awaits the complete process tree",
  { timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-process-tree-"));
    const statusPath = join(temporaryDirectory, "status.json");
    t.after(() => rmSync(temporaryDirectory, { force: true, recursive: true }));

    const resultPromise = spawnDevServer(
      process.execPath,
      [processTreeChildPath, "manager", statusPath],
      {
        shell: false,
        stdio: "ignore",
        windowsHide: true,
      },
      { terminateProcessTree: true },
    );
    const { pid, port } = await waitForStatus(statusPath);

    process.emit("SIGTERM");
    const result = await resultPromise;

    assert.deepEqual(result, { code: null, signal: "SIGTERM" });
    assert.throws(() => process.kill(pid, 0));
    await listenOnPort(port);
  },
);
