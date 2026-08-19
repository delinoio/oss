import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { terminatePosixProcessGroup } from "../../../scripts/spawn-dev-server.mjs";
import { waitForChildClose } from "./platform-smoke-child.mjs";

const processTreeChildPath = fileURLToPath(new URL("./process-tree-child.mjs", import.meta.url));
const captureSource = readFileSync(fileURLToPath(new URL("../src-tauri/src/capture.rs", import.meta.url)), "utf8");
const tauriConfig = JSON.parse(readFileSync(fileURLToPath(new URL("../src-tauri/tauri.conf.json", import.meta.url)), "utf8"));
const macTauriConfig = JSON.parse(readFileSync(fileURLToPath(new URL("../src-tauri/tauri.macos.conf.json", import.meta.url)), "utf8"));
const macInfoPlist = readFileSync(fileURLToPath(new URL("../src-tauri/Info.desktop.plist", import.meta.url)), "utf8");
const koreanInfoPlist = readFileSync(fileURLToPath(new URL("../src-tauri/infoplist/ko.lproj/InfoPlist.strings", import.meta.url)), "utf8");

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

function assertProcessIsNotLive(pid) {
  try {
    process.kill(pid, 0);
  } catch (error) {
    assert.equal(error.code, "ESRCH");
    return;
  }
  if (process.platform === "linux") {
    const stat = readFileSync(`/proc/${pid}/stat`, "utf8");
    const [state] = stat.slice(stat.lastIndexOf(")") + 1).trim().split(/\s+/u);
    assert.ok(state === "Z" || state === "X");
    return;
  }
  assert.fail(`process ${pid} is still live`);
}

test("returns the exit code after a child closes", async () => {
  const child = spawn(process.execPath, ["-e", "process.exit(7)"], {
    stdio: "ignore",
    windowsHide: true,
  });

  assert.equal(await waitForChildClose(child, "normal", 5_000), 7);
});

test("desktop capture smoke contracts cover each supported native adapter", () => {
  assert.match(captureSource, /MacOsCaptureAdapter/u);
  assert.match(captureSource, /WindowsCaptureAdapter/u);
  assert.match(captureSource, /X11CaptureAdapter/u);
  assert.match(captureSource, /link\(name = "CoreGraphics"/u);
  assert.match(captureSource, /link\(name = "user32"/u);
  assert.match(captureSource, /link\(name = "X11"/u);
  assert.equal(tauriConfig.bundle.macOS.infoPlist, "Info.desktop.plist");
  assert.match(macInfoPlist, /NSScreenCaptureUsageDescription/u);
  assert.deepEqual(macTauriConfig.bundle.resources, {
    "infoplist/ko.lproj/InfoPlist.strings": "ko.lproj/InfoPlist.strings",
  });
  assert.match(koreanInfoPlist.trim(), /^NSScreenCaptureUsageDescription = ".+";$/u);
  assert.match(koreanInfoPlist, /화면/u);
});

test("waits for a timed-out child to close before rejecting", async () => {
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1_000)"], {
    detached: process.platform !== "win32",
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
  "does not treat Linux EPERM as process-group exit while a member is live",
  { skip: process.platform !== "linux", timeout: 5_000 },
  async (t) => {
    const child = spawn(
      process.execPath,
      [
        "-e",
        "process.on('SIGTERM', () => {}); process.send('ready'); setInterval(() => {}, 1_000)",
      ],
      {
        detached: true,
        stdio: ["ignore", "ignore", "ignore", "ipc"],
        windowsHide: true,
      },
    );
    const closePromise = once(child, "close");
    await once(child, "message");

    const originalKill = process.kill;
    t.after(async () => {
      process.kill = originalKill;
      if (child.exitCode === null && child.signalCode === null) {
        try {
          originalKill(-child.pid, "SIGKILL");
        } catch (error) {
          if (error.code !== "ESRCH") {
            throw error;
          }
        }
      }
      await closePromise;
    });

    process.kill = (pid, signal) => {
      if (pid === -child.pid && signal === 0) {
        const error = new Error("operation not permitted");
        error.code = "EPERM";
        throw error;
      }
      return originalKill(pid, signal);
    };

    let settled = false;
    const terminationPromise = terminatePosixProcessGroup(child, "SIGTERM").finally(() => {
      settled = true;
    });
    await new Promise((resolve) => setTimeout(resolve, 50));

    assert.equal(settled, false);

    originalKill(-child.pid, "SIGKILL");
    await terminationPromise;
  },
);

test(
  "terminates and awaits a timed-out process tree",
  { timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-smoke-process-tree-"));
    const statusPath = join(temporaryDirectory, "status.json");
    t.after(() => rmSync(temporaryDirectory, { force: true, recursive: true }));

    const child = spawn(process.execPath, [processTreeChildPath, "manager", statusPath], {
      detached: process.platform !== "win32",
      shell: false,
      stdio: "ignore",
      windowsHide: true,
    });
    const { pid, port } = await waitForStatus(statusPath);

    await assert.rejects(waitForChildClose(child, "hung-tree", 50), /hung-tree smoke timed out/u);
    assertProcessIsNotLive(pid);
    await listenOnPort(port);
  },
);
